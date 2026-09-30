# Validation & maintenance

This revision was reviewed collaboratively and checked against selected primary sources. The supplementary books, videos and every linked page have not all been audited. The full VM, migration, HA and VMM sequence has not been run end to end.

**Executed checks in this revision:**

- **Reference first-VM path:** nested Ubuntu 24.04.5, kernel 6.8.0-139-generic, QEMU 8.2.2, libvirt 10.0.0 and virt-install 4.1.0. The signed image, seed, overlay, XML preview, KVM launch, lease, cloud-init, SSH, disk expansion, corrected preflight and wrong-client-key rejection passed.

- **Course service:** installed inside that guest under the documented systemd unit; health, acknowledged records, SQLite integrity and persistence across service restart passed. Separate loopback tests also covered literal SQL-like input and error responses.

- **Additional nested checks:** a Q35/UEFI probe booted with its own writable NVRAM; persistent/autostart and start/destroy/undefine behavior matched expectations. Two fresh guests sharing the verified immutable base had distinct hostnames, machine IDs and ED25519 host keys. This checks fresh-image identity, not the complete customized-template pipeline.

- **Offline restore:** with the original domain undefined and its overlay and base made unavailable, an independent flattened linux-01 backup booted, recovered A–C and accepted D. The restored copy was then stopped; the original chain was restored and still contained only A–C. A separate networkless flat clone also booted without the base. This is a nested, same-host restore test; it does not prove recovery from loss of the physical host or backup medium.

- **Monitoring baseline:** Noble’s packaged exporter ran on `127.0.0.1:9177` using the read-only libvirt socket after the package-added management group membership was removed. The revised bounded smoke check passed, and domain identity/units/counters agreed with nearby `virsh domstats` observations. The chapter-8 starter Prometheus configuration and all three alert rules passed promtool validation; stopping the collector fired LibvirtCollectorDown after 70 seconds, and restarting it restored a healthy scrape and cleared the alerts. Prometheus listened only on 127.0.0.1:9090. The remaining performance drills were not executed.

- **KVM reference program:** the pinned Fedora build and the successful real/protected/32-bit paging modes are recorded in 13.2; its original long-mode failure and separately tested CPUID repair are recorded there.

**Not covered by those runs:** the separate raw-QEMU exercise, the remaining networking/image-pipeline/performance drills, physical network changes, two-host migration, independent-host disaster recovery, VFIO, HA/fencing, the complete custom VMM/control plane and independent capstone assessment. The first C1 run used trust on first use. A separate fresh nested probe then passed the serial-log fingerprint comparison and strict SSH path in chapter 1.5. These are specific limits on the evidence, not results to mark passed.

Use the existing chapter README for four facts: **last execution**, **manifest commit**, **observed result**, and **deviations/resolution**. Start each unrun chapter at "not executed." A procedure is validated after a clean run of its steps and failure/recovery check on that recorded environment—not from a reviewer's score.

On the first run, check the installed packages/keyring, local virt-install options, selected firmware and writable variable state, image/seed initialization, exporter compatibility, and actual guest/application recovery. Keep Appendix B's output. A second reader should be able to reproduce the early labs and programming bridges without undocumented help; fix any step that needs it.

After changing the host, kernel, QEMU, libvirt, firmware or guest image, rerun the affected exercises and the representative smoke checks. Keep hardware-dependent work and independent assessment outstanding until performed. Supported, tested and still outstanding are different states.
