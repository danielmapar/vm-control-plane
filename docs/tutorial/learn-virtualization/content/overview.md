# Before your first VM

## Who this guide is for

This guide assumes you can use a Linux shell, SSH, Git and a package manager. Use **Refresh if needed** notes for specific gaps, then return to the lab. Use the chapter navigation and the page outline to follow its numbered substeps.

You will progress from a first VM to operating and recovering Linux and Windows guests, automating libvirt, building a teaching VMM, and engineering a multi-host platform. Early labs provide worked commands; later projects ask you to implement and test defined behavior.

**Start now:** complete chapter 0, run Appendix B’s preflight, then create linux-01 in chapter 1. Read the references needed for your current step; the supplementary library is optional. The **Validation status** section records actual executions and remaining gaps; the full course has not been run end to end.

## Four names, four jobs

| **Component** | **What you use it for** |
| --- | --- |
| **virsh** | The command-line client you use to inspect and control libvirt-managed VMs. |
| **libvirt** | The API, drivers and services that define and manage VMs, networks and storage. |
| **QEMU** | The userspace VM process: machine model, devices and the chosen execution accelerator. |
| **Linux KVM** | The kernel virtualization interface QEMU uses to run guest CPU work with hardware assistance. |

A **domain** is libvirt’s name for a VM; **host** is the machine running it; **guest** is the operating system inside it. A **VMM** is a virtual machine monitor, such as QEMU or the small one you will write later. The main path in this guide uses KVM acceleration; TCG software emulation is a later comparison.

![Stack diagram: virsh and applications call libvirt, which manages QEMU; QEMU uses KVM for guest execution and device backends for I/O.](assets/image4.png)

*Figure 1. Follow a management request downward, then distinguish the CPU path from the I/O path. Revisit this diagram whenever a failure appears to belong to “the hypervisor.”*

## Start here: prepare once

## Setup 1. Use one reference environment

