### Appendix B. Remove the course completely

B.2 deletes all course guests and disks: save what you need. After an interrupted chapter, run its Clean up first; it undoes what B.2 cannot know, like a file in `/etc`.

#### B.1 What the course leaves behind

**Goal.** Know what B.2 removes and why.

`~/kvm-course` and the pool directory `/var/lib/libvirt/images/kvm-course` each hold an empty marker file, `.created-by-course`. **Course guests** have a disk in the pool directory. B.2 removes the network `kc-isolated`, or a log, only if its creation time (`stat -c %W`) is after the marker's in `~/kvm-course`; older or undated ones stay. Without the pool marker, B.2 leaves that directory, its guests and logs alone. The `kc-` prefix is reserved for the course while it is installed: do not create or edit objects of your own with such names.

| Left behind | What B.2 does |
|---|---|
| Course guests with UEFI variables, TPM state, saved memory, snapshot and checkpoint records | `virsh destroy` and `undefine` |
| Their QEMU, serial console and TPM logs | Deletes them |
| Network `kc-isolated` (chapter 3), pool `kvm-course` and its directory | Stops, undefines, deletes; rescans pool `default`, whose directory held it |
| libvirt bookkeeping: empty port directories and `.macs` list, removed networks' PID files | Deletes it |
| Certificate files of the TPM emulator swtpm in `/var/lib/swtpm-localca` (UEFI guests get a TPM) | Deletes new ones if no TPM state remains |
| virt-install's log in `/root/.cache/virt-manager` | Deletes it |
| Modules `vhost_net`, `tap`, `vhost`, `vhost_iotlb`, loaded for guest network cards | `rmmod` if unused; harmless, as the kernel loads them again on demand. `tun`, loaded with them, stays: other programs use it |
| GnuPG helpers of the course keyring (1.1) | Stops them |
| Packages | Undoes the transactions in `~/kvm-course/dnf-transactions.txt`, newest first |
| `~/kvm-course` | Deletes it last, with `sudo` for root-owned files |

B.2 keeps `~/.config/libvirt` and `~/.cache/libvirt`, which session connections (2.5) create: they may hold your own configuration, so if the course created them they stay behind. Removing them is **not validated here**: both predated the course on the validation host.

#### B.2 Remove everything

**Goal.** Return the host to its pre-course state.

**Commands.** `LC_ALL=C` keeps virsh output in English for the checks; `virsh` means `sudo virsh -c qemu:///system`; `after_marker FILE` is true for a file created after the marker. It stops at the first error, keeping `~/kvm-course`.

