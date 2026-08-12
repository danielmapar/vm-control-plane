# ADR-0006: Storage — sized qcow2 overlays, verified cache, validated ensures, offline snapshot state machines

**Status:** Accepted · **Date:** 2026-08-12

## Context

`qemu-img` gives full visibility into image mechanics (the learning goal), but its sharp edges are exactly where naive control planes corrupt data: backing-format requirements, default-size inheritance, partial files passing existence checks, and write operations against images a running QEMU holds.

## Decision

- **Creation:** `qemu-img create -f qcow2 -b <cache> -F qcow2 <tmp> <size-bytes>` — the explicit `-F` is required by modern qemu-img, and the explicit size is required or the requested size is silently ignored in favor of the backing image's. Temp-create → fsync → **no-replace publication** under an **epoch-qualified path** (`/var/lib/vmc/<node>/<vm>/<epoch>/`).
- **Validated ensure:** a pre-existing final file is accepted only after `qemu-img info --output=json --backing-chain` confirms format, virtual size, canonical backing digest/path, and ownership. Existence is not evidence.
- **Cache:** content-addressed (`sha256-…`), URL + digest pinned, verified (`sha256` + `qemu-img check`) before publication; backing files refcounted while children reference them.
- **Snapshots — offline only, with full state machines:** `Creating → Verifying → Ready | Failed` and `Restoring → Verifying → Ready | Failed`. The API requires desired power `Stopped`; the reconciler waits for observed `ShutOff` while the VM's serialized executor holds the lifecycle (no start can interleave). Mechanism: qcow2 internal snapshots through QEMU's own locking — never unsafe shared-open flags, never `qemu-img` against a live image. `qemu-img check` gates `Ready`; check failures park as `Failed` for **manual recovery** (auto-repair can mask corruption). Kill-mid-create and kill-mid-restore are tested with guest-data verification.
- **Volume model:** root disk embedded in the VM spec; standalone `Volume` resources attach at create with a readiness gate and a deletion refcount guard. **Clones and live snapshots are stretch** (external-snapshot re-parent flip is the designed path).

- **Teardown is as durable as publication:** unlink → fsync the containing directory, and epoch-directory removal → fsync its parent, **before** the teardown receipt is issued (same ordering for cache eviction). An fsync failure marks teardown incomplete — ledger debt and refcounts are retained and rescanned at startup. Without this, a host crash after the receipt could resurrect an artifact whose reservation and backing refcount were already released.
- **Snapshot admission requires a clean-shutdown attestation** — recorded from libvirt's stopped-event detail (normal shutdown, no destroy escalation), scoped (vm, epoch, stop revision), cleared on every Start, failing closed when the reason is unavailable. "Observed ShutOff" alone can race a crash or forced destroy.

## Consequences

- Kill-anywhere holds for storage: partial files are garbage by construction, and the scavenger + validation close the remaining windows.
- Demo 5 shows a real backing chain and a real snapshot/restore with the state machine visible.

## Alternatives considered

- libvirt storage pools: hides the mechanics this project exists to learn.
- Live snapshots via libvirt external snapshots: correct but a large surface; deferred deliberately.