Use a dedicated **x86-64 Ubuntu 24.04 LTS host**, Ubuntu 24.04 cloud guests, and distribution packages for the first pass. This is a supported teaching baseline, not a claim about the newest release. See the  [Ubuntu support matrix](https://ubuntu.com/about/release-cycle).

Use **qemu:///system** for managed VMs. Chapter 2 compares the separate per-user connection. Keep experimental QEMU/kernel builds outside the installed system packages. See the  [libvirt connection model](https://libvirt.org/drvqemu.html).

Record host identity, CPU/RAM, free storage, kernel, interfaces/default route and recovery access before setup. Add installed tool versions, image hashes and source commits as you use them. One inventory file is enough:

```yaml
# Name the physical or nested host this manifest describes.
host: host-a
# The instruction set assumed by this course.
architecture: x86_64
# Keep the reference distribution consistent across the early labs.
distribution: Ubuntu 24.04 LTS
# Use the system instance for the managed course VMs.
connection: qemu:///system
# Record the running kernel, not just an installed package.
kernel: RECORD_ACTUAL_VERSION
# Record the QEMU binary version used by libvirt.
qemu: RECORD_ACTUAL_VERSION
# Record the libvirt client and daemon versions.
libvirt: RECORD_ACTUAL_VERSION
# Record both the firmware package and the image selected for this VM.
firmware: RECORD_PACKAGE_AND_SELECTED_IMAGE
# Pin the source image build and its verified digest.
guest_image: RECORD_SOURCE_BUILD_AND_SHA256
```

Replace the RECORD values with observations. Later add the selected machine/CPU model, storage/network settings and new dependencies. Keep credentials and private keys outside Git; sanitize logs before sharing.

## Setup 2. Arrange hardware when you need it

These are planning budgets, not vendor minimums. Leave resources for the host and backups.

| **Stage** | **Arrange before starting** | **What this lets you prove** |
| --- | --- | --- |
| Chapters 0–8 | One dedicated virtualization-capable x86-64 host. Start with roughly 32 GiB RAM and 100 GiB free space for Linux labs; independent backups need more. Keep local or out-of-band recovery access. | A working KVM guest and recoverable service. CPU flags alone do not prove acceleration. |
| Chapters 9–10 | A second compatible host, private management/migration networking and destination capacity. | Real two-host migration and recovery with the original host unavailable. |
| Chapter 11 | Windows media and resources for the chosen release; suitable firmware/TPM support. This can use one host. | Reproducible Windows provisioning and recovery. |
| Chapters 12–15 | Space for builds and test artifacts; an eligible spare device and suitable IOMMU/reset behavior for VFIO. | API/device work; physical DMA isolation only when tested on suitable hardware. |
| Chapters 16–19 | The selected storage/HA topology, quorum participants, independent fencing, recovery capacity and a reviewer. | Multi-host ownership and recovery under the failures actually tested. |

**No host-b yet? (The continuation rule.)** Finish chapters 0–8 and chapter 10’s single-host diagnosis/recovery work, mark the two-host tasks outstanding, then continue through chapters 11–15. Return to the missing tasks later. A nested host-b can teach migration mechanics; it does not prove survival of its shared physical host’s failure. Missing VFIO or HA hardware likewise leaves the relevant practical outstanding; do the stated design exercise meanwhile.

Use disposable lab resources for faults. Identify the boot disk, sole management NIC and only display device before changing anything. Failure injection must not endanger the machine or service you depend on.

## Setup 3. Reuse the lab and keep brief notes

Start with **host-a / linux-01**. Add **linux-02** in chapter 5, **host-b** in chapter 9, and **windows-01** in chapter 11. The small SQLite service in Appendix A becomes the common workload in chapter 7. Use separate disposable guests for destructive tests.

Keep one repository: an inventory directory, a directory per chapter, and recovery notes. In each chapter README, record the starting state, exact commands or commit, expected versus observed result, and cleanup. Save supporting output only when it explains or proves a result. Mark the chapter **not started**, **in progress**, **passed**, or **outstanding: reason**. Disk images and secrets stay outside Git.

## Setup 4. Work through one checkpoint at a time

1. Read the goal and any unfamiliar refresher.

2. Build the smallest example using the stated defaults.

3. Check the expected result before adding complexity.

4. Change or break one thing, diagnose it, and restore the known working state.

5. Save the useful commands and observations, then continue.

Replace marked example values before running commands. When a check fails, compare expected and actual output, check versions/permissions and the relevant logs, then change one variable. Fix the cause; do not disable host protections to force a pass.

## Setup 5. Set the pace from your own work

A numbered lab may take several sessions. Use chapters 0–2 to estimate your pace. A device implementation or control plane is a project, not a single sitting. Skip a refresher once you can pass its check; do not skip the result you must demonstrate. Allow separate time for downloads, hardware and independent review.

## Milestones and assessment

| **After chapter** | **Demonstrate** | **Keep as evidence** |
| --- | --- | --- |
| 4 | Create, inspect and repair a Linux VM and its network/storage dependencies. | Rebuild notes, packet path, backing-chain diagram and recovery result. |
| 10 | Operate and recover a service across two hosts. | Verified restore, alert/recovery, maintenance and incident results. |
| 15 | Automate the stack and explain or modify an API/device/hardware path. | Tested libvirt tool, teaching VMM, detecting regression test and available hardware evidence. |
| 19 | Reproduce and defend the complete platform. | Version matrix, deployment/recovery instructions, failure results, source work and independent review. |

For chapter 10, a helper can choose a fault without revealing it. On your own, prepare reversible faults with separate answer files, shuffle them and wait before diagnosing one; open its answer only after writing your diagnosis. Label that result self-assessed.

For chapter 19, first rebuild using only your repository and backup set. Then have a peer deploy, inject a fault and recover independently. Without a reviewer, record independent review as outstanding. Full completion includes the missing hardware practicals and this independent assessment.

## Use the right reference

Project documentation explains the API; the installed distribution determines available packages and features. Use QEMU master and Ceph latest pages for orientation, then the matching release documentation for commands. Keep Ubuntu, Arch and RHEL recipes separate. A later lab that follows a different distribution’s HA guide needs its own matching environment.

Old books, videos and tutorials can explain concepts without being safe command recipes. Read the chapter’s starting material first; consult the retained supplementary library when another explanation helps.

Throughout these chapters, abbreviated virsh commands mean `sudo virsh -c qemu:///system COMMAND`; use the full form in saved scripts. Commands labeled inside a guest run there, not on the host. The starting linux-01 budget is 2 vCPUs, 2 GiB RAM and a 20 GiB virtual disk, subject to host reserve.
