# Windows guests and unattended provisioning

**Goal.** Build two independently initialized Windows guests, then restore one as the same VM.

**Requires.** Chapter 10’s single-host checks; two-host checks may remain outstanding under Setup 2. Bring official media and the right to use a supported x64 Windows release, adequate RAM/disk, a private graphical recovery route, UEFI, matching virtio drivers and TPM support where required. Record edition/build, media checksum, virtio-win build, firmware and swtpm versions. Do not bypass OS hardware requirements.

**Read first.** Microsoft’s  [Setup automation](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/automate-windows-setup),  [Sysprep generalization](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/sysprep--generalize--a-windows-installation), and  [Cloudbase-Init](https://cloudbase-init.readthedocs.io/en/stable/). Use  [libvirt Secure Boot](https://www.libvirt.org/kbase/secureboot.html),  [virtio-win packaging](https://github.com/virtio-win/virtio-win-pkg-scripts) and the  [Packer QEMU builder](https://developer.hashicorp.com/packer/integrations/hashicorp/qemu/latest/components/builder/qemu) when their sublabs call for them.

**Route.** Understand one manual install, automate that same install, generalize a clean template, then restore one VM with its identity and recovery state.

## 11.1 Build once by hand

1. Create disposable windows-build-01 with Q35, UEFI, a fresh writable variable store, the selected TPM model/version and a virtual disk. Attach official installer and trusted virtio driver media. If Setup cannot see the virtio disk, load the storage driver matching its controller and Windows architecture. Install through the private graphical console.

2. Record edition, partition, drivers, network and account inputs; save XML and a device inventory.

**Check:** disk and NIC drivers work, Windows boots from the installed disk, and the recovery console works with its NIC disconnected.

## 11.2 Check security and recovery state

1. In elevated PowerShell, record Confirm-SecureBootUEFI and Get-Tpm where applicable; observe state rather than inferring it from a firmware filename. Install and test the guest agent, treating agent execution as privileged access. Inventory disk, XML, NVRAM, TPM and any encryption/recovery material; keep secrets outside the public notebook.

2. Do not reset NVRAM/TPM on an encrypted guest without a tested recovery route.

**Check:** intended security state is visible inside the guest and the recovery inventory is complete.

## 11.3 Automate the known install

1. Validate an answer file against the chosen Windows image with Microsoft’s tooling, including exact edition, UEFI disk layout and driver paths. Prove it on a new empty disk before introducing Packer.

2. Pin Packer and its QEMU plugin; use a trusted checksum, run packer init and packer validate, and build with the same answer file and driver media. Keep temporary credentials private and removable.

3. Compare the builder’s firmware, CPU, storage and network with the working manual build; a generic QEMU example is not a Windows template.

**Check:** two clean installer runs complete without manual repair and produce the required device inventory.

## 11.4 Generalize, then clone

1. Configure Cloudbase-Init for the metadata format you actually provide; NoCloud and ConfigDrive are distinct. Test discovery before capture. Run supported Sysprep generalization, shut down, and capture the successful powered-off image without rebooting its source. Follow Microsoft’s restrictions for VM-specific Sysprep options.

2. Deploy windows-01 and a second disposable clone with separate VM identity, writable NVRAM, TPM state and metadata. Check hostnames/guest identities, access policy, initialization logs and monitoring. Keep activation material in a private procedure, never a reusable public template.

**Check:** both clones initialize independently.

## 11.5 Break and restore

1. On a disposable build, omit the required storage driver, diagnose the missing disk, restore the driver and rebuild. Back up windows-01 with all inventoried state and recovery material. Make the original unavailable; restore to an isolated target and verify data, firmware/TPM and encrypted-volume access if used.

2. A same-VM restore keeps its identity; a new clone does not. Linux-style serial alone does not provide Windows recovery; EMS/SAC needs its own setup.

**Check:** both the repaired build and isolated restore pass their original checks.

**Pass and cleanup.** Keep the build manifest, private provisioning procedure, unattended logs, two-clone identity/security/device checks and same-VM restore report. A Packer exit code or login screen alone is insufficient. Remove only named disposable builds after confirming which disk, NVRAM and TPM state belongs to each build, template, clone or restored VM. Keep the known-good image and protected recovery set. Hyper-V enlightenments and extra image-factory tools are optional comparisons.
