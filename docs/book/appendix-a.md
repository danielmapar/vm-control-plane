### Appendix A. Preflight and checkpoint C1

The preflight checks the host before you create a VM; checkpoint C1 checks the running linux-01 after chapter 1. Neither repairs the host: they never change networks or weaken security, and C1 only refreshes the course's own SSH entries for linux-01. A CPU flag and `/dev/kvm` are necessary, but only the running QEMU process can show that a guest really uses KVM. Reading: [virsh](https://www.libvirt.org/manpages/virsh.html) and [QEMU's query-kvm](https://www.qemu.org/docs/master/interop/qemu-qmp-ref.html).

#### A.1 Check the host

**Goal.** Refuse an incomplete host before creating a VM.

**Commands.** Run chapter 0 first. Save the checks as a script and run it; it stops at the first failed check. 40 GiB of free space is enough to start, not for the whole course:

```bash
cat > ~/kvm-course/bin/preflight.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
. /etc/os-release
[ "$ID" = fedora ]
[ "$VERSION_ID" = 44 ]
[ "$(uname -m)" = x86_64 ]
rpm -q qemu-kvm qemu-img libvirt-daemon-kvm libvirt-daemon-config-network libvirt-client virt-install \
  cloud-utils-cloud-localds genisoimage guestfs-tools edk2-ovmf gnupg2-verify
test -c /dev/kvm
test -f /etc/pki/rpm-gpg/RPM-GPG-KEY-fedora-44-primary
getenforce | grep -x Enforcing
sudo firewall-cmd --state
systemctl is-active virtqemud.socket
systemctl is-active virtnetworkd.socket
systemctl is-active virtstoraged.socket
sudo virsh -c qemu:///system net-info default | grep -E '^Active: +yes'
sudo virsh -c qemu:///system net-dumpxml default | grep "<forward mode='nat'"
avail=$(df -B1 --output=avail /var | tail -n 1)
df -h /var
[ $avail -ge $((40 * 1024 * 1024 * 1024)) ]
free -h
echo 'Host preflight passed; C1 checks the guest.'
EOF
bash ~/kvm-course/bin/preflight.sh
```

**Expected output.** On the validation host (excerpt; the `df` and `free -h` tables are left out, and sizes differ):

```text
Enforcing
running
active
active
active
Active:         yes
  <forward mode='nat'>
Host preflight passed; C1 checks the guest.
```

**Check.** The script ends with `Host preflight passed`. Read the free RAM in the `free -h` output too.

**If it fails.** The script stops at the failed check, often silently. `bash -x ~/kvm-course/bin/preflight.sh` prints each command before it runs; the last one printed failed. Missing package: chapter 0.3. No `/dev/kvm`: chapter 0.2. Inactive socket or network: chapter 0.3–0.4; never edit or remove a network or VM that existed before the course.

**Clean up.** Keep the script; it changed no host configuration.

#### A.2 Check the first VM

**Goal.** Confirm actual KVM use, an authenticated login, a clean cloud-init run and the grown Btrfs root.

**Commands.** Run this after chapter 1.7; it repeats the checks of 1.5. `query-kvm` asks the running QEMU process whether it uses KVM. `trust.sh` trusts the guest's key only if its fingerprint is in the console log and waits for cloud-init. `ssh … 'bash -se' <<'GUEST'` runs the lines up to `GUEST` in a shell inside linux-01 (`-s` reads them from ssh's input, `-e` stops at the first failure). `guest-ping` asks the guest agent inside linux-01 to answer. Because the two queries go to QEMU and to the agent directly, not through libvirt's normal API, libvirt marks the domain *tainted* (`custom-monitor`, `custom-ga-command`): a note for bug reports, not damage.

```bash
cat > ~/kvm-course/bin/checkpoint-c1.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C
[ "$(sudo virsh -c qemu:///system domstate linux-01)" = running ]
sudo virsh -c qemu:///system qemu-monitor-command linux-01 '{"execute":"query-kvm"}' | grep -F '"enabled":true,"present":true'
bash ~/kvm-course/bin/trust.sh linux-01
ssh -F ~/kvm-course/ssh/config linux-01 'bash -se' <<'GUEST'
set -euo pipefail
[ "$(hostname)" = linux-01 ]
[ "$(findmnt -n -o FSTYPE /)" = btrfs ]
size=$(df -B1 --output=size / | tail -n 1)
[ $size -ge $((17 * 1024 * 1024 * 1024)) ]
df -h /
systemctl is-active qemu-guest-agent.service
systemctl is-active serial-getty@ttyS0.service
getenforce | grep -x Enforcing
GUEST
sudo virsh -c qemu:///system qemu-agent-command linux-01 '{"execute":"guest-ping"}'
echo 'C1 passed: KVM, verified SSH, cloud-init, Btrfs growth, SELinux and guest agent.'
EOF
bash ~/kvm-course/bin/checkpoint-c1.sh
```

**Expected output.** On the validation host this printed (excerpt; sizes, times and fingerprints differ: compare yours with your own console log, not with this one):

```text
{"return":{"enabled":true,"present":true},"id":"libvirt-15"}
<14>Oct  2 06:31:15 cloud-init: 256 SHA256:NsAP285xpno0uEf2JyicHCPwnYZXQanIxEU8JD9Bxag root@linux-01 (ED25519)
status: done
linux-01
/dev/vda3        20G  670M   19G   4% /
active
active
Enforcing
{"return":{}}
C1 passed: KVM, verified SSH, cloud-init, Btrfs growth, SELinux and guest agent.
```

**Check.** The script ends with `C1 passed`. The root is Btrfs and at least 17 GiB, because on first boot the guest grew its root partition and file system to fill the 20-GiB disk.

**If it fails.** Run `bash -x ~/kvm-course/bin/checkpoint-c1.sh`; if the last command shown is `ssh`, the failed check is inside the guest. Fingerprint not in the console log: never bypass that check. cloud-init exit status 2 means degraded, not passed: read `/var/log/cloud-init.log` in the guest.

**Clean up.** Only the course's own SSH entries for linux-01 were refreshed; keep linux-01.

#### Clean up

Keep both scripts while you study, and repeat C1 whenever you want to know that linux-01 is healthy. They check the local linux-01 only: after chapter 8 migrates a guest, check it where it runs. Appendix B.2 removes the scripts with the rest of the course.
