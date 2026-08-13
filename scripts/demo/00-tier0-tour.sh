#!/usr/bin/env bash
# A short single-terminal tour of the Tier-0 (fake-driver) control plane, used
# to record the README demo. No hypervisor required: it starts the stack, runs
# the VM lifecycle operations, then stops.
set -uo pipefail
cd "$(dirname "$0")/../.."
export PATH="$PATH:/usr/local/go/bin"

c()    { printf '\033[1;32m$ %s\033[0m\n' "$*"; }            # a typed command
note() { printf '\n\033[1;36m# %s\033[0m\n' "$*"; sleep 1; } # a comment

note "Tier 0: no hypervisor, fake drivers. Start the stack."
# Uses prebuilt binaries (make build) so the tour starts instantly.
DEVLOG=$(mktemp)
./bin/dev >"$DEVLOG" 2>&1 &
DEV=$!
trap 'kill "$DEV" 2>/dev/null || true' EXIT
for i in $(seq 1 60); do S=$(grep -oE 'VMCTL_SERVER=127.0.0.1:[0-9]+' "$DEVLOG" | head -1 | cut -d= -f2 || true); [ -n "$S" ] && break; sleep 1; done
export VMCTL_SERVER="$S"
c "export VMCTL_SERVER=$S"; sleep 1

note "Create a VM. It returns an async operation."
c "bin/vmctl create vm demo-1 --cpu 2 --memory 2GiB"
out=$(bin/vmctl create vm demo-1 --cpu 2 --memory 2GiB); echo "$out"
op=$(echo "$out" | grep -oiE '[0-9a-f-]{36}' | tail -1); sleep 2

note "Wait for the operation, then list. REVISION is applied/desired."
c "bin/vmctl op wait $op"; bin/vmctl op wait "$op" --timeout 90s; sleep 1
c "bin/vmctl list vms"; bin/vmctl list vms; sleep 2

note "Stop it. Power is the mutable part of the spec."
c "bin/vmctl stop vm demo-1"
sout=$(bin/vmctl stop vm demo-1); echo "$sout"; sop=$(echo "$sout" | grep -oiE '[0-9a-f-]{36}' | tail -1)
bin/vmctl op wait "$sop" --timeout 90s >/dev/null; sleep 1
c "bin/vmctl list vms"; bin/vmctl list vms; sleep 2

note "Delete it: tombstone, reconciled teardown, finalize."
c "bin/vmctl delete vm demo-1"
dout=$(bin/vmctl delete vm demo-1); echo "$dout"; dop=$(echo "$dout" | grep -oiE '[0-9a-f-]{36}' | tail -1)
c "bin/vmctl op wait $dop"; bin/vmctl op wait "$dop" --timeout 90s; sleep 1
c "bin/vmctl list vms"; bin/vmctl list vms; sleep 2

note "Tier 0 lifecycle complete."; sleep 1
