# /dev/kvm: from your first VM to virtualization engineering

## Who this guide is for

You can already use a Linux shell, SSH and a package manager. This course teaches Linux KVM, QEMU, libvirt and virsh on Fedora 44. You start with your first VM, then learn to operate, recover and automate guests, and finally look underneath: the KVM API, QEMU's devices and the CPU's virtualization support. Every lab gives the commands and the output to expect; a few ask you to predict a result or write a check first.

**Start now:** do chapter 0, run Appendix A's host preflight, then create linux-01 in chapter 1. Read the references each step names; the library at the end is optional.

### Four names, four jobs

| Component | Its job |
|---|---|
| **virsh** | The command-line client used to inspect and control libvirt-managed VMs |
| **libvirt** | The API, drivers and services that manage VM definitions, networks and storage |
| **QEMU** | The userspace VM process: machine model, devices and selected CPU accelerator |
| **Linux KVM** | The kernel interface QEMU uses for hardware-assisted guest CPU execution |

A **domain** is libvirt's name for a VM. The **host** runs it; the **guest** is the operating system inside it. A **VMM** (virtual machine monitor) is the program that runs a VM, such as QEMU or the tiny one you write in chapter 11. The course always uses KVM, never TCG, QEMU's slower software emulation. The saved VM definition (**persistent XML**), the running process and the disk data are different things: deleting one does not delete the others.

*Figure 1. Follow a management request downward, then distinguish the CPU path from the I/O path. Revisit this diagram whenever a failure appears to belong to “the hypervisor.”*

## Start here: prepare once

### Setup 1. Use one reference environment

Use **Fedora 44 Workstation or Server on x86-64** with Intel VT-x or AMD-V, SELinux enforcing, firewalld and NetworkManager. Guests use **Fedora 44 Cloud Base Generic**, login `fedora`. The host may be your daily machine: the labs never disable SELinux or firewalld, reload KVM modules, reboot the host or upgrade its operating system. Fedora is not a long-term-support release; check its [lifecycle](https://docs.fedoraproject.org/en-US/releases/lifecycle/) before long-lived use.

Course VMs use the system connection `qemu:///system` ([libvirt QEMU driver](https://libvirt.org/drvqemu.html)); chapter 2 compares it with the per-user connection.

The reference workstation runs kernel 7.2.7-200.fc44, QEMU 10.2.2, libvirt 12.0.0 with modular daemons, virt-install 5.1.0 and Fedora Cloud image build 44-1.7. The osinfo database has no `fedora44` entry yet, so the course uses `--osinfo fedora43`, the nearest one.

Keep a short inventory of your lab: host versions; each VM's machine type, CPU model, RAM and firmware; image checksums; guest addresses; backing chains; backup locations; and tested recovery steps.

### Setup 2. Arrange hardware

Start with about 32 GiB of RAM and 100 GiB of free disk space. Before a chapter adds guests, check with `free -h` that they fit. A thin virtual disk can eventually use its full advertised size.

| Chapters | Arrange before starting | What you can prove |
|---|---|---|
| 0–7 | One x86-64 host with nested virtualization (chapter 0.2) and local console access | KVM execution, networking, storage, images, confinement, backup and restore |
| 8 | Room for two nested hosts and an NFS guest, up to 12 GiB of guests | Migration and host-drain mechanics, labelled nested |
| 9–14 | Nothing more (chapters 11–13 install `gcc`, `bpftrace`, `perf`); 12.4 and parts of 13 are not validated on AMD | Incident response, API automation, the KVM API, QEMU's I/O path, the MMU and a smoke check; chapter 13 never assigns a device |

A **nested host** is a Fedora guest that runs its own KVM guests. It shares the workstation's CPU, power and disks, so it cannot prove recovery from the loss of a physical host. Label such results **nested**.

Never use the host's boot disk, its only management network interface or its only display device as a fault target.

### Setup 3. Reuse the lab and keep notes

Start with linux-01 in chapter 1 and keep it between chapters. Later chapters add guests such as linux-02 (chapter 5) and disposable nested hosts. Chapter 1.7 turns your commands into helper scripts; later starting points reuse a guest or rebuild it.

Everything the course writes in your home directory lives under `~/kvm-course`, including a dedicated SSH key and known_hosts file. VM disks live in the libvirt storage pool `kvm-course`. Course networks and extra pools have names starting with `kc-`. Reserve these names and paths for the course.

Keep notes outside `~/kvm-course`, which Appendix B deletes: per chapter, the commands, expected and observed results, and recovery notes. Mark each outcome **not started**, **in progress**, **passed** or **outstanding: reason**. An exit status of zero alone does not prove a milestone.

Each chapter's Clean up removes its temporary changes and says what to keep. **Appendix B removes the entire course**, including disks and the packages it installed; save what you want first.

### Setup 4. Work one checkpoint at a time

1. Read the goal and the concept explanation.
2. Type the commands for the smallest example.
3. Compare the output with the expected output before adding complexity.
4. Change or break one thing, test one diagnosis, and restore the working state.
5. Save useful commands and observations, then continue.

Commands run on the host unless sent into a guest. Management commands use `sudo virsh -c qemu:///system COMMAND`. Blocks that must stop at their first failure start with `( set -euo pipefail` or are saved as scripts with that line, so the failure stops them without closing your terminal. To see where a script fails silently, run it with `bash -x SCRIPT`. Placeholders are in CAPITALS and introduced before use; most addresses and UUIDs are derived from the running lab.

If a check fails, compare versions, permissions and logs before changing one thing. Never weaken host protections to force a pass, or change an unrelated VM, pool or network to match a lab.

### Setup 5. Set your own pace

Use chapters 0–2 to estimate your pace. Skip a refresher once you can pass its check, but never skip the result a chapter asks you to demonstrate.

## Milestones and assessment

| Part | Chapters | Milestone |
|---|---|---|
| I — Become comfortable with the stack | 0–5 | Checkpoint C1 (Appendix A): a verified KVM-accelerated first VM; after 4, explain and repair its network and storage; after 5, reproduce distinct guest identities |
| II — Operate and recover VMs | 6–9 | Demonstrate recovery access, a verified restore, a migration and an incident record |
| III — Automate with the libvirt API | 10 | A tested automation script that uses the libvirt API |
| IV — Understand the virtualization machinery | 11–13 | Run a tiny VMM, trace an I/O request through QEMU and VirtIO, and explain the hardware and MMU path |
| V — Integrate and keep learning | 14 | A layer-by-layer smoke check, a diagnosed fault and a plan for further learning |

For an unknown-fault assessment, another person injects a fault without telling you which. Solo practice uses reversible faults with separate answer notes: shuffle them, wait until the solutions are no longer fresh, diagnose, then check the answer. Label that **self-assessed**. Following a printed fix is rehearsal, not assessment.

## Use the right reference

Project documentation explains APIs; your installed Fedora packages decide which commands and options exist, so prefer the installed manual pages (`man virsh`, `man qemu-img`). QEMU's "master" documentation describes the next release. Recipes for Ubuntu or Arch use other package names, services and security policies; RHEL is closest to Fedora but ships older versions. Old books and videos explain concepts well; check their commands against your versions.
