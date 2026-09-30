# VM lifecycle, XML and libvirt daemons

**Goal.** Predict which state persists and identify the service responsible. Use linux-01 for observation; use a disposable clone for operations that could lose the guest.

**Read first.**  [virsh](https://libvirt.org/manpages/virsh.html), selected  [domain XML](https://libvirt.org/formatdomain.html),  [domain capabilities](https://libvirt.org/formatdomaincaps.html) and  [daemon architecture](https://libvirt.org/daemons.html).

## 2.1 Inspect the definition

1. Save sudo virsh -c qemu:///system dumpxml linux-01 before changing it. Record its UUID, disk/seed source, NIC model, machine type, firmware, autostart and active/inactive XML. Inspect virsh domcapabilities for x86\_64 KVM and the installed machine/firmware options.

2. Compare saved and active XML with virt-install --print-xml and virsh domxml-to-native output, explaining why native command-line output is an inspection aid rather than a portable replacement definition. Compare the generated profile to the desired **Q35/UEFI/virtio** teaching profile; do not silently assume virt-install chose those defaults.

**Check:** The saved XML identifies UUID, disk, NIC, machine, firmware and active/inactive state.

## 2.2 Probe Q35/UEFI

1. Make a separate profile-probe overlay and unique seed from the verified base. Reuse the 1.4 argument pattern with the probe’s own domain name, disk, seed and serial-log path (for example `/var/log/libvirt/qemu/profile-probe-console.log`); change the guards to those same names. Every new clone also needs its own log. The guard intentionally refuses a reused log so old fingerprints cannot authenticate a new guest. Only if domcapabilities advertises usable UEFI for the selected Q35 type, produce and inspect a --machine q35 --boot uefi virt-install XML preview, then boot the probe. Confirm its actual firmware/NVRAM file and guest boot mode.

2. Preserve the probe’s own writable variable state. If UEFI is unavailable, record the package/capability gap and continue the lifecycle exercises with the existing profile; do not hand-edit guessed firmware paths.

**Check:** The Q35/UEFI probe boots only when advertised by capabilities, with its own NVRAM state. The nested reference run used `pc-q35-noble`, an advertised OVMF image and a distinct writable NVRAM file; `/sys/firmware/efi` existed inside the guest. Record your actual advertised paths.

## 2.3 Predict lifecycle

1. On linux-01, compare define, start, shutdown, destroy, undefine and autostart by making a state-prediction table **before** running the safe ones. Graceful shutdown can take time or fail; destroy is an immediate forced stop and leaves disks.

2. Undefine removes the persistent definition; if the guest is running, it becomes transient and continues until stopped. Managed save, snapshot metadata, NVRAM and TPM may require explicit flags. Test destructive semantics only on the disposable probe, after saving its XML and disk identity.

**Check:** The predicted persistent/live state matches each safe operation observed on the probe.

## 2.4 Observe hotplug

1. Preview a disk or NIC hotplug on the probe; choose explicit --live and/or --config flags as appropriate. Observe live XML and inactive XML, restart the guest and compare. Use virsh event to watch lifecycle notifications. Check installed monolithic or modular daemon/socket names with systemctl rather than assuming one fixed Ubuntu service.

2. Treat virtlogd/virtlockd and socket activation as separate components.

**Check:** Live and persistent XML differ exactly as the selected hotplug flags predict.

## 2.5 Compare connections

1. Diagnose a wrong connection URI: compare qemu:///system and qemu:///session listings. The latter is a distinct per-user connection, with different resource and access behavior. Then, on the disposable probe, test only a documented management-daemon restart and record what happens to its QEMU process.

2. Do not indiscriminately stop every libvirt daemon or use a production host for this.

**Check:** The two URIs list distinct domains and a documented daemon restart leaves the probe understood.

**Pass and cleanup.** Explain which state survives reboot and which is live only, using saved active/inactive XML and events. Remove the probe only after identifying its disk, seed and NVRAM; keep linux-01. Explain why virsh workflows do not map one-to-one to C API calls.
