## Where the LPIC-3 book fits

Antonio Vazquez's *LPIC-3 Virtualization and Containerization Study Guide* (Apress, 2024) prepares readers for the LPI exam 305. Use it as a readable companion, not as the curriculum. The table below matches the book's chapter titles, taken from the [publisher's table of contents](https://link.springer.com/book/10.1007/979-8-8688-1080-0), to this course. Almost every chapter is named after an objective in the [LPI exam 305 objectives](https://www.lpi.org/our-certifications/exam-305-objectives/) (version 3.0). The mapping is based on those titles and objectives, not on a reading of the full book.

| Book chapter | Read it with | Why |
|---|---|---|
| 1. Virtualization Concepts and Theory | Chapter 1; its migration and snapshot parts with chapters 8 and 4 | Hypervisor types, terms and the ideas behind migration and snapshots. |
| 2. QEMU | Chapters 1, 2 and 12; the guest agent with 6.5 | The QEMU process, its command line, monitor, VirtIO devices and guest agent. libvirt starts this same QEMU for you. |
| 4. libvirt Virtual Machine Management | Chapter 2; read the book chapter's network, storage-pool and migration parts with chapters 3, 4 and 8 | Objective 351.4 covers virsh, domain XML, virtual networks, storage pools and migration. |
| 5. Virtual Machine Disk Image Management | Chapters 4–5 | qemu-img, guestfish and the other libguestfs tools. |
| 6. Proxmox and Open vSwitch | Chapter 3 | Background only: Open vSwitch is another kind of bridge that libvirt can attach guests to. Proxmox is a comparison: it drives QEMU through its own [qemu-server](https://github.com/proxmox/qemu-server), not through libvirt. |
| 14. cloud-init | Chapters 1 and 5 | The cloud-init seed you write in chapter 1 and the repeatable images of chapter 5. |

The other chapters are not mapped: 3 (Xen), 7–11 (container concepts, LXC, Docker, orchestration, podman), 12 (cloud management tools), 13 (Packer) and 15 (Vagrant). They are on the exam but not in this course.

Two cautions:

- The exam objectives still name the single `libvirtd` daemon, `brctl` and `tunctl`. Fedora 44 runs libvirt as separate daemons (`virtqemud`, `virtnetworkd`, `virtstoraged` and others), and chapter 3 builds its bridge with `ip`. When the book's commands differ, follow the chapter text, which states the versions it was validated with.
- A chapter title does not prove that the chapter covers what a lab here needs. For commands and features, use the project and Fedora documentation.

## Supplementary resource library

This optional library gives a few entries per chapter for a second explanation or more depth. Read a chapter's own links first; they are not repeated here.

- **Most entries explain concepts.** Older posts and talks show commands for older releases. Before copying one, compare it with your versions (`rpm -q qemu-kvm libvirt-daemon virt-install`) and the installed manual pages; this course was validated with QEMU 10.2.2, libvirt 12.0.0 and virt-install 5.1.0.
- **QEMU "master" pages describe the next QEMU release.** A feature documented there may not exist in Fedora 44's QEMU 10.2.2.
- **Red Hat Enterprise Linux 10 guides** are the closest enterprise relative of Fedora's stack; their support limits are Red Hat policy, not Fedora restrictions.
- **Every link was checked on 2026-10-02.** A working link only means the page exists; it is not a review of everything the page says.

Two books for background: Tanenbaum and Bos, *Modern Operating Systems*, 5th edition, chapter 7 "Virtualization and the Cloud" ([contents](https://www.pearson.com/en-us/subject-catalog/p/modern-operating-systems/P200000003295/9780137618880)), and Brendan Gregg, *Systems Performance*, 2nd edition, chapter 11 "Cloud Computing" ([book page](https://www.brendangregg.com/systems-performance-2nd-edition-book.html)).

### Chapters 0–2: a safe lab, the first VM, its lifecycle and libvirt daemons

- [RHEL 10: Configuring and managing Linux virtual machines](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/index): a task-by-task guide to the same libvirt, QEMU and virt-install tools.
- Fedora package pages for [libvirt](https://packages.fedoraproject.org/pkgs/libvirt/libvirt/), [qemu-kvm](https://packages.fedoraproject.org/pkgs/qemu/qemu-kvm/) and [virt-install](https://packages.fedoraproject.org/pkgs/virt-manager/virt-install/): the version each Fedora release ships.
- [KVM Architecture Overview, 2015 edition](https://vmsplice.net/~stefan/qemu-kvm-architecture-2015.pdf) (Stefan Hajnoczi, slides): how the kvm module, the QEMU process and libvirt fit together.
- [Understanding QEMU devices](https://www.qemu.org/2018/02/09/understanding-qemu-devices/) (qemu.org, 2018): what an emulated device is.
- [Fedora Quick Docs: nested virtualization in KVM](https://docs.fedoraproject.org/en-US/quick-docs/using-nested-virtualization-in-kvm/): how to check `/sys/module/kvm_intel/parameters/nested` (chapter 0.2; it printed `Y` on the validation host). Reloading the module, as it describes, needs every VM stopped.

### Chapter 3: networking

- [libvirt: Firewall and network filtering](https://libvirt.org/firewall.html): the rules libvirt's virtual networks add, and why their bridges join the firewalld zone `libvirt`.
- [RHEL 10: Configuring virtual machine network connections](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/configuring-virtual-machine-network-connections): NAT, bridged and isolated networks with `virsh` and `nmcli`.

### Chapter 4: storage and backing chains

- [qcow2 image format](https://www.qemu.org/docs/master/interop/qcow2.html): the on-disk format, including backing files, snapshots and clusters.
- [libvirt: Storage management](https://libvirt.org/storage.html): every pool and volume type `virsh` manages.
- [QEMU: Live block device operations](https://www.qemu.org/docs/master/interop/live-block-operations): the block jobs behind `virsh blockpull`, `blockcommit` and `backup-begin`. Also useful for chapter 7.

### Chapter 5: repeatable Linux images

- [Fedora Cloud downloads](https://fedoraproject.org/cloud/download/): every Fedora 44 Cloud image variant and its signed CHECKSUM file.
- [cloud-init: Run cloud-init locally with libvirt](https://docs.cloud-init.io/en/latest/howto/launch_libvirt.html): virt-install's `--cloud-init` option, which builds the seed for you. Its example uses an Ubuntu image and password logins; use the Fedora image, `--osinfo fedora43` and your course key instead, as in chapter 1.
- [libguestfs](https://libguestfs.org/): the manual of every libguestfs tool, including chapter 5's virt-customize and virt-sysprep (Fedora 44 ships them in guestfs-tools 1.56.0).

### Chapter 6: security boundaries and recovery access

- [Fedora Quick Docs: Getting started with SELinux](https://docs.fedoraproject.org/en-US/quick-docs/selinux-getting-started/): labels, modes and denials on Fedora. Background for sVirt.
- [RHEL 10: Securing virtual machines](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/securing-virtual-machines): sVirt, SELinux booleans and Secure Boot for guests.

### Chapter 7: backups and restore

- [RHEL 10: Backing up and recovering virtual machines](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/backing-up-and-recovering-virtual-machines): saving the XML and disks with `virsh backup-begin`, then rebuilding the VM from them.

### Chapter 8: migration and host maintenance

- [RHEL 10: Migrating virtual machines](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/migrating-virtual-machines): requirements and steps for live and offline migration.
- [CPU model configuration for QEMU/KVM on x86 hosts](https://web.archive.org/web/20260818231623/https://www.berrange.com/posts/2018/06/29/cpu-model-configuration-for-qemu-kvm-on-x86-hosts/) (Daniel Berrangé, 2018, archived): host-passthrough, host-model and named CPU models, and what each means for migration.

### Chapter 9: incident response

- [RHEL 10: Diagnosing virtual machine problems](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/diagnosing-virtual-machine-problems): libvirt debug logs, VM core dumps and backtraces of VM processes.

### Chapter 10: the libvirt API

- [What benefits does libvirt offer to developers targeting QEMU+KVM?](https://web.archive.org/web/20260311034241/https://www.berrange.com/posts/2011/06/07/what-benefits-does-libvirt-offer-to-developers-targetting-qemukvm/) (Daniel Berrangé, 2011, archived): why a stable management API sits between your code and QEMU.
- [libvirt API: domain functions](https://libvirt.org/html/libvirt-libvirt-domain.html): the C reference for every domain call and its flags; most methods in chapter 10 wrap one of these.
- [libvirt API: event functions](https://libvirt.org/html/libvirt-libvirt-event.html): the C reference behind `virEventRegisterDefaultImpl`, `virEventRunDefaultImpl` and `virEventAddTimeout`.

### Chapter 11: the KVM API and a tiny VMM

- [KVM host in a few lines of code](https://zserge.com/posts/kvm/): a tiny KVM host in C, short enough to read in one sitting.
- [kvmtool](https://github.com/kvmtool/kvmtool): a small, real VMM that boots Linux.
- [Firecracker: Lightweight Virtualization for Serverless Applications](https://www.usenix.org/conference/nsdi20/presentation/agache) (NSDI 2020): why a production VMM keeps its device model minimal.

### Chapter 12: QEMU, VirtIO and the I/O path

- [QEMU QMP reference](https://www.qemu.org/docs/master/interop/qemu-qmp-ref.html): every QMP command and event, such as 12.2's `query-cpus-fast`.
- [QEMU internals: overall architecture and threading model](http://blog.vmsplice.net/2011/03/qemu-internals-overall-architecture-and.html) (Stefan Hajnoczi, 2011): the event loop, vCPU threads and the global lock; old, but the picture still applies.
- [QEMU internals: vhost architecture](http://blog.vmsplice.net/2011/09/qemu-internals-vhost-architecture.html) (2011): how vhost-net moves the virtio data path into the kernel.
- [Virtio devices and drivers overview](https://www.redhat.com/en/blog/virtio-devices-and-drivers-overview-headjack-and-phone) (Red Hat): which side does what in virtio.
- [RHEL 10: Optimizing virtual machine performance](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/configuring_and_managing_linux_virtual_machines/optimizing-virtual-machine-performance): how to tune vCPUs, memory, disk and network.

### Chapter 13: hardware virtualization, the KVM MMU and VFIO

- [perf-kvm(1)](https://man7.org/linux/man-pages/man1/perf-kvm.1.html): count and trace VM exits from the host, as in 13.2.
- [Kernel: nested VMX](https://docs.kernel.org/virt/kvm/x86/nested-vmx.html): how KVM runs a hypervisor inside a guest on Intel CPUs.
- [Intel SDM manuals](https://www.intel.com/content/www/us/en/developer/articles/technical/intel-sdm.html): the current Intel 64 and IA-32 manuals; volume 3 covers VMX.
- [AMD64 Architecture Programmer's Manual, volume 2](https://docs.amd.com/v/u/en-US/24593_3.45_APM_Vol2_PUB) (revision 3.45): system programming, including the Secure Virtual Machine (SVM) chapter.
- [IOMMU groups, inside and out](http://vfio.blogspot.com/2014/08/iommu-groups-inside-and-out.html) (Alex Williamson, 2014): why IOMMU groups bound DMA isolation and VFIO ownership.
- [Kernel: HugeTLB pages](https://docs.kernel.org/admin-guide/mm/hugetlbpage.html): how huge pages are reserved and accounted for.

### Chapter 14: final integration and continued expertise

- [planet.virt-tools.org](https://planet.virt-tools.org/): blog posts from libvirt, QEMU and KVM developers in one feed.
- [KVM Forum archive](https://kvm-forum.qemu.org/archive/): talks from past KVM Forums.
- [qemu-devel archive](https://lore.kernel.org/qemu-devel/) and [kvm list archive](https://lore.kernel.org/kvm/): the QEMU and KVM development lists, searchable.
