# AI review triage — PRs 2–3

Reviewer: codex `gpt-5.6-sol`, reasoning effort ultra. Scores at review time:
PR 2 = 7/10, PR 3 = 2/10. Findings triaged below; this record lands in the PR
descriptions when the stack is pushed. (The triage record — including
rejections — is the evidence of judgment; see plan §13.)

## PR 2 (scaffolding)

| Finding | Decision | Action / reason |
|---|---|---|
| Race lane covered only `./internal/...`; no stress job | **Accepted** | Race now runs `./...`; bounded stress job added (`-race -count=3`, 15 m timeout), scope grows with hot packages |
| `make dev` default target + README quickstart not runnable at this commit | **Accepted** | `build` is the default target; `dev` fails with a pointer to its owning PR; quickstart labeled "after the Tier-0 E2E PR" |
| Makefile Windows portability (GOBIN, POSIX-only recipes) | **Partially accepted** | GOBIN now honors `go env GOBIN` with GOPATH/bin fallback and is quoted. POSIX-sh recipes kept **deliberately**: the Makefile is documented as an optional thin wrapper, ezwinports make ships an sh, and every target's go command is runnable directly — an OS-conditional Makefile would add complexity to a convenience layer |
| PR template lacks Depends/Produces/Accepts/Deferred | **Accepted** | Fields added — mirrors the plan §9 dependency-graph contract |
| Unanchored `proto` lint exclusion could hide hand-written code | **Accepted** | Restricted to `proto/.*\.pb\.go$` |

## PR 3 (spike)

| Finding | Decision | Action / reason |
|---|---|---|
| Report pending — PR not mergeable per its own contract | **Accepted (process)** | PR 3 will be opened as a **Draft** and merges only with committed spike results. It stays in the stack because M1–M2 do not depend on it and the scripts are the mechanism for producing the report |
| Probe does not establish D8 (blackholed a read; never lost a mutation response) | **Accepted** | Probe rewritten: a response-gating proxy withholds the `DomainDefineXML` response; an independent observer confirms the domain landed while the caller is still blocked (the ambiguous-outcome window made visible); poison; blocked call must fail promptly; re-observe + run-scoped cleanup |
| Blackhole implementation racy | **Accepted** | Replaced by the mutex-guarded gate proxy; no state shared with Read paths |
| Scripts committed non-executable (100644) | **Accepted** | `update-index --chmod=+x` |
| `sudo -n false` chain returned echo's status; netdev path guaranteed NO-GO | **Accepted** | Single `ovs-vsctl -- add-br -- set bridge …` transaction for both datapaths |
| `$USER` under nounset; groups inactive in current shell; no Go preflight; OVS socket perms don't survive restart | **Accepted** | `id -un`; two-phase run with explicit re-login step and effective-identity verification; Go preflight; systemd drop-in re-applies socket perms on every daemon start (and the spike checks restart survival) |
| Fixed resource names make reruns unsafe | **Accepted** | Run-scoped names (`vmc-spike-<id>`), trap cleanup of only this run's resources, run-scoped NAT subnet |
| Full boot/DHCP/SSH/ownership cycle missing | **Accepted** | `--full` mode: cloud image + ephemeral ed25519 key + NoCloud seed, DHCP lease wait, key-only SSH to `cloud-init status --wait`, storage write through QEMU, ownership-aware teardown |
| Failure aggregation unreliable; logs overwritten | **Accepted** | Per-check log files retained under a run log dir with provenance header (identity, host, commit); transcript appended; exit status = battery result |
