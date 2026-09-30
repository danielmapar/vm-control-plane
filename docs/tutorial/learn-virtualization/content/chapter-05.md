# Repeatable Linux images and provisioning

**Goal.** Produce linux-02 and another clean instance from a documented image pipeline, while keeping linux-01 as the service guest.

**Read first.**  [NoCloud](https://docs.cloud-init.io/en/latest/reference/datasources/nocloud.html),  [virt-customize](https://libguestfs.org/virt-customize.1.html),  [virt-sysprep](https://libguestfs.org/virt-sysprep.1.html) and  [cloud-init first-boot determination](https://docs.cloud-init.io/en/latest/explanation/first_boot.html).

**Quick model.** A disk clone carries cached cloud-init state, machine ID and SSH host keys unless generalized. A new seed alone does not prove a new identity.

![An overlay needs its base; a flattened clone is independent but keeps its captured identity; a generalized template prepares distinct new guest identities.](assets/image2.png)

*Figure 4. Check storage independence and guest identity separately. A generalized template can itself be the base for new writable overlays.*

## 5.1 Build a manifest

1. On the Ubuntu host, install the image tools first: sudo apt install libguestfs-tools. Confirm command -v virt-customize and command -v virt-sysprep succeed, and record their versions. Create an image manifest from chapter 1: download URL, verified signature/key fingerprint, SHA-256, Ubuntu image build, packages, host tool versions, partition layout and chosen guest profile.

2. Copy the pristine image into a **working build copy**; leave the verified source untouched. Use virt-customize on the offline copy for one small declared change, such as an agreed package. Record repository snapshot/pinning if claiming reproducible package state; the base checksum alone does not freeze later apt results.

**Check:** The working copy and immutable source have distinct manifests and expected digests.

## 5.2 Generalize the template

1. Prepare a publishable template offline. For a booted build image, use the documented cleanup sequence: run `cloud-init clean` with the appropriate options inside the disposable build guest immediately before its final shutdown, then use selected `virt-sysprep` operations only on the shut-down image or its offline copy.

2. Inspect what is removed and retain a backup; do not boot the cleaned template again before cloning it. Publish a new checksum, manifest and provenance. Avoid embedding SSH host keys, machine ID, credentials or instance-specific seeds. Do not sysprep a running guest or the authoritative linux-01 service disk.

**Check:** The published template has no intended per-instance identity and is not rebooted before cloning.

## 5.3 Provision two guests

1. Create linux-02 and a second test instance from separate writable overlays of the published immutable base, each with a distinct instance-id, hostname and NoCloud seed. Give each its own MAC, disk path and, for UEFI, writable NVRAM. Boot one at a time first, then together.

2. Check cloud-init status, SSH host-key fingerprints, /etc/machine-id, hostname and DHCP leases. Expected: base checksum unchanged and identities differ.

**Check:** Two guests boot with distinct instance IDs, machine IDs, host keys and leases; base digest is fixed. This identity check passed for two fresh Q35 guests in the nested run; a customized, previously booted template still needs its own cleanup test.

## 5.4 Reproduce identity trouble

1. Power off a third disposable instance and flatten its overlay; provision it as a **new** machine only after generalizing the captured image and supplying new seed/identity. Compare this with the already generalized template path.

2. A cached cloud-init instance ID can cause per-instance setup to be skipped: demonstrate the failure by capturing a previously booted disposable image without cleanup, then repair both image cache and new seed identity. Two pristine images with the same ID alone may not reproduce it.

**Check:** The stale-cache failure is reproduced on a disposable copy and corrected by generalization plus new seed.

## 5.5 Rebuild from notes

1. Recreate linux-02 from the manifest on a clean lab target without copying unexplained commands. Test access policy and checksum/identity assertions. Keep experimental tools such as virt-builder, Packer, mkosi, osbuild, diskimage-builder, virt-v2v and guestfish as targeted comparisons after this one pipeline works; Windows Packer usage follows in chapter 11.

**Check:** Another clean target recreates linux-02 from the manifest and passes identity/access checks.

**Pass and cleanup.** Another learner can recreate guests with distinct machine IDs and SSH host keys while the source checksum stays fixed. Explain overlay, flattened clone and generalized template. Remove only named disposable clones after checking paths; keep linux-02 and the manifest.
