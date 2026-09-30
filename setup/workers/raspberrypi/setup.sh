#!/usr/bin/env bash
# Linux workstation entry point. Only Imager runs with elevated privileges.
set -euo pipefail
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
exec python3 "$SCRIPT_DIR/flash/prepare.py" "$@"
