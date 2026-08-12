# ADR-0004: Agent protocol — execution grants, host lock, per-VM serialization, fencing

**Status:** Accepted · **Date:** 2026-08-12

## Context

The control plane must never dial into hosts (NAT/firewall reality; restart simplicity), so agents pull intents. Pull raises four safety questions this ADR answers: (1) when is it safe to reschedule a placement from a dead node? (2) what stops a *stale agent process* from mutating the substrate after its replacement starts? (3) what stops a slow provisioning step from resurrecting a deleted VM? (4) how do reports prove *which* desired revision they realized?

## Decision

1. **Execution grants (the exposure proof).** "No ack received" is *not* proof an intent was never acted on — an agent can act before its ack commits. So the agent performs **no substrate action** for a placement until it requests an execution grant for (vm, placement_epoch) and receives confirmation the grant **committed**. Grant and unassignment serialize on the same `placements` row; exactly one wins. Exposure is sticky for the epoch. Reschedule is legal **only** for never-granted placements; anything else parks in `Unknown` until the node confirms — two VMs is strictly worse than one late VM.
2. **Host-daemon topology + action fencing.** **One agent process per physical host** — it holds the host's exclusive lock and a persistent `host_id`, and advertises one or more **logical nodes** (scheduling partitions with their own capacities, labels, and leases, whose quota sums are validated against host allocatable). This is what makes "two nodes on one laptop" coherent: one writer for the substrate, multiple partitions for the scheduler. Node sessions carry a **session generation** and bind to `host_id`; a node identity cannot rebind to another host while exposure exists. A replacement daemon cannot act until the old one loses the lock; session/lease loss halts new external operations; destructive steps re-read artifact ownership immediately before executing. The first-grant transition additionally checks **admission predicates** — current session bound to this host, unexpired server-clock lease, current placement and revision, no committed tombstone — under a documented lock order (VM row → placement row) shared with deletion, so a delayed grant after expiry, replacement, or delete fails admission instead of creating sticky exposure.
3. **Per-VM serialization.** One executor goroutine per VM; signals coalesce to the latest desired revision; the revision is revalidated **before and after every external step** — which is what makes "delete supersedes provision" a protocol. Deletion travels as a tombstone intent retained until that placement's teardown confirms; local GC runs only after a complete intent snapshot for the current session.
4. **Evidence-bearing, ordered reports.** Reports carry (session generation, per-VM **report_seq**, epoch, **applied_desired_revision**, realized-spec fingerprints) and are accepted in **(session_generation, report_seq)** lexicographic order — a restarted daemon's fresh session starting at seq 1 is not rejected behind the old session's high sequence, while a delayed old-session report is. Stale sessions/epochs are rejected for status; **teardown receipts are a separate message type** permitted to update exactly their matching placement-tombstone ledger row. Operations complete only on exact-revision evidence.

**Ownership marks** (node + VM UUID + epoch) go on every artifact: libvirt domain `<metadata>`, OVS `external_ids` and deterministic `interfaceid`, epoch-qualified volume/seed/temp paths. Resync, drift, GC, and every destroy are owner+epoch-scoped.

## Security growth path (out of scope for v0.1.0)

Registration is trusting today. The designed path: per-node bootstrap token issued at enrollment → mTLS with node identity → grants and intents only over the authenticated channel.

## Consequences

- Two *logical nodes* sharing one libvirtd via the single host daemon is sound: one substrate writer, owner-scoped observation; tested explicitly (concurrent logical nodes, same-host replacement, cross-host node-ID reuse rejected).
- The partition-after-start case has a correct, honest answer (`Unknown`), and the never-granted case a safe reschedule — both tested with a partition proxy and grant/unassign race tests.

## Alternatives considered

- Push (control plane dials agents): worse failure modes, no offline recovery story.
- Reschedule on heartbeat loss alone: the classic split-brain generator; rejected.
