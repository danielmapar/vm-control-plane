#!/usr/bin/env bash
# Distributed real-KVM demo — VM SIDE. The control-plane + PostgreSQL run on a
# SEPARATE host (the Windows dev box); this script reaches them over an SSH
# reverse tunnel at 127.0.0.1:<port>. The nested-KVM VM therefore runs ONLY the
# libvirt agent + libvirt + the guest — the same near-isolated environment the
# integration test boots reliably in. That split is the whole reason a
# control-plane/agent architecture exists, and here it is also what lets a guest
# boot at all: the DB/control-plane workload no longer shares the fragile
# nested-VirtualBox VM with the guest (see docs/reviews/real-substrate-findings.md).
#
# Driven by scripts/demo/02-distributed.ps1 on the Windows side. Run as the
# unprivileged service user (libvirt/kvm/vmc groups); embedded Postgres, on the
# Windows side, refuses to run as root.
set -uo pipefail
PORT="${1:?usage: 02-distributed-vm.sh <tunnel-port>}"
cd "$(dirname "$0")/../.."
export PATH="$PATH:/usr/local/go/bin"
SERVER="127.0.0.1:$PORT"

echo "== 1. build (agent + vmctl) =="
go build -o bin/ ./cmd/...

echo "== 2. wait for the remote control-plane via the tunnel ($SERVER) =="
ok=""
for i in $(seq 1 30); do
  if bin/vmctl --server "$SERVER" list vms >/dev/null 2>&1; then ok=1; echo "control-plane reachable over the tunnel"; break; fi
  sleep 2
done
[ -n "$ok" ] || { echo "control-plane not reachable over tunnel $SERVER"; exit 1; }

echo "== 3. seed image cache + warm it into RAM =="
./scripts/spike/seed-image.sh
[ -f /var/lib/vmc/cache/ubuntu-24.04.qcow2 ] && cat /var/lib/vmc/cache/ubuntu-24.04.qcow2 >/dev/null 2>&1 || true

echo "== 4. ephemeral SSH key =="
KEYDIR=$(mktemp -d); ssh-keygen -t ed25519 -N "" -f "$KEYDIR/id" >/dev/null; PUB="$KEYDIR/id.pub"

echo "== 5. start ONLY the libvirt agent (dials the remote control-plane) =="
AGENTLOG=$(mktemp /tmp/vmc-agent.XXXXXX.log)
bin/hypervisor-agent --server "$SERVER" --host-id host-local --nodes node-a,node-b \
  --driver libvirt --storage-root /var/lib/vmc --mgmt-network vmc-mgmt \
  --ssh-key-file "$PUB" --pin-cpuset 8-15 --poll-interval 3s >"$AGENTLOG" 2>&1 &
AGENT=$!
trap 'virsh -c qemu:///system destroy real-1 >/dev/null 2>&1 || true; kill "$AGENT" 2>/dev/null || true' EXIT
sleep 6
if ! kill -0 "$AGENT" 2>/dev/null; then echo "agent exited:"; cat "$AGENTLOG"; exit 1; fi

export VMCTL_SERVER="$SERVER"
echo "== 6. create a real VM through the distributed control plane =="
OP=$(bin/vmctl create vm real-1 --cpu 1 --memory 1GiB --image ubuntu-24.04 --disk 4GiB | grep -oiE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | tail -1)
echo "operation $OP"
bin/vmctl op wait "$OP" --timeout 4m
bin/vmctl list vms

echo "== 7. wait for the mgmt IP (newest real-1 lease) =="
IP=""
for i in $(seq 1 40); do
  IP=$(timeout 10 virsh -c qemu:///system net-dhcp-leases vmc-mgmt 2>/dev/null \
        | awk '$6=="real-1"{print $1"T"$2, $5}' | sort -r | head -1 | awk '{print $2}' | cut -d/ -f1 || true)
  [ -n "$IP" ] && break
  sleep 3
done
[ -n "$IP" ] || { echo "no DHCP lease"; exit 1; }
echo "guest IP: $IP"

echo "== 8. SSH into the guest (retry until cloud-init brings up sshd) =="
SSH_OK=""
for i in $(seq 1 36); do
  if OUT=$(timeout 15 ssh -i "$KEYDIR/id" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 -o BatchMode=yes ubuntu@"$IP" \
        'cloud-init status --wait >/dev/null 2>&1; echo "REAL-KVM GUEST REACHED: $(hostname) / $(uname -r)"' 2>/dev/null); then
    echo "$OUT"; SSH_OK=1; break
  fi
  sleep 5
done
[ -n "$SSH_OK" ] || { echo "guest never answered SSH within timeout"; exit 1; }

echo "== 9. delete =="
DOP=$(bin/vmctl delete vm real-1 | grep -oiE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | tail -1)
bin/vmctl op wait "$DOP" --timeout 2m
echo "== DISTRIBUTED DEMO COMPLETE: a real KVM guest was booted, reached over SSH, and deleted through a control plane running on a SEPARATE host =="
