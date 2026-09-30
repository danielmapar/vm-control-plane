# QEMU, VirtIO and I/O internals

**Goal:** follow a guest I/O request through real QEMU code, change the implementation with a test, and give the chapter-13 VMM a correctly validated virtio-blk device. **Requires:** chapter 13. **Budget:** 12–18 sessions; 14.4 is a multi-session subproject, not an afternoon.

**Read first:** selected  [QEMU developer documentation](https://www.qemu.org/docs/master/devel/index.html), the  [VirtIO ring explanation](https://www.redhat.com/en/blog/virtqueues-and-virtio-ring-how-data-travels), and the transport and block-device chapters of the  [VirtIO specification](https://docs.oasis-open.org/virtio/virtio/v1.3/virtio-v1.3.html) — use the specification version matching what you implement. Treat the older Hajnoczi and Airbus material as maps, reconciled against your pinned source tree.

*Refresher — VirtIO:* the guest driver and device backend exchange requests through queues in shared guest memory. Descriptors identify buffers; notifications tell the other side to look for work. The transport exposes setup and notification registers. Start with the default QEMU userspace device path before comparing vhost offload.

![A guest block request passes through shared virtqueue descriptors to a validated backend, then completes through the used ring and an interrupt.](assets/image15.png)

*Figure 7. Trace one request first. The diagram omits optional batching and notification suppression; the specification defines their rules.*

## 14.1 Build QEMU from pinned source

1. Clone QEMU, check out the release tag matching the lab, and build outside the system installation.

2. Boot a disposable guest on the built binary and confirm it via QMP `query-version`.

3. While navigating, learn the machine/device/backend relationships, QOM, memory regions, the event loop, BQL and IOThreads at map level.

**Check:** locate the virtio-blk device model and its backend in the tree.

## 14.2 Trace one request end to end

1. Enable the virtio-blk trace events on the built QEMU and run a bounded workload in a disposable guest.

2. Produce an annotated path: guest notification → virtqueue processing → backend → completion → interrupt delivery. Add the equivalent design-level path for a network packet, comparing KVM with TCG and the vhost-net, vhost-user and vDPA variants.

3. Run one tiny, deterministic unmanaged guest under TCG and under KVM separately; trace a short TCG translation path.

**Check:** explain one disk request and one network packet end to end, plus which execution mechanism changed between TCG and KVM.

## 14.3 A QMP change with a test

1. Choose a small QMP command or diagnostic change; select the framework from the  [QEMU testing guide](https://www.qemu.org/docs/master/devel/testing/index.html).

2. Deliver the patch plus a test that fails before and passes after.

Keep experiments on unmanaged guests: even read-only QMP investigation must not silently undermine libvirt's ownership of managed configuration ([passthrough security](https://libvirt.org/kbase/qemu-passthrough-security.html)).

## 14.4 Virtio-blk subproject for the tiny VMM

*Scope (the practical minimal feature boundary):* modern virtio-mmio, one split virtqueue, one vCPU, a read-only disposable raw disk. Negotiate `VIRTIO_F_VERSION_1` and the read-only block feature; advertise nothing you have not implemented. PCI transport is comparative. Never attach an image containing data you value. Use the Firecracker and gokvm block-device code paths as reference skeletons; do not transplant unverified fragments.

Work through these checkpoints in order. Each can take several sessions; keep a working version before moving on:

- **(a) Transport.** Implement the register state machine and feature negotiation, with unit tests.

- **(b) Virtqueue parsing.** Implement descriptor parsing as pure functions with validation tests: malformed chains, out-of-range guest addresses, loops, zero-length buffers. Follow the specification's buffer-direction, notification and ordering rules; reject bad input before use.

- **(c) Block requests.** Add request handling and completion/interrupt delivery, checking sector arithmetic, device capacity and each descriptor's direction before touching the file. Test that writes to the read-only device fail.

- **(d) Guest kernel and interrupts.** Build the pinned kernel with VirtIO MMIO, block and root-filesystem support available at boot. Expose the MMIO base and interrupt via the x86 discovery mechanism for this teaching path: `CONFIG_VIRTIO_MMIO_CMDLINE_DEVICES` plus a matching `virtio_mmio.device` parameter ([kernel parameter reference](https://docs.kernel.org/admin-guide/kernel-parameters.html)); record the MMIO range and IRQ on both sides of the manifest. Implement the matching KVM interrupt routing/injection and test guest-visible completion interrupts, including acknowledgement and deassertion where required, before booting a root disk.

- **(e) Boot.** First boot the existing initramfs and read known sectors from the new device; then boot a prepared, compatible read-only root filesystem from it, confirming it contains an executable init and required libraries.

**Control run (do this before debugging your own device against the kernel):** boot the same kernel, initramfs and read-only test disk under the pinned QEMU build's  [microvm machine](https://www.qemu.org/docs/master/system/i386/microvm.html) with virtio-blk-device. Set `virtio-mmio.force-legacy=false` so the transport matches your modern implementation, and record the transport version, actual MMIO address/IRQ and guest command line. Microvm may add its own `virtio_mmio.device` arguments — inspect `/proc/cmdline` and do not append a conflicting mapping copied from your VMM. Require successful device probing and a known-sector read in the control before testing your device. If the control fails, fix its kernel/image/configuration first. If only your VMM fails, compare discovery, feature negotiation, descriptors and interrupts against the control; a working control narrows the fault but does not prove the two machine configurations equivalent.

**Check:** the guest boots from the read-only root on your device, known blocks match their source hashes, malformed descriptors are rejected, and unsupported writes fail cleanly. Writable disks, multiple queues and additional features are later extensions with separate tests.

## 14.5 vhost observation and Firecracker comparison

1. Observe a virtiofsd/vhost-user setup with disposable shared data. A vhost-user backend has a separate protocol contract, including memory sharing and file-descriptor transfer ([vhost-user protocol](https://www.qemu.org/docs/master/interop/vhost-user.html)).

2. Run the same kernel and a small workload under Firecracker; using its design documentation, list the isolation and lifecycle facilities your teaching VMM lacks.

**Check:** a written comparison, not a feature race.

**Failure drill:** introduce one controlled device/backend configuration error and isolate whether the failure is in the guest driver, the QEMU device, the backend or a host resource. **Chapter pass:** the sublab deliverables plus a test detecting the behavior your patch changed. **Recovery:** experimental binaries, guests and images stay disposable and isolated from the managed installation. **Further reading (comparative):** the supplementary qcow2 specification, QMP references, threading/vhost posts, Airbus series, Bellard paper, virtio-networking series and current IOThread/virtqueue-mapping material.
