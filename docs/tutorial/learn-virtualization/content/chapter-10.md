# Incident response and the operator milestone

**Goal.** Solve unknown faults with evidence, recover the service and leave the platform in a known state.

**Read first.**  [libvirt debug logging](https://libvirt.org/kbase/debuglogs.html), the installed host distribution’s troubleshooting guide and targeted QMP/tracing references only when the symptom calls for them.

## 10.1 Freeze the baseline

1. Record: host/guest versions, linux-01 owner, domain XML, disk chain, network path, application records, backup manifest, current alert state and a tested console path. Give an instructor or peer a list of **allowed disposable fault targets**, never authority to damage the management NIC, host root, sole backup or production data.

**Check:** The baseline and permitted disposable fault targets are frozen before injection.

## 10.2 Follow the runbook

1. For each unknown symptom: establish impact → inspect recent changes → localize host/libvirt/QEMU/guest/network/storage/application layer → capture minimal evidence → test one hypothesis → recover → verify application records and security boundaries → prevent recurrence. Save commands and observations, not only the final fix.

**Check:** Each diagnosis records impact, one tested hypothesis, recovery and verification.

## 10.3 Diagnose unknown faults

1. Ask the helper to inject several faults you do not see in advance: wrong boot/firmware setting, missing backing file, lab-only storage pressure, DHCP/DNS, firewall/MTU, confinement, guest-agent failure or migration incompatibility. For each, predict the failing layer and use the known-good comparison.

2. For solo practice use the fault-bank procedure under Milestones and assessment, and label the result self-assessed. Time-box verbose libvirt/QEMU logging, preserve the original settings and restore them after diagnosis.

**Check:** Several unfamiliar faults are repaired without persistent security or configuration drift.

## 10.4 Recover the service

1. Independently restore linux-01’s service from the chapter-7 backup to a clean target while the original host/storage are inaccessible. Verify SQLite/app data and measured RPO/RTO. Then complete a host-a maintenance cycle with host-b and verify that a single owner served requests before and after.

**Check:** Independent restore and maintenance preserve verified service data and one owner.

## 10.5 Report the result

1. Sanitize reports before sharing: XML, logs and traces can contain host topology, private paths or VNC/SPICE passwords. Include the relevant daemon/QEMU log excerpt, versions, XML, timeline and a backtrace when appropriate, without publishing secrets.

**Check:** The incident report is useful without disclosing secrets.

**Pass and cleanup.** Submit incident reports with symptom, evidence, cause, recovery, verification and prevention. Show several repairs, an independent restore and a maintenance cycle without weakening security or guessing ownership. Remove the temporary console account only after another route works, accounting for it in retained backups. This demonstrates administration and recovery, not production readiness; chapters 11–19 develop the engineering path.
