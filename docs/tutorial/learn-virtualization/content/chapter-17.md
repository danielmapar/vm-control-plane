# A resilient control plane and custom observability

**Goal:** make automation correct when requests, events, processes and hosts fail. **Requires:** chapter 16 and the chapter-12 program. **Budget:** 14–20 sessions — the largest software deliverable in the course.

**Read first:**  [libvirt API concepts](https://libvirt.org/api.html), the  [domain statistics/event documentation](https://libvirt.org/html/libvirt-libvirt-domain.html), and one production comparison such as  [KubeVirt architecture](https://kubevirt.io/user-guide/architecture/). Trace the corresponding libvirt-driver code using the supplementary internals references.

**Reference design for the two-host lab** — implement exactly this floor before extending:

- **Process model:** one active controller with a local SQLite store. Refuse a second instance via an exclusive process lock held for the controller's lifetime. This is a recoverable single controller, not a highly available one.

- **Persisted record, written before the first external mutation:** `{VM UUID, owner marker, requested host, desired presence, desired power state, spec, generation}`. Assign UUIDs once, not per retry.

- **State separation:** observed domain state is kept apart from job state (`Pending`, `Applying`, `Unknown`, `Succeeded`, `Failed`).

- **Concurrency:** serialize work per VM, reserve destination capacity across simultaneous jobs, retain an operation record before each external action.

- **Reconcile loop:** load desired state → observe both hosts → check ownership/capacity → perform at most one safe action → observe again before recording completion.

- **Safety rules:** the controller's metadata marker limits which domains it manages — it is *not* a disk lock or fencing mechanism, and unmarked domains and their disks are never modified. If either possible owner is unreachable, record `Unknown` and stop placement/deletion/retry until observation or the chapter-16 fencing procedure establishes ownership. Do not restart a VM elsewhere because a connection timed out.

- **Timing:** inventory every 60 seconds initially; events trigger earlier reconciliation. These are adjustable lab defaults, not latency guarantees.

![Persist intent, observe both hosts, require known ownership and capacity, take one safe action, then observe again. Unknown ownership blocks mutation.](assets/image14.png)

*Figure 9. Reconciliation resolves uncertain outcomes by observing state. A timeout does not authorize a second owner.*

## 17.1 Desired state and reconcile loop

1. Implement the store and a loop converging observed state to desired state for create and delete.

2. Test first against a fake libvirt adapter: crash before the request, after remote success, and before recording success. Use the same UUID on recovery.

**Check:** replay converges without duplicate VMs or accidental deletion; then repeat against disposable real domains.

## 17.2 Jobs, drain and admission

Add long-running job tracking, host drain, and admission checks against the chapter-8 capacity policy.

**Check:** a drain migrates or stops marked VMs, refuses over-capacity placements, and completes with recorded per-VM outcomes.

## 17.3 Reconnect and unknown outcomes

1. Register the  [connection-close callback](https://libvirt.org/html/libvirt-libvirt-host.html#virConnectRegisterCloseCallback); on reconnect, re-register event handling and fully resynchronize.

2. Treat callbacks as connection-based notifications; periodic inventory is part of the design, not an optimization.

3. A local timeout does not prove remote cancellation: after uncertainty, observe actual state before retrying a mutation. This is a conservative controller-design requirement, not an exactly-once guarantee supplied by libvirt.

**Check:** explain what happens after every uncertain outcome, with bounded retries.

## 17.4 Bulk-stats exporter

Build a Go exporter on the  [bulk domain statistics API](https://libvirt.org/html/libvirt-libvirt-domain.html#virConnectGetAllDomainStats) for both hosts, collecting every 15 seconds by default. Retain absent fields as absent rather than zero, handle counter resets and host changes, and bound resource usage. `NOWAIT` can permit partial statistics; it is not a general RPC deadline.

**Check:** tested alerts for guest availability, CPU pressure, memory pressure, storage latency/capacity and collector loss, with thresholds tuned from the measured baseline.

## 17.5 Controller-store recovery

1. Stop the controller for a consistent SQLite copy; back it up with schema/version, configuration and protected management credentials; restore to a clean controller location.

2. Prove the old controller cannot still run before starting its replacement.

3. Start in observe-only mode, reconcile the recovered inventory and generations with both hosts, and require operator review before enabling mutations if the backup is stale.

**Check:** recovery neither invents ownership nor deletes resources omitted by an older store. This is recoverability of a single-controller design, not controller HA.

## 17.6 Fault-injection matrix

Run and record each: controller killed mid-operation; connection lost while a request completes; events missed while disconnected; concurrent conflicting requests; restart from persisted state alone; host unreachable during drain.

**Check:** safe convergence after the dependency recovers, or an explicit blocked/`Unknown` state when ownership cannot be proved. No duplicate VMs, simultaneous owners, accidental deletion or untracked resources. On delete, verify the ownership inventory and retain recoverable disk backups until the operation has been reviewed; never derive deletion targets from client-supplied paths.

**Cloud-init delivery** is per-instance and access-controlled; never expose a public directory of seed secrets. **Chapter pass:** the matrix passes; the assessment is correctness of eventual state and side effects, not a fabricated exactly-once claim. **Recovery:** the controller manages only marked lab domains. **Further reading (comparative):** the official/pure-Go binding comparison, KubeVirt and Nova call paths, Incus and Proxmox designs, Terraform/OpenTofu and Ansible integrations, exporter references and monitoring runbooks. Compare one alternative deeply; reading four orchestrators is not a prerequisite.
