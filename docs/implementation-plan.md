# Implementation Plan — `vm-control-plane`

A small, production-shaped VM control plane in Go: gRPC API → PostgreSQL desired state → reconciler → scheduler → hypervisor agent → libvirt/KVM, with Open vSwitch networking and qcow2 storage. Architecturally it follows the common shape of libvirt-based compute platforms (the OpenStack Nova / KubeVirt lineage): a state-owning control plane, a per-host agent wrapping the hypervisor, and level-triggered reconciliation between them.

**Status:** v6 — fifth adversarial revision. v2: feasibility ground truth. v3: concurrency skeleton. v4: grants, fencing, epoch ownership. v5: single-tx transitions, admission predicates, host-daemon topology. v6: lease-aware commit guards, operations that *always* terminate, session-generation report ordering, action repair incarnations, host-aggregate capacity, live+inactive drift with clean-shutdown attestations, a libvirt connection-supervisor (go-libvirt RPCs cannot be context-cancelled — designed around, not assumed away), durable teardown, an hours-estimated executable PR DAG, and a concrete failure branch · **Repo:** `github.com/sigtunnel/vm-control-plane` · **Author:** Daniel (documented AI-pair-programming exercise; decisions recorded here and in ADRs)

---

## Table of contents

1. [Purpose and positioning](#1-purpose-and-positioning)
2. [Verified environment and constraints](#2-verified-environment-and-constraints)
3. [Goals and non-goals](#3-goals-and-non-goals)
4. [Architecture overview](#4-architecture-overview)
5. [Design decisions and rationale](#5-design-decisions-and-rationale)
6. [State model and concurrency protocol](#6-state-model-and-concurrency-protocol)
7. [The vertical slice and its failure boundaries](#7-the-vertical-slice-and-its-failure-boundaries)
8. [Local development strategy: three tiers](#8-local-development-strategy-three-tiers)
9. [Milestones, scope, and PR dependency graph](#9-milestones-scope-and-pr-dependency-graph)
10. [Testing strategy](#10-testing-strategy)
11. [Observability plan](#11-observability-plan)
12. [Demo script: the eight essential demonstrations](#12-demo-script-the-eight-essential-demonstrations)
13. [Engineering process and quality gates](#13-engineering-process-and-quality-gates)
14. [Risks and mitigations](#14-risks-and-mitigations)
15. [Definition of done — two tiers](#15-definition-of-done--two-tiers)
16. [Immediate action items](#16-immediate-action-items)

---

## 1. Purpose and positioning

A portfolio project for an infrastructure-engineering interview — primarily a **software-engineering assessment**: control-plane design skill (desired-state reconciliation, idempotency, crash recovery, scheduling, fencing) over real hypervisor, network, and storage layers.

> My professional strength is infrastructure control planes. I built this project to deepen my hands-on understanding of the hypervisor, networking, and storage layers those control planes orchestrate.

Positioning rules: the public repo is **company-agnostic** (lineage-described; company mapping lives in private gitignored notes), and the **"why not KubeVirt / Nova?"** answer is prepared: those are systems to *operate*; this builds the *mechanisms*, small enough to defend every line.

```
vmctl create vm web-1 --image ubuntu-24.04 --cpu 2 --memory 2GiB \
      --network production-a --volume workspace:10GiB
```

…returns an async Operation that **always terminates**, schedules onto a logical node, provisions a sized qcow2 overlay from a digest-verified cache, boots a real KVM domain on a VLAN-isolated OVS port, and converges observed state back into PostgreSQL — with the hard interleavings treated as named protocols with named failpoint tests.

## 2. Verified environment and constraints

Verified on the development machine (2026-08-12):

| Fact | Value | Consequence |
|---|---|---|
| OS | Windows 11 Pro | Native dev loop, **natively CI-tested** (Windows runner) |
| CPU | i9-12900HK, VT-x enabled, SLAT | Nested KVM viable inside a VirtualBox Ubuntu guest |
| Substrate host | **VirtualBox 7.2.8; Hyper-V OFF (both verified)** — an Ubuntu guest with `--nested-hw-virt on` gets working /dev/kvm | Tier 1 = Ubuntu VM under VirtualBox (WSL explicitly not wanted); the spike runs inside it, is M0 work, and gates M3+ |
| Go | 1.26.5 windows/amd64 | Ready |
| Docker / containers | Absent everywhere | Standalone pinned observability binaries; embedded PostgreSQL |
| buf / protoc-gen-go / golangci-lint | **Installed + verified during planning** (1.72.0 / 1.36.12 / 2.12.2) | PR 2 bootstrap proven |
| codex CLI | 0.146.0, `gpt-5.6-sol`, ultra verified | Advisory reviewer per PR |
| GitHub | `danielmapar` valid; `devsigtunnel` token invalid; repo visible to neither | Pushing blocked until fixed (§16) |
| embedded-postgres | Smoke-tested: PG 18.3, 16.4 s cold | Viable (D11) |
| go-libvirt / libovsdb | Compiled on windows/amd64 here | Pure-Go holds — **but go-libvirt mutation RPCs take no context** (e.g. `DomainDefineXML`), so cancellation is a designed connection-supervisor concern (D8), and the M0 spike includes an exact-library Go probe |
| Substrate kernel & OVS | A real Ubuntu VM kernel ships the openvswitch module | Kernel datapath expected; the spike still capability-detects rather than assumes |
| GitHub-hosted KVM | Documented experimental/unsupported | Three-lane CI; unavailable KVM is a **neutral capability result**; release gate is provenance-based (§8) |

Hard constraints: (1) all code compiles and unit-tests on Windows; real drivers `//go:build linux`; both-GOOS builds + native Windows tests in CI; (2) real KVM = an Ubuntu VM under VirtualBox (nested VT-x) post-spike + capability-gated CI lane; (3) working copy leaves OneDrive before code lands.

## 3. Goals and non-goals

### Goals

- **G1 — One excellent vertical slice**, create and delete, every failure boundary designed and tested.
- **G2 — Crash-safe by construction, including the hard interleavings**, each a named protocol with a named test.
- **G3 — Real substrate:** libvirt/KVM, OVS integration bridge with VLAN isolation, sized qcow2 overlays from a verified cache, offline snapshots with durable manifests.
- **G4 — Multi-node scheduling** with the host/node split modeled honestly, including **host-aggregate capacity**: logical-node quota sums are validated against host allocatable (no overcommit in v0.1, stated).
- **G5 — Semantic idempotency for the complete mutation inventory** (§5 D3) — user *and* admin verbs.
- **G6 — Drift reconciliation** over **live and next-boot (inactive) configuration plus autostart**, via owned-field projection, owner- and epoch-scoped.
- **G7 — Observable**, async-honest tracing (span links).
- **G8 — Portfolio artifacts:** design doc, ADRs (kept in lockstep — stale ADRs are treated as bugs), educational PRs with acceptance evidence, demos, honest statistics, scale analysis, limitations, tagged release.
- **G9 — Runs on the author's machine:** fake tier natively on Windows; real KVM in an Ubuntu VM under VirtualBox (nested VT-x) on the same machine.

### Non-goals

- **N1 — Not a private cloud.**
- **N2 — No evacuation of possibly-running VMs.** Reschedule only never-granted placements; granted-but-unreachable parks `Unknown` with allocations quarantined. No force-forget in v0.1.
- **N3 — No authn/authz** (token → mTLS growth path).
- **N4 — Cross-host tenant networking out of scope.** Guarantee: tenant-NIC VLAN segmentation on one host's bridge + management network that is NAT-with-guest-isolation. Both halves positively tested.
- **N5 — No HA control plane** (single active controller; protocol is multi-worker-safe).
- **N6 — No Windows/macOS hypervisors.**
- **N7 — Stretch (post-MVP only):** streaming intents, clones, live snapshots, multi-disk snapshots, dashboards, operation TTL, overcommit policies.

## 4. Architecture overview

```
        vmctl (CLI)
          │ gRPC
          ▼
┌───────────────────────────────┐        ┌───────────────────────────────┐
│ control-plane                 │        │ hypervisor-agent (per HOST)   │
│  ├─ api        (gRPC services)│  gRPC  │  ├─ logical nodes: node-a,-b  │
│  ├─ reconciler (claim queue)  │◄──────►│  ├─ intent poller + grants    │
│  ├─ scheduler  (filter/score) │        │  ├─ per-VM serial executors   │
│  └─ store      (PostgreSQL)   │        │  ├─ libvirt conn supervisor   │
└───────────────┬───────────────┘        │  └─ drivers (libvirt/OVS/img) │
                ▼                        └──────────────┬────────────────┘
           PostgreSQL                           libvirtd / OVS / qemu-img
```

- **`cmd/control-plane`**: gRPC API, reconciler, scheduler behind `--role` flags; strict package boundaries.
- **`cmd/hypervisor-agent`** — **one process per physical host** (exclusive host lock, persistent `host_id`), advertising logical nodes whose **quota sums are validated against host allocatable**. Node sessions carry a **session generation** and bind to `host_id`. The daemon polls intent snapshots, requests execution grants before first substrate action, runs per-VM serialized executors, drives libvirt through a **connection supervisor** (deadline → poison/close → fail in-flight → reconnect → re-observe before retrying ambiguous mutations), and reports through a per-session sequencer.
- **`cmd/vmctl`**: `create`, `get`, `describe`, `list`, `start`, `stop`, `delete`, `snapshot`, `op wait` (with a defensive client deadline), `debug drift`, `admin retry-cleanup`, `admin clear-recovery`.
- **`proto/vmc/v1`**: resources, revision vocabulary, Operations with target revisions and **per-verb deadlines**, idempotency envelopes on the full mutation inventory.
- **PostgreSQL**: desired + observed state, placements, claims, action-retry records (with repair generations), reservations, IP allocations, envelopes, operations, placement tombstones, snapshot manifests, clean-shutdown attestations, events.

Layout: as v5 (`cmd/`, `internal/{api,reconciler,scheduler,store,agent,driver/*,faults,obs,clock,netproxy}`, `proto/vmc/v1`, `docs`, `scripts`, `deploy`).

## 5. Design decisions and rationale

**D1 — Go.** Control-plane lingua franca; static binaries; goroutines map to executors and loops.

**D2 — One `control-plane` binary with role flags.** Strict package seam; `--role` demonstrates the split.

**D3 — API contract: Operations that always terminate; envelopes for the complete mutation inventory; explicit power semantics.**
- Operations carry resource UUID, verb, target desired revision, canonical request hash, **and a per-verb database-clock deadline**. Termination is total, not just retry-driven: realized → `Done`; newer revision first → `Superseded`; budget exhausted or permanent error → `Failed` (same tx as the phase write); **deadline expiry → `DeadlineExceeded` terminalization *without* unsafe cleanup** — desired state may stay pending (`Unschedulable` keeps waiting), an exposed placement stays `Unknown`, a quarantined delete keeps running in the background; late convergence never rewrites an immutable terminal result. `op wait` has a defensive client-side deadline too. Tests: indefinitely-Unschedulable create, grant-then-host-loss, unreachable delete.
- **The mutation inventory is explicit and complete** — every verb below carries an idempotency envelope (method + API version + target + canonical hash; envelope row is the serialization point; no TTL): VM create / power update / delete / snapshot create / snapshot restore; Volume create / delete; Network create / delete; `admin retry-cleanup`; `admin clear-recovery`. **`retry-cleanup` advances a retry *generation* under CAS**, so a duplicated or delayed retry-cleanup cannot reset attempts consumed after the original request. Replay/mismatch/concurrency tests cover the admin verbs as well.
- **Mutable in v0.1: desired power state only.**
- **Power semantics:** Stop = ACPI shutdown → poll → deadline (60 s default) → forced destroy, escalation recorded; Delete same ladder, shorter deadline. **Clean-shutdown attestation:** on stop, the daemon records libvirt's stopped-event *detail* (normal shutdown vs destroyed/crashed); a persistent attestation scoped (vm, epoch, stop revision) is written only for normal shutdown with no destroy escalation, **cleared on every Start, failing closed when the reason is unavailable**. Snapshot admission requires the attestation transactionally — "observed ShutOff" alone can race a crash or forced destroy.

**D4 — PostgreSQL state + queue; token claims; lease-aware single-transaction transitions.** Claims mint an immutable token with a lease (atomic `UPDATE … RETURNING`). **The transition guard is token + input versions + `claim_expires_at > clock_timestamp()` — checked in the same statement that commits the transition.** A token does not outlive its lease: an expired worker cannot commit even if takeover hasn't happened yet; takeover serializes on the claim row. All durable side effects of a transition (including reservation + placement) commit in that one guarded transaction. Durable retry state rides the same rows. Tests: expiry-without-takeover, takeover race, renewal-vs-completion, completion under observation traffic, expired-worker-attempts-scheduling.

**D5 — Level-triggered reconciliation; durable retry at both boundaries; repair incarnations; bounded work.**
- Controller-side: durable budgets → `Failed` + Operation terminalized; `Unschedulable` is a condition; explicit retry = spec touch or generation-fenced `admin retry-cleanup`.
- **Agent actions carry repair incarnations:** retry records are keyed (vm, epoch, revision, action, **action_generation**) with an immutable attempt-token history. A successful action later needing same-revision drift repair opens a *new generation* rather than reusing a terminal record. Only the current generation/token may advance work, under a CAS that also checks epoch, revision, tombstone, and the RecoveryRequired latch; ambiguous results (timeout, lost response) require **re-observation before retry**; attempts are delivered only when the database clock says they are due (`not_before`). Tests: same-revision repair, late success after replacement / deletion / terminal failure, duplicate results, restart-resets-nothing.
- Deadlines on every driver call **via the connection supervisor** (D8 — the RPCs themselves are not cancellable); `PR_SET_PDEATHSIG` for children; startup scavenger with quiescence proof; bounded worker pool.

**D6 — Scheduler: all-predicate reservations; schema-enforced placement; host-aggregate capacity.** Filter/score as before; one conditional `UPDATE` rechecks every hard predicate; epoch allocation under the VM row lock; **partial unique index: one active placement per VM**; idempotent `DELETE … RETURNING` releases. **Host aggregation:** the daemon registers host allocatable; logical-node quota sums are validated ≤ host allocatable at registration and on any quota change (rejected otherwise); v0.1 has no overcommit (stated policy). Tests: concurrent placements on different logical nodes of one host stay within host capacity; quota change with live reservations.

**D7 — Agent protocol: host daemon, admission-checked grants, serialization, session-generation-ordered evidence.**
- **Topology:** one daemon per host (lock + `host_id`), N logical nodes; sessions bind to host; cross-host node-ID rebinding rejected while exposure exists.
- **Grants:** no substrate action before a committed grant for (vm, epoch); first-grant admission requires current session + host binding + unexpired lease (server clock) + current placement + current revision + no tombstone, under the documented lock order (VM row → placement row); replay idempotent after commit; delayed grants after expiry/replacement/delete fail admission.
- **Serialization + tombstones:** per-VM executor, latest revision wins, revalidation around every external step; tombstone intents; **teardown receipts** distinct from status reports (a stale-epoch receipt may update exactly its ledger row); snapshot-token-gated GC.
- **Ordered evidence across restarts:** reports are ordered by **(session_generation, report_seq)** compared lexicographically — the sequencer persists its session generation, so a new session starting at seq 1 is not rejected behind the old session's seq 100, and a delayed old-session high-seq report *is* rejected. Test: new-session-low-seq then delayed-old-session-high-seq.
- **Fingerprints (see D8) drive drift**; reports carry (session gen, seq, epoch, applied revision, fingerprints).

**D8 — libvirt through pure Go, with the client's limits designed around.**
- `digitalocean/go-libvirt` (compiles on Windows; real driver `//go:build linux`). **Its mutation RPCs take no context** — a goroutine timeout does not cancel a blocked call — so the agent uses a **connection supervisor**: per-call deadlines are enforced by poisoning/closing the connection, failing all in-flight calls, reconnecting, and **re-observing state before retrying any ambiguous mutation** (define/start may have landed). The M0 spike includes an exact-library Go probe: blackholed RPCs, lost response after a successful mutation, concurrent connection replacement, bounded goroutine counts.
- **Drift covers next boot, not just now:** for a running persistent domain the daemon projects owned fields from **both** live XML and the `INACTIVE` (next-boot) definition — `virsh edit` tampering is invisible to live-only comparison — and owns **autostart=false** (checked, since autostart is outside the XML and would boot an unfenced domain after host restart). Live-only drift raises a **`RestartRequired` condition** rather than an automatic disruptive restart (policy stated; conservative default). Tests: inactive-edit drift, live-device tamper, autostart tamper + libvirtd restart, benign augmentation no-drift.
- Hand-built domain XML with goldens (pure, Windows-testable).

**D9 — Networking** (as v5, unchanged in substance): host singletons atomically created+stamped (single OVSDB transaction; define-time metadata for `vmc-mgmt`); one integration bridge per host; networks = unique VLAN tags; deterministic MAC + interfaceid; narrow adoption proof for the per-port pre-stamp window; NAT-with-guest-isolation management plane; capability-detected datapath; positive + negative isolation battery.

**D10 — Storage: durable in both directions; strict cache; manifested snapshots.**
- Publication: temp → `fdatasync` → no-replace publish → **fsync parent dir**; parents durably created; epoch-qualified paths; validated ensures (`qemu-img info --backing-chain`).
- **Teardown is as durable as publication:** unlink → **fsync containing directory**; epoch-directory removal → fsync its parent — **before the teardown receipt is issued**; same ordering for cache eviction. An fsync failure marks teardown incomplete (ledger debt + refcounts retained, startup rescan). Failpoints straddle unlink/fsync/receipt. Without this, a host crash after receipt could resurrect an artifact whose reservation and backing refcount were already released.
- Cache admission: digest-pinned, `sha256` + `qemu-img check`, **standalone base only** (no own backing, no external data file, no encryption, bounded size, feature whitelist).
- Snapshots: durable manifests (vm, epoch, snapshot, disk) with inspect/resume rules; `Creating|Restoring → Verifying → Ready | Failed`; **admission requires the clean-shutdown attestation** (D3); RecoveryRequired latch blocks Start until verify or `admin clear-recovery` (enveloped); single-disk v0.1; kill-mid-create/restore on real qemu-img in the always-on lane.

**D11 — Embedded PostgreSQL** (pinned = CI, UTF-8/locale C, per-instance dirs/ports, serialized extraction, per-package TestMain, non-admin shell, cold-cache CI).

**D12 — Driver fakes are first-class** (latency/failure hooks); fake volume/network from the first E2E milestone.

**D13 — Observability, async-honest** (persisted origin context; span links; standalone pinned Prometheus + Jaeger).

**D14 — Idempotency at three layers:** envelopes; tokens + lease-aware single-tx transitions; validated ensures + durable publication/teardown + epoch ownership.

**D15 — Privilege/filesystem model tested through the whole lifecycle** (as v5: service-UID acceptance incl. OVSDB rights surviving daemon restart; dynamic-ownership contract; guest write → clean stop → snapshot → restore → reboot → delete under real identities; containment rules; loopback listeners).

## 6. State model and concurrency protocol

Fields/owners/fences (all row mutations CAS `resource_version`):

| Field | Writer | Guard / fence |
|---|---|---|
| `spec` (power-only mutable), `spec_generation` | API | CAS; immutability rules |
| `deleted_at` | API | set-once; lock order VM row → placement row |
| `desired_revision` | store | agents never act on older |
| `placements` (vm, epoch): assigned → granted → torn_down; node, host_id | scheduler (VM-row lock; **partial unique active**); grant transition (admission predicates) | grant ⊻ unassign ⊻ delete on one row |
| `claims`: token, lease, attempts, next_attempt_at, computed-from | workers | token + versions + **unexpired lease at commit** |
| `action_retries` (vm, epoch, revision, action, **generation**): attempts, not_before, token history | reconciler writes; daemon consumes | current-generation CAS incl. tombstone/latch; ambiguous → re-observe |
| `observed`: state, applied revision, fingerprints (live+inactive), **(session_gen, report_seq)** | report handler | lexicographic ordering; session/epoch fenced |
| `phase` + conditions (incl. `RestartRequired`) | reconciler only | CAS |
| `reservations` (vm+epoch unique), `ip_allocations` (network+address unique) | scheduler / API+reconciler | idempotent DELETE…RETURNING in guarded txs |
| `operations` (verb, target revision, hash, **deadline**) | API creates; reconciler terminalizes | every path terminates (D3) |
| `placement_tombstones` | reconciler | teardown receipts only; survive rows |
| `snapshot_manifests`, `shutdown_attestations` | reconciler + receipts | inspect/resume; attestation cleared on Start, fail-closed |
| idempotency envelopes (full inventory incl. admin verbs) | API | serialization point; retry-generation CAS for retry-cleanup |

Phase machine as v5, plus `RestartRequired` condition. **The six protocols** as v5, with protocol 1 amended: *transition commit requires an unexpired lease in the committing statement*, and protocol 5 amended: *(session_generation, report_seq) lexicographic*.

## 7. The vertical slice and its failure boundaries

Slice steps as v5 (envelope → guarded transition with scheduling → admission-checked grant → durable ensure → atomic singletons → define/start/stamp → sequenced evidence → exact-revision completion), with deletion adding **durable teardown**: unlink + dir fsync **before** the receipt, ledger retention on fsync failure.

Failure matrix (failpoint IDs are assigned in the checked-in manifest at PR 10; until then rows cite design anchors). ★ = also real-substrate in CI:

| # | Failure | Recovery | Mechanism |
|---|---|---|---|
| 1 | Retry / mismatch / concurrency on any inventory verb (incl. admin) | Original op / `FAILED_PRECONDITION` / serialized | D3 |
| 2 | Duplicate `retry-cleanup` after newer attempts | Retry-generation CAS rejects | D3 |
| 3 | Controller dies anywhere | Durable claims/retry; rescan | D4/D5 |
| 4 | **Expired worker attempts commit (no takeover yet)** | Commit statement requires unexpired lease → rolls back | D4 |
| 5 | Concurrent/double placement; cross-node host oversubscription | Partial unique; all-predicate reservation; **quota-sum ≤ host allocatable** | D6 |
| 6 | Agent acts, grant response lost | Grant committed → exposure recorded; idempotent replay | D7 |
| 7 | Delayed grant after expiry/replacement/delete | Admission fails | D7 |
| 8 | Node dies, never granted | Unassign + release → reschedule | D7 |
| 9 | Node dies after grant | `Unknown`; quarantine; **operation deadline still terminalizes** | D7/D3 |
| 10 | Stale daemon vs replacement; cross-host node-ID reuse | Host lock; host_id binding | D7 |
| 11 | ★ Daemon dies mid-provision (incl. mid-`qemu-img`) | Redelivery (not_before); scavenger; validated ensure | D5/D10 |
| 12 | ★ Dies before port stamp / singleton stamp | Adoption proof / impossible (atomic tx) | D9 |
| 13 | **Blocked/blackholed libvirt RPC; lost response after successful mutation** | Supervisor poisons connection; re-observe before retry | D8 |
| 14 | Old-epoch cleanup after new placement | Epoch paths + ownership re-read | §6 |
| 15 | ★ Delete races any provisioning step | Post-step revalidation | D7 |
| 16 | Crash mid-delete; **host crash after unlink before receipt** | Durable-teardown ordering; ledger retains debt on fsync failure | D10 |
| 17 | **New-session low seq vs delayed old-session high seq** | (session_gen, seq) lexicographic | D7 |
| 18 | Duplicate intents; agent restart; **same-revision drift repair** | Revision monotonicity; action generations | D5 |
| 19 | ★ Live tamper / **inactive (next-boot) edit / autostart tamper** vs benign augmentation | Dual-XML owned-field projection + autostart ownership; `RestartRequired` for live-only drift | D8 |
| 20 | Never converges / stalls without retries (Unschedulable, Unknown, unreachable delete) | **Per-verb operation deadlines terminalize; no unsafe cleanup** | D3 |
| 21 | ★ Kill mid-snapshot; qemu-img success + lost commit; **crash/destroy during ACPI window** | Manifests + attestation fail-closed; latch blocks Start | D10/D3 |
| 22 | ENOSPC at publication; daemon restarts (libvirtd, ovsdb); half-open partition on lease | Clean failure; supervisor reconnect; action halt | §10 |
| 23 | PostgreSQL restarts | Back off, resume | D4 |

**Invariant auditor:** phase-aware (as v5), plus: no artifact without a ledger/manifest record after teardown receipts; attestation state consistent with power history.

## 8. Local development strategy: three tiers

**Tier 0** (as v5): native Windows, embedded PG, one host daemon + two logical nodes, loopback, stable paths, non-admin.

**Tier 1 — an Ubuntu 24.04 VM under VirtualBox (nested VT-x), gated on the M0 spike** under the exact service identity, also proving: **the go-libvirt connection-supervisor probe** (blackholed RPC, lost-response re-observation), OVSDB rights surviving daemon restart, full ownership cycle, datapath detection (a real VM kernel ships the openvswitch module). `scripts/spike/vbox-create.ps1` provisions the VM — `--nested-hw-virt on` is the load-bearing flag, and Hyper-V is verified off so VirtualBox passes raw VT-x through. The spike report (a) gates M3+, (b) re-estimates the schedule, (c) **selects the concrete fallback venue on a NO-GO** — a rented nested-virt-capable Linux host is provisioned then, or the degraded-scope matrix (§9) is invoked; "some Linux somewhere" is not a plan.

**Tier 2 — CI, three lanes** (as v5: portable+race+stress; no-KVM Linux substrate lane incl. qemu-img kill batteries, OVSDB-under-UID, seed read-back; capability-gated guest lane with hard preflight). **Release gate, tightened:** the tag workflow verifies a passing real-KVM artifact **for the exact release commit** — SHA, image digest, runner identity, acceleration preflight, results — from the substrate VM or the fallback venue; unavailable hosted KVM is a neutral capability result, never a pass.

## 9. Milestones, scope, and PR dependency graph

**Scope honesty.** Interview-MVP = through PR 24. Full v0.1.0 = all 32. **Budget in engineer-hours** (per-PR estimates below are ±50% until the two reforecast points: after M0's spike and after the first fake E2E): M0 ≈ 14 h, M1 ≈ 26 h, M2 ≈ 70 h, M3a ≈ 40 h, M3b ≈ 48 h, M4 ≈ 40 h → ≈ 240 h + 30–50% integration contingency ⇒ **MVP ≈ 3–5 focused weeks; full v0.1.0 ≈ 8–11 part-time weeks.** Two cut lines, both pre-decided: **substrate-triggered** (spike fails → cut OVS + snapshots; demo 4 falls back to Linux-bridge isolation with the OVS design documented; DoD-B's demo table swaps accordingly) and **schedule-triggered** (MVP not done at the reforecast date → cut PRs 25–28 to stretch and ship DoD-A + chaos + docs; independent of substrate luck).

Ground rules unchanged (one idea per PR; Deps/Produces/Accepts columns; protos and demos land with their PRs). **Acceptance always sits in the first PR where the complete behavior exists.**

### M0 — Foundations & ground truth (≈14 h)
| PR | Title | Deps | Est | Produces / Accepts |
|---|---|---|---|---|
| 1 | Docs | — | 4 h | Plan, design doc, ADRs 1–6 **(v6-consistent — stale ADRs are bugs)** / render + diagram check |
| 2 | Scaffolding + CI lane 1 | — | 6 h | Layout, lint, both-GOOS, Linux+Windows runners, race lane, buf bootstrap / green on empty tree |
| 3 | Substrate spike + report | VirtualBox VM (user provisions) | 4 h + spike time | Go/no-go incl. **go-libvirt probe**; fallback-venue decision; re-estimate / report committed; **gates M3+** |

### M1 — API and state (≈26 h)
| PR | Title | Deps | Est | Produces / Accepts |
|---|---|---|---|---|
| 4 | Proto: VM + Operation + envelope | 2 | 5 h | vmc/v1 core incl. per-verb deadlines / buf lint+generate |
| 5 | Store I | 2 | 7 h | Embedded-PG harness, migrations, VM repo / CAS tests |
| 6 | Store II | 5 | 7 h | Operations (deadline terminalization), envelopes for the **full inventory** / matrix 1–2 |
| 7 | API server | 4,6 | 5 h | CRUD (delete = tombstone), validation, deadlines / demo 7 seed |
| 8 | vmctl | 7 | 2 h | CLI, `op wait` with client deadline / golden tests |

### M2 — Control loop, fake tier (≈70 h)
| PR | Title | Deps | Est | Produces / Accepts |
|---|---|---|---|---|
| 9 | Claim queue | 6 | 8 h | Token claims, **lease-aware commit guard**, durable retry, bounded pool / matrix 3–4 |
| 10 | Faults + harness core | 2 | 6 h | Failpoint registry + **manifest with IDs**, injectable clock, partition proxy / meta-tests |
| 11 | Harness II + auditor | 10 | 6 h | In-process + subprocess harnesses, phase-aware auditor / auditor meta-tests |
| 12 | Host daemon + logical nodes | 9,11 | 8 h | host_id, host lock, sessions **with generations**, leases, **host-aggregate quota validation** / matrix 10; quota tests |
| 13 | Scheduler + placements | 9,12 | 8 h | All-predicate reservations, VM-row-lock epochs, partial unique, idempotent release / matrix 5 |
| 14 | Grants | 12,13 | 6 h | Admission predicates, lock order, idempotent replay / matrix 6–8 |
| 15 | Intents + action retries + fake drivers | 14 | 8 h | Snapshot tokens, **action generations + not_before**, fake compute+volume+network / matrix 18 |
| 16 | Evidence path | 15 | 6 h | Report sequencer, (session_gen, seq) ordering, status materialization / matrix 17 |
| 17 | Tier-0 E2E | 7,8,15,16 | 6 h | Supervisor, `make dev`, create→Running on 2 logical nodes / demo 2 v1, demo 6a |
| 18 | UpdateVM + power | 7,8,15 | 5 h | Power updates, stop ladder + **attestations (fake)** / demo 7 final |
| 19 | Drift + node loss | 16,17 | 7 h | Dual-fingerprint drift (fake), session replacement, reschedule policy, teardown receipts, quarantine / matrix 7–9, 19-fake; demo 6b, 3-fake |
| 20 | Deletion + ledger | 18,19 | 6 h | Tombstone intents, **durable-teardown ordering (fake fs)**, staged finalization, ledger + consumer, `admin retry-cleanup` (generation-fenced) / matrix 2, 14–16; **Tier 0 complete** |

### M3a — Minimal real slice (gated on PR 3) (≈40 h)
| PR | Title | Deps | Est | Produces / Accepts |
|---|---|---|---|---|
| 21 | CI lanes 2+3 | 2,3 | 6 h | Substrate lane, guest lane, artifacts, **release-gate provenance workflow** / lane smoke |
| 22 | Domain XML + seed | 4 | 8 h | domainxml goldens (identity, epoch, isolated mgmt, autostart); pure-Go ISO (CIDATA verified, ssh keys, MAC-matched net-config) / lane-2 read-back |
| 23 | Real qcow2 core | 21 | 10 h | Sized overlays, cache admission, **publication + teardown durability**, scavenger, manifest primitives / ★ matrix 11-img, 16-durability in lane 2 |
| 24 | Mgmt net + libvirt lifecycle | 16,18,19,20,22,23 | 16 h | Owned `vmc-mgmt`, **connection supervisor**, define/start/stop-ladder/undefine, dual-XML fingerprints, DHCP discovery, ownership cycle / ★ boot→SSH→stop→delete→drift in guest lane + substrate VM; matrix 13, 19-real; demos 1, 3-real |

### M3b — Full real substrate (≈48 h)
| PR | Title | Deps | Est | Produces / Accepts |
|---|---|---|---|---|
| 25 | Volume resource + refcounts | 20,23 | 8 h | Attach model, finalization extension / refcount tests |
| 26 | Snapshots | 18,23,24,25 | 14 h | Manifests, create+restore machines, **attestation admission**, latch + `admin clear-recovery` / ★ matrix 21 (lane 2 + guest); demo 5; **D15 full cycle** |
| 27 | Network resource + IPAM | 20 | 8 h | CIDR allocations, VLAN uniqueness, deletion guards / constraint tests |
| 28 | OVS driver | 24,27 | 18 h | Atomic bridge+stamp, adoption proof, verify/repair, isolation battery / ★ matrix 12, 19-OVS; demo 4 |

### M4 — Proof and polish (≈40 h)
| PR | Title | Deps | Est | Produces / Accepts |
|---|---|---|---|---|
| 29 | Chaos completion | 20,24,26,28* | 12 h | Full matrix, half-open partitions, restart/ENOSPC batteries, seeded chaos + replay / demos 2/8 final (*26/28 drop out under a cut line, and the matrix shrinks with them) |
| 30 | Tracing + stack | 17 | 8 h | Span links, standalone Prom/Jaeger / trace walkthrough |
| 31 | Hardening | 29 | 8 h | Graceful shutdown, leader gate, ledger-driven GC audit / containment tests |
| 32 | Benchmarks + release | 29,30,31 | 12 h | Median/IQR/raw distributions; scale analysis; limitations; recording; **exact-SHA provenance release gate**; v0.1.0 / §15-B |

## 10. Testing strategy

As v5 (failpoint manifest with IDs from PR 10; injectable clock; directional/half-open partition proxy; two harnesses; race + stress lane; substrate battery incl. daemon restarts and ENOSPC; guest battery incl. full ownership cycle and isolation battery; named semantic tests), plus v6 additions: **expiry-without-takeover; new-session-low-seq ordering; same-revision repair generations; blackholed-RPC / lost-response supervisor probes; inactive-XML and autostart drift; attestation fail-closed cases; durable-teardown failpoints (unlink/fsync/receipt); host-aggregate quota races; admin-verb envelope replay tests.**

## 11. Observability plan

As v5, plus `vmc_operation_deadline_expirations_total{verb}`, `vmc_report_rejections_total{reason}`, `vmc_connection_supervisor_resets_total`.

## 12. Demo script: the eight essential demonstrations

As v5's table, with demo 1 additionally narrating the stop ladder + attestation, demo 3 showing inactive-XML drift detection, and demo 5 showing attestation-gated snapshot admission. Tier assignments unchanged.

## 13. Engineering process and quality gates

As v5: merge gates are engineering artifacts (acceptance evidence, matrix rows, auditor, CI lanes); AI review advisory with recorded triage; ADRs in lockstep (stale ADR = bug, fixed in the same PR); conventional commits; protected main.

## 14. Risks and mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| GitHub auth (push blocked) | **Current fact** | §16; local branches |
| Spike surprises (KVM/OVS/go-libvirt probe) | Medium | M0 gate; concrete fallback venue decided at spike time; degraded-scope matrix pre-written |
| Hosted-runner KVM experimental | Documented | Three lanes; exact-SHA provenance release gate |
| OneDrive vs `.git` | High if unaddressed | Move to `C:\dev` before PR 1 |
| Schedule optimism | Acknowledged | Hour-level estimates ±50%, two reforecast points, schedule-triggered cut line independent of substrate |
| Protocol complexity creep | Medium | Six protocols are the complete list; more requires a plan change |

## 15. Definition of done — two tiers

**A. Interview MVP (PRs 1–24):** fake tier complete (matrix rows on fakes: 1–10, 14–20, 22–23) + real slice (boot → key-only SSH → stop ladder → delete → drift; ★ rows 11, 13, 19; lane-2 storage battery); demos 1, 2, 3, 6, 7; docs/ADRs current; Tier-0 quickstart ≤ 5 min.

**B. Full v0.1.0 (PRs 1–32):** entire matrix with manifest-mapped failpoints (shrunk per any invoked cut line, with the substitution table applied); all eight demos per §12 (or the degraded matrix's substitutes); recording; **exact-SHA provenance release gate**; honest statistics + scale analysis + limitations; `v0.1.0` tagged.

## 16. Immediate action items

**For Daniel:**
1. `gh auth login -h github.com` for `devsigtunnel`; confirm `sigtunnel/vm-control-plane` exists under that exact name.
2. Approve moving the working copy to `C:\dev\vm-control-plane` at implementation kickoff.
3. Admin PowerShell: `wsl --install -d Ubuntu-24.04`, reboot — PR 3 (spike incl. go-libvirt probe) runs immediately after and gates M3+.

**Process:** educational walkthrough → design-doc approval → implementation per §9 with recorded review triage per PR.
