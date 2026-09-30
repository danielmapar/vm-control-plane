# Appendix B · Preflight

Use this check on the reference host with linux-01's original single-NIC/default-NAT setup. It installs nothing and does not change configuration or VM state.

1. **Save the script below** as preflight.sh in your lab repository.

2. **After chapter 0, before creating the VM:** run `bash preflight.sh`. Expect "Preflight passed." Its 40 GiB check is for the initial exercise; still reserve the larger course budget and host RAM from Setup 2.

3. **After chapter 1:** complete chapter 1.5’s serial-log fingerprint comparison and add only that verified key to known\_hosts. Use your normal SSH configuration if the private key has a non-default path. Run `bash preflight.sh --post-vm ADDRESS`, replacing ADDRESS with linux-01's observed IPv4 lease. Expect "C1 checks passed." Inspect the printed root filesystem size to confirm the guest expanded its disk as expected.

4. **Keep the result.** Save stdout, stderr and exit status. If piping through tee, enable Bash pipefail so tee cannot hide a failed check. On FAIL, repair the named prerequisite and rerun. SSH/guest checks have a 180-second limit and reject cloud-init errors or warnings that need investigation.

The post-VM check queries  [KVM's enabled state in QEMU](https://www.qemu.org/docs/master/interop/qemu-qmp-ref.html#command-query-kvm), rather than inferring acceleration from CPU flags. query-kvm is available in the Ubuntu 24.04 baseline; a newer stack may use its replacement. Libvirt may log a custom-monitor taint for this read-only query. Record the reason; this is not permission to change managed configuration through QMP.

```python
#!/usr/bin/env bash
# Run on host-a after chapter 0. No configuration or VM state is changed.
# Usage: bash preflight.sh
#        bash preflight.sh --post-vm 192.168.122.123

# Stop on unhandled errors and unset variables; pipefail also catches
# failed pipeline stages. Explicit guards below handle expected failures.
set -euo pipefail
# Force a stable locale so tool output parses the same on every host.
export LC_ALL=C

# Print the failed check and stop; every FAIL names what to repair.
fail() { echo "FAIL: $*" >&2; exit 1; }
# Print a passed check.
ok() { echo "PASS: $*"; }

# Choose the mode from the argument count and first argument together.
case "$#:${1:-}" in
  # No arguments: host-only preflight, run before the VM exists.
  0:) mode=pre ;;
  # Exactly "--post-vm ADDRESS": full checkpoint C1 verification.
  2:--post-vm) mode=post ;;
  # Anything else is a usage error.
  *) fail "usage: bash preflight.sh [--post-vm GUEST_IPV4]" ;;
esac

# Load the distribution's ID and VERSION_ID variables.
. /etc/os-release
# This script encodes the course baseline; refuse other distributions.
[[ "$ID" == ubuntu && "$VERSION_ID" == 24.04 ]] || fail "requires the Ubuntu 24.04 reference host"
# The labs assume the x86-64 KVM/QEMU stack.
[[ "$(uname -m)" == x86_64 ]] || fail "requires x86_64"

# Every chapter-1 step assumes these tools resolve on PATH.
for cmd in sudo virsh qemu-system-x86_64 qemu-img virt-install \
    cloud-localds gpg curl python3 ssh timeout; do
  # A missing binary means chapter 0's package step is incomplete.
  command -v "$cmd" >/dev/null || fail "$cmd missing; complete chapter 0"
done

# Confirm each required package is fully installed, not merely present on disk.
for pkg in qemu-system-x86 libvirt-daemon-system libvirt-clients virtinst \
    qemu-utils cloud-image-utils ovmf ubuntu-keyring python3; do
  # dpkg-query prints "install ok installed" for a fully configured package.
  status=$(dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null) || fail "missing package: $pkg"
  # Reject half-installed or removed-but-configured states.
  [[ "$status" == *' ok installed' ]] || fail "package not installed: $pkg"
done

# The KVM character device is how userspace reaches the hypervisor.
[[ -c /dev/kvm ]] || fail "/dev/kvm missing; inspect firmware/modules"
# Chapter 1 verifies the image signature against this keyring.
[[ -f /usr/share/keyrings/ubuntu-cloudimage-keyring.gpg ]] || fail "image keyring missing"
ok "reference OS, commands, packages, KVM device and keyring"

# Wrapper: every virsh call targets the system connection with a stable locale.
v() { sudo env LC_ALL=C virsh -c qemu:///system "$@"; }
# Proves the libvirt daemon is running and this user may manage it.
v list --all >/dev/null || fail "cannot connect to system libvirt"
# The first VM attaches to libvirt's default NAT network; capture its state.
net=$(v net-info default) || fail "default network missing; see chapter 0"
# Defined but inactive still breaks DHCP; require an active network.
grep -Eq '^Active:[[:space:]]+yes$' <<<"$net" || fail "default network inactive"
ok "system connection and active default network"

if [[ "$mode" == pre ]]; then
  # Free bytes in the default libvirt image directory.
  avail=$(df -B1 --output=avail /var/lib/libvirt/images | tail -n 1) || fail "cannot inspect image-storage path"
  # Guard against unparseable df output before doing arithmetic on it.
  [[ "$avail" =~ ^[[:space:]]*[0-9]+$ ]] || fail "cannot read image-storage free space"
  # 40 GiB covers the base image, overlay, seed and working room.
  (( avail >= 40 * 1024 * 1024 * 1024 )) || fail "under 40 GiB free for initial VM work"
  ok "initial storage headroom; also check full course budget and host RAM reserve"
  echo "Preflight passed. KVM execution and guest access still need chapter 1."
  exit 0
fi

# ---- post-vm mode: verify checkpoint C1 against the running guest ----

# The address the operator observed in the DHCP lease.
address=$2
# Basic shape check before comparing against lease data.
[[ "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || fail "supply the observed guest IPv4 address"

# The domain must exist and be running before deeper checks make sense.
state=$(v domstate linux-01) || fail "linux-01 missing"
[[ "$state" == running ]] || fail "linux-01 is not running"

# Ask the running QEMU process itself whether KVM acceleration is active,
# via libvirt's QMP passthrough (read-only; may log a harmless taint).
v qemu-monitor-command linux-01 '{"execute":"query-kvm"}' |
  python3 -c '
import json, sys                              # QMP reply arrives on stdin
reply = json.load(sys.stdin)                  # {"return": {...}, "id": ...}
kvm = reply.get("return", {})                 # the query-kvm result object
present = kvm.get("present") is True          # KVM support visible to QEMU
enabled = kvm.get("enabled") is True          # guest actually runs accelerated
sys.exit(0 if (present and enabled) else 1)   # exit code feeds the shell check
' || fail "KVM acceleration not confirmed"

# Read the DHCP lease libvirt recorded for this guest.
leases=$(v domifaddr linux-01 --source lease) || fail "cannot read guest lease"
# Extract bare IPv4 addresses: keep rows whose protocol column is ipv4,
# then strip the /prefix from the address column.
ips=$(awk '$3 == "ipv4" {sub(/\/.*/, "", $4); print $4}' <<<"$leases")
# Exact whole-line match; -- protects against dash-leading input.
grep -Fx -- "$address" <<<"$ips" >/dev/null || fail "address does not match linux-01 lease"
ok "linux-01 running with KVM enabled and matching lease"

# In-guest verification over SSH, bounded at 180 s total. BatchMode forbids
# interactive prompts; StrictHostKeyChecking requires the pre-verified host
# key. The remote chain: wait for cloud-init to finish (fails on errors and
# on warnings needing investigation), confirm the hostname, then show the
# root mount and its size for the disk-expansion inspection.
timeout 180 ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=8 "ubuntu@$address" \
  'cloud-init status --wait && test "$(hostname)" = linux-01 && findmnt / && df -h /' ||
  fail "SSH/cloud-init/guest check failed; inspect chapter-1 evidence and verified SSH host key"
ok "key-based SSH, cloud-init, hostname and root filesystem inspected"

echo "C1 checks passed. Save output with XML, image digests and manifest."
```

Checked during this revision, on this commented version of the script: Bash syntax and rejection of an unsupported local operating system. The QMP parser reads the same enabled/present fields and keeps the same exit statuses. Run `bash -n preflight.sh` on your saved copy before first use. The corrected package check and post-VM path passed on a nested Ubuntu 24.04.5 host: libvirt/QMP reported KVM enabled, the DHCP lease matched, SSH succeeded, cloud-init was done, and the root filesystem expanded to about 19 GiB. The first run used trust on first use; a separate fresh nested probe then verified the ED25519 fingerprint against its host-side serial log before strict SSH login. Follow chapter 1.5 for that verified procedure. This proves the tested nested configuration, not bare-metal or multi-host behavior.
