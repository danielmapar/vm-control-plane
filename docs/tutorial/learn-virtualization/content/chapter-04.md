# Storage and backing chains

**Goal.** Know which file owns guest data and which dependencies a disk requires. Learn guest filesystem versus virtual disk versus host pool; raw versus qcow2; sparse allocation, LVM, overlays, snapshots, flattening, live block jobs, and the effect of cache/discard/flush on integrity and measurements.

**Read first.**  [qemu-img](https://www.qemu.org/docs/master/tools/qemu-img.html),  [libvirt backing chains](https://libvirt.org/kbase/backing_chains.html) and  [virtio-blk versus virtio-scsi](https://www.qemu.org/2021/01/19/virtio-blk-scsi-configuration/).

**Quick model.** A qcow2 overlay stores changed blocks and reads unchanged blocks from its base. A flattened image must boot with that base unavailable.

![A writable overlay reads unchanged blocks from an immutable base; an offline flattened backup has no backing-file dependency.](assets/image13.png)

*Figure 3. Separate the active disk chain from an independent backup you can restore.*

## 4.1 Map the chain

1. Shut down linux-01 and verify it is inactive before inspecting its image with qemu-img info --backing-chain. Draw the base → linux-01 overlay chain and compare virtual size with allocated host size. Record the image’s format explicitly; do not assume every backing file is qcow2.

2. Restart and verify SSH and guest filesystem access before proceeding. Application checks begin after the service is installed in chapter 7.

**Check:** The recorded base/overlay chain and virtual-versus-host sizes match the inactive image.

## 4.2 Add an overlay

1. From the trusted base, create a **second independent** test overlay with qemu-img create -f qcow2 -F qcow2 -b ABSOLUTE\_BASE\_PATH TEST\_OVERLAY. Boot a disposable domain from it with a unique seed. Change one file inside that guest; confirm the base checksum did not change.

2. Never run image-modifying tools concurrently with an active guest disk.

**Check:** The test guest changes while the trusted base checksum remains fixed.

## 4.3 Flatten a clone

1. Power off the test domain. Flatten its overlay into a new independent image with qemu-img convert -O qcow2 TEST\_OVERLAY FLAT\_CLONE, after checking free space. Verify qemu-img info reports no backing dependency.

2. Boot the flat image **only as an isolated recovery test of the same already-booted identity**: keep the original test domain off and disconnect the clone from its normal network, so two copies cannot use the same identity or service data simultaneously. Shut the clone down after checking boot and storage independence.

3. Flattening removes a storage dependency; it does **not** generalize an already booted guest or reset its identity. Chapter 5 teaches generalization for a new clone.

**Check:** The flat image boots without its former base while duplicate identity stays isolated. In the nested run, a networkless flat clone reached its serial login prompt with the original base unavailable.

## 4.4 Grow a test disk

1. Grow only a disposable test disk, then grow the guest partition/filesystem using the method appropriate to its actual layout. Observe host image size and guest filesystem size separately. Use a tiny dedicated test filesystem for a bounded exhaustion drill, never host root. Record cleanup and confirm the host retains reserve.

**Check:** Guest filesystem and host image sizes change as predicted; host reserve is preserved.

## 4.5 Recover a missing base

1. With a disposable overlay shut down, temporarily move its private test base out of the recorded path, observe the missing-backing-file failure, restore the exact base and path, and verify boot. Never move the base shared by linux-01/linux-02. Then, on disposable disks, perform one documented external snapshot plus blockcommit and one blockpull.

2. Save before/after chain diagrams and use virsh’s live block-job observations. If a job fails, stop and inspect ownership/chain state before retrying. Compare a directory pool and a small LVM pool conceptually or in an isolated lab; do not repurpose an existing host volume group.

**Check:** The private missing-base failure is repaired and block-job chain diagrams match observation.

**Pass and cleanup.** Name each authoritative file, boot a flat image without its former base, and explain raw/qcow2 and virtio-blk/virtio-scsi choices. Restore linux-01’s chain. Inspect active disks through managed live tools or consistent offline copies; qemu-img can misread a changing image. Snapshots are not backups.
