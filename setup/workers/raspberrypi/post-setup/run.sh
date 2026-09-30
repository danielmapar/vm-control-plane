#!/usr/bin/env bash
# Run the existing Ansible container with workstation SSH-agent access.
set -euo pipefail

if [[ "${1:-}" == --help || "${1:-}" == -h ]]; then
  cat <<'HELP'
Usage: run.sh [ANSIBLE_OPTIONS]

Examples:
  ./run.sh --check --diff
  ./run.sh --limit worker1
  ./run.sh -e fan_full_speed=false

INVENTORY defaults to ~/.config/vm-control-plane/raspberrypi.ini.
CONTAINER_ENGINE can select docker or podman; otherwise Docker is tried first.
Build vmc-pi-post-setup as described in README.md before running.
HELP
  exit 0
fi

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }

engine=${CONTAINER_ENGINE:-}
if [[ -z "$engine" ]]; then
  if command -v docker >/dev/null 2>&1; then
    engine=docker
  elif command -v podman >/dev/null 2>&1; then
    engine=podman
  else
    fail 'Install Docker or Podman first.'
  fi
fi
command -v "$engine" >/dev/null 2>&1 || fail "Container engine not found: $engine"

inventory=${INVENTORY:-"$HOME/.config/vm-control-plane/raspberrypi.ini"}
[[ -f "$inventory" && -r "$inventory" ]] || fail "Inventory not readable: $inventory"
inventory=$(realpath -- "$inventory")
[[ -S "${SSH_AUTH_SOCK:-}" ]] || fail 'Start your SSH agent and load your key with ssh-add.'
[[ -f "$HOME/.ssh/known_hosts" ]] || fail 'Connect to each Pi with SSH first to verify its host key.'

exec "$engine" run --rm -it --network host --security-opt label=disable \
  -e SSH_AUTH_SOCK=/ssh-agent \
  -v "$SSH_AUTH_SOCK:/ssh-agent" \
  -v "$HOME/.ssh/known_hosts:/root/.ssh/known_hosts:ro" \
  -v "$inventory:/ansible/inventory.ini:ro" \
  vmc-pi-post-setup -i inventory.ini --ask-become-pass "$@"
