# Backups, restore and disaster recovery

**Goal.** Recover application data when the original VM storage is unavailable. Start with a powered-off independent backup; add live capture only after a clean restore works.

**Read first.**  [libvirt full-disk backup](https://www.libvirt.org/kbase/live_full_disk_backup.html),  [state-capture trade-offs](https://www.libvirt.org/kbase/domainstatecapture.html),  [backup XML](https://libvirt.org/formatbackup.html) and the chosen application’s consistency guidance.

**Quick model.** RPO is the maximum acceptable data-loss window; RTO is the target maximum time to restore service. A snapshot is not an independent backup, and a bootable disk is not proof of application consistency.

## 7.1 Install the service

1. Install the Appendix A SQLite-backed HTTP service on linux-01. Inside the guest, run sudo apt update and sudo apt install python3 curl sqlite3, then follow Appendix A for exact paths, permissions, the systemd unit and curl checks. Keep its unauthenticated port on the disposable lab NAT network, never a bridged/public network.

2. Save the source and unit in the chapter repository. For example, run curl --fail --data-binary A http://127.0.0.1:8080/records, then repeat for B and C as Appendix A shows. GET must show three distinct IDs/values, and PRAGMA integrity\_check must return ok. These acknowledged records are the data checked in later restore and migration labs. If using another application, document its write/flush and recovery behavior first.

**Check:** A–C have distinct acknowledged IDs and values; SQLite integrity\_check returns ok.

## 7.2 Set recovery targets

1. Define concrete RPO and RTO targets for this lab, for example at most one acknowledged record lost and a measured 30-minute restore target. Those numbers are **learner targets**, not guarantees.

2. Inventory all linux-01 disks, its inactive XML, network definition/dependencies, firmware and NVRAM, guest/application state, seed provenance, access keys and any TPM/encryption recovery material. Keep protected material out of the Git notebook.

**Check:** RPO/RTO targets and all components needed for same-VM recovery are inventoried.

## 7.3 Capture offline backup

1. Stop writes, shut down linux-01 cleanly, and verify the QEMU process is gone. Save inactive XML and a manifest of exact disk paths. Flatten its **inactive** qcow2 chain to an independent backup image using qemu-img convert -O qcow2 SOURCE\_OVERLAY DESTINATION\_BACKUP, with enough free space and a destination independent of the original storage.

2. Record image digest and check that the backup has no backing dependency. Copy required configuration/firmware state consistently and protect the complete backup set. A disk image alone is not a complete VM recovery kit.

**Check:** The backup image has no backing dependency and its complete protected recovery set is recorded.

## 7.4 Restore the same VM

1. Restore on an isolated clean target with the original disk path and host unavailable to the operator. Adjust domain paths, network identifiers and firmware references deliberately.

2. **Do not run original and restored same-identity VM together.** Boot, query records A–C, write D, and check database integrity with SQLite’s integrity\_check and the application’s own readback. Measure actual lost records and wall-clock recovery time.

**Check:** Isolated restore retrieves A–C, accepts D and reports measured loss and recovery time.

## 7.5 Add live/incremental modes

1. Corrupt a *copy* of the backup or omit a required component. Digest/inventory checks must reject it; restore from the intact set again.

2. If supported by the installed libvirt/QEMU stack, use its documented disk backup API for a live capture with a short, tested application-consistency or freeze/thaw window. Verify automatic thaw/cleanup after a failed capture, then independently restore that set.

3. If checkpoint-based incrementals are supported, take a *new coordinated full baseline*, save its checkpoint ID, add a known record, capture the incremental, and restore the full-plus-incremental chain to an isolated target. Verify old and new records. Never pair this incremental with the unrelated earlier full image; mark unsupported work outstanding.

**Check:** The damaged set is rejected; each supported live or incremental set independently restores the expected records and application integrity.

**Pass and cleanup.** From the runbook and independent backup alone, another person can recover A–C with measured RPO/RTO and verified SQLite/app data. Keep the clean restore as evidence; remove only named disposable targets. New clones get new identity, while recovery of the same VM may need its original firmware/TPM/encryption state. Reuse this inventory for Windows in chapter 11.