```bash
(
set -euo pipefail
export LC_ALL=C
D=$HOME/kvm-course
P=/var/lib/libvirt/images/kvm-course
if [ ! -e "$D" ]; then echo 'Nothing to remove: ~/kvm-course does not exist'; exit 0; fi
if [ ! -f "$D/.created-by-course" ]; then echo 'STOP: ~/kvm-course has no course marker' >&2; exit 1; fi
if ! command -v virsh >/dev/null; then
  if [ -s "$D/dnf-transactions.txt" ] || sudo test -f "$P/.created-by-course"; then
    echo 'STOP: virsh is missing but course VMs, disks or packages remain: install libvirt-client, then run B.2 again' >&2; exit 1
  fi
  rm -rf "$D"; echo 'The course has been removed.'; exit 0
fi
virsh() { sudo virsh -c qemu:///system "$@"; }
start=$(stat -c %W "$D/.created-by-course")
after_marker() { born=$(sudo stat -c %W "$1" 2>/dev/null) || return 1; [ "$start" -gt 0 ] && [ "$born" -gt "$start" ]; }
marked=no
if sudo test -f "$P/.created-by-course"; then marked=yes
elif sudo test -e "$P"; then echo "Keeping $P: it has no course marker"; fi

echo 'Remove course guests'
domains=$(virsh list --all --name)
while IFS= read -r name; do
  [ -n "$name" ] && [ "$marked" = yes ] || continue
  disks=$(virsh domblklist "$name")
  grep -qF " $P/" <<< "$disks" || continue
  if [ "$(virsh domstate "$name")" != 'shut off' ]; then virsh destroy "$name"; fi
  if virsh dominfo "$name" >/dev/null 2>&1; then
    virsh undefine "$name" --nvram --tpm --managed-save --snapshots-metadata --checkpoints-metadata
  fi
done <<< "$domains"

echo 'Remove their logs'
logs=$(sudo find /var/log/libvirt/qemu -maxdepth 1 -type f -name '*.log' ! -name '*-console.log')
while IFS= read -r log; do
  [ -n "$log" ] && [ "$marked" = yes ] || continue
  name=$(basename "$log" .log)
  if virsh dominfo "$name" >/dev/null 2>&1 || ! sudo grep -qF "$P/" "$log"; then continue; fi
  for file in $(sudo find /var/log/libvirt/qemu /var/log/swtpm/libvirt/qemu -maxdepth 1 -type f \( -name "$name.log*" -o -name "$name-console.log*" -o -name "$name-swtpm.log*" \) 2>/dev/null); do
    [ "$file" = "$log" ] && continue
    if after_marker "$file"; then sudo rm "$file"; fi
  done
  if after_marker "$log"; then sudo rm "$log"; fi
done <<< "$logs"

echo 'Remove the network kc-isolated (chapter 3) and the pool kvm-course'
if virsh net-info kc-isolated >/dev/null 2>&1; then
  if ! after_marker /etc/libvirt/qemu/networks/kc-isolated.xml; then echo 'Keeping network kc-isolated: older than the course'
  else
    if [ "$(virsh net-info kc-isolated | awk '/^Active:/ {print $2}')" = yes ]; then virsh net-destroy kc-isolated; fi
    virsh net-undefine kc-isolated
    sudo rm -f /run/libvirt/network/kc-isolated.pid
  fi
fi
if virsh pool-info kvm-course >/dev/null 2>&1; then
  xml=$(virsh pool-dumpxml kvm-course)
  if [ "$marked" = no ] || ! grep -qF "<path>$P</path>" <<< "$xml"; then echo "Keeping pool kvm-course: it is not the marked $P"
  else
    if [ "$(virsh pool-info kvm-course | awk '/^State:/ {print $2}')" = running ]; then virsh pool-destroy kvm-course; fi
    virsh pool-undefine kvm-course
  fi
fi
if [ "$marked" = yes ]; then sudo rm -rf "$P"; fi
if [ "$(virsh pool-info default 2>/dev/null | awk '/^State:/ {print $2}')" = running ]; then virsh pool-refresh default; fi

echo 'Remove leftovers'
for dir in /run/libvirt/network/default /run/libvirt/network/kc-isolated; do
  sudo rmdir "$dir/ports" "$dir" 2>/dev/null || true
done
sudo rmdir /run/libvirt/qemu/swtpm 2>/dev/null || true
if [ -z "$(sudo ls -A /var/lib/libvirt/swtpm 2>/dev/null)" ]; then
  for file in $(sudo find /var/lib/swtpm-localca -maxdepth 1 -type f 2>/dev/null); do
    if after_marker "$file"; then sudo rm "$file"; fi
  done
fi
bridge=$(virsh net-info default 2>/dev/null | awk '/^Bridge:/ {print $2}') || true
macs=/var/lib/libvirt/dnsmasq/$bridge.macs
if [ -n "$bridge" ] && [ "$(sudo cat "$macs" 2>/dev/null | tr -d '[:space:]')" = '[]' ]; then sudo rm "$macs"; fi
if after_marker /root/.cache/virt-manager; then sudo rm -rf /root/.cache/virt-manager; fi
if after_marker /root/.cache; then sudo rmdir /root/.cache 2>/dev/null || true; fi
for module in vhost_net tap vhost vhost_iotlb; do
  if [ "$(cat "/sys/module/$module/refcnt" 2>/dev/null)" = 0 ]; then sudo rmmod "$module"; fi
done
if [ -d "$D/gnupg" ]; then GNUPGHOME=$D/gnupg gpgconf --kill all; fi

echo 'What remains in libvirt:'
virsh list --all
virsh net-list --all
virsh pool-list --all
echo 'Undo package installs'
if [ -f "$D/dnf-transactions.txt" ]; then
  for id in $(sort -rnu "$D/dnf-transactions.txt"); do
    sudo dnf history undo "$id" -y
    sed -i "/^$id\$/d" "$D/dnf-transactions.txt"
  done
fi
sudo rm -rf "$D"
echo 'The course has been removed.'
)
```

Confirm:

```bash
if [ -e ~/kvm-course ]; then echo 'Course files remain: read the error above' >&2; false; else echo '~/kvm-course is gone'; fi
```

**Expected output.** To run every step, the validation also made the network `kc-isolated` and a UEFI guest with a TPM, profile-probe (trimmed; yours differs):

```text
Domain 'linux-01' destroyed
Domain 'linux-01' has been undefined
Domain 'profile-probe' has been undefined
Network kc-isolated has been undefined
Pool kvm-course has been undefined
Pool default refreshed
Removing:
 cloud-utils-cloud-localds noarch 0:0.33-13.fc44 fedora      52.8 KiB
 virt-install              noarch 0:5.1.0-4.fc44 fedora      34.9 KiB
The course has been removed.
~/kvm-course is gone
```

**Check.** The three lists show only pre-course objects, and `~/kvm-course` is gone.

**If it fails.** Fix the error and rerun B.2. If a package undo fails, the failing ID is the largest number left in `~/kvm-course/dnf-transactions.txt` (`dnf history list` shows the transactions), and `dnf history info ID` lists what it installed. The usual cause is a package updated or removed since then (**not validated here**: none changed during these runs). To remove those packages anyway, run `sudo dnf remove` with exactly their names and without `-y`, and answer `n` if dnf also lists any other package. Then, or to keep them, delete only that ID's line and rerun B.2.

**Clean up.** A second run prints `Nothing to remove`.
