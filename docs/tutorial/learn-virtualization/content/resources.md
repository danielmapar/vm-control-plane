# Reference library

## Where the LPIC-3 book fits

Use *LPIC-3 Virtualization and Containerization Study Guide* as a readable companion, not the entire curriculum. Its  [publisher table of contents](https://link.springer.com/book/10.1007/979-8-8688-1080-0) supports this chapter-level mapping:

- Book chapters 1–2: companion reading for curriculum chapters 1–2.

- Book chapter 4: libvirt administration alongside curriculum chapter 2, revisited selectively for APIs in chapter 12.

- Book chapter 5: image-management background for curriculum chapters 4–5.

- Book chapter 6: selected OVS material alongside curriculum chapter 3; Proxmox is comparative context, not libvirt practice.

- Book chapters 13–14: Packer/cloud-init background for curriculum chapters 5 and 11; use Windows-specific authorities for the actual unattended Windows procedure.

- Xen, containers and broader cloud topics: a separate adjacent-subject appendix. They are not prerequisites for this KVM/QEMU/libvirt learning sequence.

Use current project/distribution documentation for commands, supported features and source-level work. A chapter title in a book's table of contents is not evidence that it covers every practical requirement here.

## Supplementary resource library

Use this library only after the chapter’s Read first material, or when another explanation helps. The links retain useful resources from the original curriculum; they are not all independently re-audited or required. Older tutorials, talks and books are conceptual supplements: reconcile their commands and code with the recorded lab release. Product-specific material is comparative, not a substitute for the reference lab. Duplicate essential-reading URLs are omitted here.

Additional book/course context: Tanenbaum and Bos, Modern Operating Systems, 5th edition, chapter 7 (foundations); Brendan Gregg, Systems Performance, 2nd edition, chapter 11 (chapter 8); Georgia Tech CS 6210 virtualization lessons (optional operating-systems depth). The LPIC-3 companion mapping is above.

## Foundations — chapters 0–2; revisit CPU detail in 13–15

- [Understanding QEMU devices](https://www.qemu.org/2018/02/09/understanding-qemu-devices/)

- [Intel Virtualisation: how VT-x, KVM and QEMU work together](https://binarydebt.wordpress.com/2018/10/14/intel-virtualisation-how-vt-x-kvm-and-qemu-work-together/)

- [KVM Architecture Overview: 2015 edition (slides)](https://vmsplice.net/~stefan/qemu-kvm-architecture-2015.pdf)

- [QEMU Internals: big picture overview](http://blog.vmsplice.net/2011/03/qemu-internals-big-picture-overview.html)

- [Virtualization with KVM](https://archive.fosdem.org/2012/schedule/event/444/82_fosdem12.pdf)

- [Virtualization with KVM — FOSDEM 2012 video](https://www.youtube.com/watch?v=6n-ANRWqll8)

- [Stanford CS140 Virtual Machines notes](https://web.stanford.edu/~ouster/cgi-bin/cs140-spring19/lecture.php?topic=vmm)

- [OSTEP appendix B](https://pages.cs.wisc.edu/~remzi/OSTEP/vmm-intro.pdf)

## First VMs and administration — chapters 1–2

- [Linux Hypervisor Setup (libvirt/qemu/kvm)](https://joshrosso.com/c/linux-hypervisor-setup/)

- [QEMU](https://wiki.archlinux.org/title/QEMU)

- [libvirt](https://wiki.archlinux.org/title/Libvirt)

- [KVM](https://wiki.archlinux.org/title/KVM)

- [Getting started with QEMU](https://drewdevault.com/blog/Getting-started-with-qemu/)

- [2026 virsh cheat sheet](https://inventivehq.com/blog/virsh-commands-kvm-cheat-sheet)

- [Debian wiki KVM](https://wiki.debian.org/KVM)

- [SUSE Virtualization Guide](https://documentation.suse.com/sles/15-SP6/html/SLES-all/book-virtualization.html)

- [Ubuntu Server virtualisation](https://ubuntu.com/server/docs/how-to/virtualisation/)

- [Virtualization with KVM and Qemu](https://www.linkedin.com/learning/virtualization-with-kvm-and-qemu)

- [LPIC-3 305 objectives (351.3–351.5)](https://www.lpi.org/our-certifications/exam-305-objectives/)

- [On ditching Vagrant: VMs with KVM and virsh](https://benjamintoll.com/2026/06/29/on-ditching-vagrant/)

- [Cockpit Machines](https://github.com/cockpit-project/cockpit-machines)

## Storage, networking and operations — chapters 3–4 and 8–9

- [qcow2 spec](https://www.qemu.org/docs/master/interop/qcow2.html)

- [qemu-img backing files: a poor man's snapshot/rollback](https://dustymabe.com/2015/01/11/qemu-img-backing-files-a-poor-mans-snapshotrollback/)

- [qemu-img cheatsheet](https://blog.programster.org/qemu-img-cheatsheet)

- [libvirt storage pools](https://libvirt.org/storage.html)

- [libvirt Networking Handbook](https://jamielinux.com/docs/libvirt-networking-handbook/)

- [The bridge-netfilter gotcha](https://wiki.libvirt.org/Net.bridge.bridge-nf-call_and_sysctl.conf.html)

- [libvirt + OVS (Red Hat, 2023)](https://www.redhat.com/en/blog/libvirt-open-vswitch)

- [live-migration cheat sheet and tips](https://phip1611.de/blog/live-migration-of-qemu-kvm-vms-with-libvirt-command-cheat-sheet-and-tips/)

- [virtio-win drivers](https://github.com/virtio-win/kvm-guest-drivers-windows)

- [swtpm](https://github.com/stefanberger/swtpm)

- [TPM 2.0 + Windows 11 on KVM](https://computingforgeeks.com/enable-tpm-on-kvm-and-install-windows/)

- [RHEL 7 Virtualization Tuning and Optimization Guide](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/7/html-single/virtualization_tuning_and_optimization_guide/index)

- [kbase: KVM real time](https://libvirt.org/kbase/kvm-realtime.html)

- [Mastering KVM Virtualization 2e](https://www.amazon.com/Mastering-KVM-Virtualization-virtualization-solutions/dp/1838828710)

## Observability — chapters 8 and 17

- [virsh manual — alternate project URL](https://www.libvirt.org/manpages/virsh.html)

- [Go domstats example](https://gitlab.com/libvirt/libvirt-go-module/-/blob/v1.11010.0/examples/domstats.go)

- [Python libvirt event-loop example](https://github.com/libvirt/libvirt-python/blob/master/examples/event-test.py)

- [inovex Prometheus libvirt exporter](https://github.com/inovex/prometheus-libvirt-exporter)

- [Prometheus community exporter discussion](https://github.com/prometheus-community/community/issues/50)

- [QEMU QMP reference](https://www.qemu.org/docs/master/interop/qemu-qmp-ref.html)

- [QEMU guest-agent reference](https://www.qemu.org/docs/master/interop/qemu-ga-ref.html)

- [QEMU tracing guide](https://www.qemu.org/docs/master/devel/tracing.html)

- [RHEL 9 VM performance guide](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html/monitoring_and_managing_system_status_and_performance/optimizing-virtual-machine-performance-in-rhel_monitoring-and-managing-system-status-and-performance)

- [perf-kvm manual](https://man7.org/linux/man-pages/man1/perf-kvm.1.html)

- [KubeVirt metrics reference](https://kubevirt.io/monitoring/metrics.html)

- [KubeVirt guest-memory-pressure runbook](https://kubevirt.io/monitoring/runbooks/KubeVirtVMGuestMemoryPressure.html)

- [Kernel numastat guide](https://cdn.kernel.org/doc/html/latest/admin-guide/numastat.html)

- [Kernel HugeTLB guide](https://docs.kernel.org/admin-guide/mm/hugetlbpage.html)

- [OpenStack Ceilometer measurements](https://docs.openstack.org/ceilometer/latest/admin/telemetry-measurements.html)

- [Proxmox pvestatd implementation](https://github.com/proxmox/pve-manager/blob/master/PVE/Service/pvestatd.pm)

- [Telegraf libvirt input](https://docs.influxdata.com/telegraf/v1/input-plugins/libvirt/)

- [Cockpit project](https://cockpit-project.org/)

## Linux image factories — chapter 5

- [cloud-init: launch with libvirt](https://docs.cloud-init.io/en/latest/howto/launch_libvirt.html)

- [libguestfs project and tools](https://libguestfs.org/)

- [libguestfs mailing-list archive — July 2026](https://lists.libguestfs.org/archives/list/guestfs@lists.libguestfs.org/2026/7/)

- [virt-builder manual](https://libguestfs.org/virt-builder.1.html)

- [guestfish manual](https://libguestfs.org/guestfish.1.html)

- [virt-resize manual](https://libguestfs.org/virt-resize.1.html)

- [virt-sparsify manual](https://libguestfs.org/virt-sparsify.1.html)

- [virt-install manual source](https://github.com/virt-manager/virt-manager/blob/main/man/virt-install.rst)

- [virsh vol-clone reference](https://www.libvirt.org/manpages/virsh.html#vol-clone)

- [cloud-init QEMU tutorial](https://docs.cloud-init.io/en/latest/tutorial/qemu.html)

- [QEMU live block operations](https://www.qemu.org/docs/master/interop/live-block-operations)

- [virt-v2v manual](https://libguestfs.org/virt-v2v.1.html)

- [virt-v2v VMware input guide](https://libguestfs.org/virt-v2v-input-vmware.1.html)

- [OpenStack diskimage-builder](https://docs.openstack.org/diskimage-builder/latest/)

- [systemd mkosi](https://github.com/systemd/mkosi)

- [osbuild on-premises image building](https://osbuild.org/docs/on-premises/overview/)

- [Domain XML: firmware and boot configuration](https://www.libvirt.org/formatdomain.html#operating-system-booting)

## Windows automation — chapter 11

- [KubeVirt startup scripts and Sysprep](https://kubevirt.io/user-guide/user_workloads/startup_scripts/)

- [Cloudbase-Init metadata services](https://cloudbase-init.readthedocs.io/en/master/services.html)

- [Packer unattended Windows setup](https://developer.hashicorp.com/packer/guides/automatic-operating-system-installs/autounattend_windows)

- [libvirt QEMU guest-agent guide](https://wiki.libvirt.org/Qemu_guest_agent.html)

- [RHEL 10 Windows VM guide](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html-single/configuring_and_managing_windows_virtual_machines/)

- [QEMU Hyper-V enlightenments](https://www.qemu.org/docs/master/system/i386/hyperv.html)

- [Cloudbase Windows imaging tools](https://github.com/cloudbase/windows-imaging-tools)

- [OpenStack automated image creation](https://docs.openstack.org/image-guide/create-images-automatically.html)

- [Windows unattended-file generator](https://schneegans.de/windows/unattend-generator/)

- [Proxmox Windows 11 guest practices](https://pve.proxmox.com/wiki/Windows_11_guest_best_practices)

- [Automating Windows installation in a VM](https://palant.info/2023/02/13/automating-windows-installation-in-a-vm/)

- [Microsoft volume activation with VAMT/KMS](https://learn.microsoft.com/en-us/windows/deployment/volume-activation/activate-using-key-management-service-vamt)

## Consoles and graphics — chapters 6, 15 and 18

- [RHEL 9 virtualization changes and support limits](https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/9/html/considerations_in_adopting_rhel_9/assembly_virtualization_considerations-in-adopting-rhel-9)

- [QEMU D-Bus display](https://www.qemu.org/docs/master/interop/dbus-display)

- [qemu-vnc manual](https://www.qemu.org/docs/master/tools/qemu-vnc.html)

- [KubeVirt VM access methods](https://kubevirt.io/user-guide/user_workloads/accessing_virtual_machines/)

- [Cockpit 352 release notes](https://cockpit-project.org/blog/cockpit-352.html)

- [noVNC client source](https://github.com/novnc/noVNC)

- [websockify proxy source](https://github.com/novnc/websockify)

- [QEMU display devices — 2019 explanation](https://www.kraxel.org/blog/2019/09/display-devices-in-qemu/)

- [QEMU virtio-gpu documentation](https://www.qemu.org/docs/master/system/devices/virtio/virtio-gpu.html)

- [Mesa Venus Vulkan driver](https://docs.mesa3d.org/drivers/venus.html)

- [SPICE user manual](https://www.spice-space.org/spice-user-manual.html)

- [SPICE client downloads](https://www.spice-space.org/download.html)

- [virt-viewer source](https://gitlab.com/virt-viewer/virt-viewer)

- [Looking Glass B7 requirements](https://looking-glass.io/docs/B7/requirements/)

- [Looking Glass source](https://github.com/gnif/LookingGlass)

- [Microsoft Remote Desktop access](https://learn.microsoft.com/en-us/windows-server/remote/remote-desktop-services/remotepc/remote-desktop-allow-access)

- [Sunshine remote-desktop streaming](https://github.com/LizardByte/Sunshine)

- [Felipe Borges: 2026 desktop-virtualization posts](https://blogs.gnome.org/feborges/2026/)

## KVM APIs and teaching VMMs — chapter 13

- [KVM host in a few lines of code](https://zserge.com/posts/kvm/)

- [Learning KVM — implement your own kernel](https://david942j.blogspot.com/2018/10/note-learning-kvm-implement-your-own.html)

- [KVM kernel example — source code](https://github.com/david942j/kvm-kernel-example)

- [gokvm](https://github.com/bobuhiro11/gokvm)

- [kvmtool](https://github.com/kvmtool/kvmtool)

- [Wiser](https://github.com/flouthoc/wiser)

- [Writing a KVM hypervisor VMM in Python](https://www.devever.net/~hl/kvm)

- [kvm: the Linux Virtual Machine Monitor](https://www.kernel.org/doc/ols/2007/ols2007v1-pages-225-230.pdf)

- [Firecracker design doc](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md)

- [getting started](https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md)

- [Firecracker internals](https://www.talhoffman.com/2021/07/18/firecracker-internals/)

- [NSDI 2020 paper summary](https://blog.acolyer.org/2020/03/02/firecracker/)

- [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor)

- [the crosvm book](https://crosvm.dev/book/)

- [rust-vmm kvm-ioctls](https://docs.rs/kvm-ioctls/latest/kvm_ioctls/)

## QEMU and VirtIO internals — chapter 14

- [architecture & threading model](http://blog.vmsplice.net/2011/03/qemu-internals-overall-architecture-and.html)

- [vhost architecture](http://blog.vmsplice.net/2011/09/qemu-internals-vhost-architecture.html)

- [code overview slides](https://vmsplice.net/~stefan/qemu-code-overview.pdf)

- [Airbus SecLab QEMU internals series](https://airbus-seclab.github.io/qemu_blog/)

- [memory API](https://www.qemu.org/docs/master/devel/memory.html)

- [QOM](https://www.qemu.org/docs/master/devel/qom.html)

- [translator internals](https://www.qemu.org/docs/master/devel/tcg.html)

- [QMP spec](https://www.qemu.org/docs/master/interop/qmp-spec.html)

- [invocation reference](https://www.qemu.org/docs/master/system/invocation.html)

- [QEMU, a fast and portable dynamic translator](https://www.usenix.org/conference/2005-usenix-annual-technical-conference/qemu-fast-and-portable-dynamic-translator)

- [KVM and QEMU internals: the I/O subsystem](https://www.youtube.com/watch?v=CMnDLHZzGGw)

- [devices and drivers overview](https://www.redhat.com/en/blog/virtio-devices-and-drivers-overview-headjack-and-phone)

- [packed virtqueue](https://www.redhat.com/en/blog/packed-virtqueue-how-reduce-overhead-virtio)

- [Red Hat virtio-networking series](https://www.redhat.com/en/virtio-networking-series)

- [Rusty Russell's 2008 virtio paper](https://research.ibm.com/publications/virtio-towards-a-de-facto-standard-for-virtual-io-devices)

- [vDPA kernel framework](https://www.redhat.com/en/blog/introduction-vdpa-kernel-framework)

## Hardware, MMU and device assignment — chapter 15

- [Introducing the TDP MMU](https://lwn.net/Articles/832835/)

- [KVM planes head for takeoff](https://lwn.net/Articles/1087590/)

- [nested VMX](https://docs.kernel.org/virt/kvm/x86/nested-vmx.html)

- [Liran Alon on nested virtualization (KVM Forum 2017)](https://events19.linuxfoundation.org/wp-content/uploads/2017/12/Improving-KVM-x86-Nested-Virtualization-Liran-Alon-Oracle.pdf)

- [Intel SDM Vol. 3C (VMX)](https://cdrdv2-public.intel.com/671506/326019-sdm-vol-3c.pdf)

- [AMD APM Vol. 2 ch. 15 (SVM)](https://docs.amd.com/v/u/en-US/24593_3.44_APM_Vol2)

- [Popek & Goldberg 1974, summarised](https://blog.acolyer.org/2016/02/19/formal-requirements-for-virtualizable-third-generation-architectures/)

- [UNC COMP 630 Lab 6b](https://www.cs.unc.edu/~porter/courses/comp630/s22/lab6b.html)

- [Hypervisor From Scratch](https://rayanfam.com/tutorials/)

- [Ymir (Zig, bare-metal, boots Linux)](https://hv.smallkirby.com/en/)

- [An EPYC escape: a KVM breakout](https://projectzero.google/2021/06/an-epyc-escape-case-study-of-kvm.html)

- [IOMMU groups, inside and out](http://vfio.blogspot.com/2014/08/iommu-groups-inside-and-out.html)

- [Intro to PCI device assignment with VFIO](http://vfio.blogspot.com/2016/08/kvm-forum-2016-introduction-to-pci.html)

- [VFIO device-assignment talk — video](https://www.youtube.com/watch?v=WFkdTFTOTpA)

- [SR-IOV how-to](https://docs.kernel.org/PCI/pci-iov-howto.html)

- [Gunthorpe's IOMMUFD talk (LPC 2022)](https://www.youtube.com/watch?v=tHNhegCD2tU)

- [Arch Wiki: PCI passthrough via OVMF](https://wiki.archlinux.org/title/PCI_passthrough_via_OVMF)

- [bryansteiner's gpu-passthrough-tutorial](https://github.com/bryansteiner/gpu-passthrough-tutorial)

- [single-gpu-passthrough](https://github.com/joeknock90/single-gpu-passthrough)

- [Level1Techs host-setup guide (Aug 2025)](https://forum.level1techs.com/t/guide-host-setup-for-qemu-kvm-gpu-passthrough-with-vfio-on-linux/235973)

- [L1T beginner's resource](https://forum.level1techs.com/t/the-vfio-and-gpu-passthrough-beginners-resource/129897)

- [r/VFIO](https://www.reddit.com/r/VFIO/)

- [Heiko Sieger's guides](https://www.heiko-sieger.info/12-years-gpu-passthrough/)

- [Passthrough-VM tuning](https://mathiashueber.com/performance-tweaks-gaming-on-virtual-machines/)

## APIs and platform implementations — chapters 12 and 17

- [kbase internals](https://libvirt.org/kbase/index.html)

- [what libvirt offers developers targeting QEMU+KVM](https://www.berrange.com/posts/2011/06/07/what-benefits-does-libvirt-offer-to-developers-targetting-qemukvm/)

- [the embedded QEMU driver mode](https://www.berrange.com/posts/2020/02/05/libvirt-an-embedded-qemu-driver-mode-for-isolated-usage/)

- [CPU model configuration on x86](https://www.berrange.com/posts/2018/06/29/cpu-model-configuration-for-qemu-kvm-on-x86-hosts/)

- [KubeVirt's virt-launcher](https://github.com/kubevirt/kubevirt/blob/main/pkg/virt-launcher/virtwrap/cli/libvirt.go)

- [digitalocean/go-libvirt](https://github.com/digitalocean/go-libvirt)

- [go-qemu](https://github.com/digitalocean/go-qemu)

- [Nova's LibvirtDriver](https://opendev.org/openstack/nova/src/branch/master/nova/virt/libvirt/driver.py)

- [Incus driver\_qemu.go](https://github.com/lxc/incus)

- [Proxmox qemu-server](https://github.com/proxmox/qemu-server)

- [terraform-provider-libvirt](https://github.com/dmacvicar/terraform-provider-libvirt)

- [Ansible community.libvirt](https://github.com/ansible-collections/community.libvirt)

- [virt-lightning](https://github.com/virt-lightning/virt-lightning)

- [project update](https://blogs.ovirt.org/2025/09/ovirt-project-update/)

## Continued learning — chapter 19

- [planet.virt-tools.org](https://planet.virt-tools.org/)

- [LWN](https://lwn.net/Kernel/Index/)

- [KVM Forum archive](https://kvm-forum.qemu.org/archive/)

- [KVM Forum 2024 playlist](https://www.youtube.com/playlist?list=PLW3ep1uCIRfwqKJYHxXsjIvG-Jy3nO2si)

- [KVM Forum 2025 playlist](https://www.youtube.com/playlist?list=PLW3ep1uCIRfxwmllXTOA2txfDWN6vUOHp)

- [FOSDEM's virtualization devroom](https://fosdem.org/2026/schedule/track/virtualization-and-cloud-infrastructure/)

- [qemu-devel](https://lists.nongnu.org/mailman/listinfo/qemu-devel)

- [kvm@vger](https://lore.kernel.org/kvm/)

- [SE Daily with Anthony Liguori](https://softwareengineeringdaily.com/2020/05/15/aws-virtualization-with-anthony-liguori/)

- [Oxide & Friends: Virtualizing Time](https://oxide-and-friends.transistor.fm/episodes/virtualizing-time)

- [Recap video retained from the original curriculum](https://www.youtube.com/watch?v=85k8se4Zo70)
