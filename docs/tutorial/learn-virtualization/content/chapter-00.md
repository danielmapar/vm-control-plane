# Linux prerequisites and a safe lab

**Goal.** Establish a recoverable, KVM-capable host using Linux skills you already have. Confirm storage and network boundaries before changing them. A CPU flag or /dev/kvm is only a clue; chapter 1 proves a KVM-accelerated guest runs.

**Refresh only if needed.** Use the  [Ubuntu libvirt guide](https://ubuntu.com/server/docs/how-to/virtualisation/libvirt/) and local manuals for systemctl, journalctl, ip, ss, lsblk and findmnt where a check is unfamiliar. Read  [libvirt connection modes](https://libvirt.org/drvqemu.html) before chapter 2. If basic host administration is new, complete a distribution administration tutorial first.

## 0.1 Inspect the host

1. On host-a, identify the CPU, RAM, lab storage, management NIC and default route. These commands only inspect the host:

```bash
# Record the kernel and architecture actually running.
uname -a
# Inspect the CPU and its virtualization capabilities.
lscpu
# Check RAM available for guests while keeping a host reserve.
free -h
# Map disks and filesystems before choosing any lab storage.
lsblk -f
# Identify the filesystem backing the host root directory.
findmnt /
# List interfaces and addresses in a compact form.
ip -brief address
# Identify the default route used to reach the host.
ip route
# Notice failed system services before adding virtualization.
systemctl --failed
```

2. Identify the system/boot disk in your notebook. Do not partition or format a disk in this chapter.

**Check:** The host boot disk, management route and available lab resources are identified.

## 0.2 Check virtualization

1. Install and run Ubuntu’s initial capability check:

```bash
# Refresh Ubuntu’s package index.
sudo apt update
# Install the distribution’s KVM capability checker.
sudo apt install cpu-checker
# Check whether this host can use KVM acceleration.
kvm-ok
```

2. Expect “KVM acceleration can be used” on a capable, correctly configured host. If it fails, inspect BIOS/UEFI virtualization settings, kernel modules and permissions. The next step adds libvirt’s more detailed validator.

**Check:** kvm-ok is understood; chapter 1 remains the actual guest-acceleration proof.

## 0.3 Install the stack

1. On the dedicated Ubuntu host, install the reference stack. The Bash array keeps each package’s purpose beside its name:

```bash
# Collect the packages used in the starting labs.
packages=(
  qemu-system-x86        # Install the x86 QEMU system emulator used with KVM.
  libvirt-daemon-system  # Provide the system libvirt service.
  libvirt-clients        # Supply virsh and other management clients.
  virtinst               # Supply virt-install for VM creation.
  qemu-utils             # Inspect and create disk images with qemu-img.
  cloud-image-utils      # Build NoCloud seed images with cloud-localds.
  ovmf                   # Provide UEFI firmware for the later profile probe.
  ubuntu-keyring         # Supply Ubuntu’s trusted cloud-image signing keys.
  git                    # Keep code, manifests and recovery notes under version control.
  curl                   # Download artifacts and exercise the course HTTP service.
  gnupg                  # Verify the signed image-checksum manifest.
  python3                # Run Appendix B’s QMP reply parser on the host.
)
# Expand each array item as a separate package argument.
sudo apt install "${packages[@]}"
# Confirm that Noble’s cloud-image keyring is present before chapter 1.
test -f /usr/share/keyrings/ubuntu-cloudimage-keyring.gpg
# Record installed package versions in the lab notebook.
dpkg-query -W "${packages[@]}"
# Record the running kernel version as well.
uname -r
```

The **ubuntu-keyring** package supplies that keyring on the reference distribution. Ubuntu 24.04 accepts `qemu-kvm` as a provided package name; the installed package to record and check with dpkg is `qemu-system-x86`.

2. Run sudo virt-host-validate qemu and record its output; interpret warnings against the planned workload rather than requiring every optional feature. Confirm sudo virsh -c qemu:///system list --all works. Use sudo for the initial lab rather than adding a broad permanent administrator group membership by habit.

**Check:** The installed stack, validator result and system-libvirt connection are recorded.

## 0.4 Check NAT

1. Inspect libvirt’s default NAT network: sudo virsh -c qemu:///system net-list --all; sudo virsh -c qemu:///system net-dumpxml default. If it is defined but inactive, start it with net-start default. Stop here if it is absent; create it using the installed distribution’s documented procedure, then inspect its bridge, DHCP range and NAT rules.

2. Do not reconfigure the physical uplink to make the first VM work.

**Check:** The default NAT network is active and its bridge, DHCP and forwarding behavior are known.

## 0.5 Rehearse recovery

1. Fill in the Setup 3 inventory with the disk exclusion list, SSH public key, package manifest, recovery access and an independent backup location. Pick a bounded VM budget. In a disposable test service, deliberately stop the service, locate its status and journal, restart it and verify recovery.

**Check:** The test service is healthy again and a separate backup location is named.

**Pass and cleanup.** Identify the host default route and protected disk, locate a process owner/log, and distinguish missing KVM from guest misconfiguration. Leave networking and the test service healthy. Chapter 1 proves KVM execution; label nested results as nested, not bare-metal or IOMMU evidence.
