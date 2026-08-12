#!/usr/bin/env bash
# Seed the content of the image cache the libvirt driver resolves against
# (plan D10: cache admission is pinned + verified, done HERE, never on the
# hot path). Ubuntu cloud images are already standalone qcow2.
#
#   ./scripts/spike/seed-image.sh [image-name] [url]
#
# Defaults to the Ubuntu 24.04 minimal cloud image. Writes
# /var/lib/vmc/cache/<name>.qcow2 (durable, group-owned).
set -euo pipefail

NAME="${1:-ubuntu-24.04}"
URL="${2:-https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img}"
ROOT="${VMC_STORAGE_ROOT:-/var/lib/vmc}"
CACHE="$ROOT/cache"
DEST="$CACHE/$NAME.qcow2"

mkdir -p "$CACHE"
if [ -f "$DEST" ]; then
  echo "seed-image: $DEST already present"
  qemu-img info --output=json "$DEST" | jq -r '"seed-image: format=\(.format) virtual-size=\(.["virtual-size"])"'
  exit 0
fi

TMP="$CACHE/.$NAME.download"
echo "seed-image: downloading $URL"
curl -fSL --retry 3 -o "$TMP" "$URL"

FMT=$(qemu-img info --output=json "$TMP" | jq -r '.format')
if [ "$FMT" != "qcow2" ]; then
  echo "seed-image: converting $FMT -> qcow2"
  qemu-img convert -O qcow2 "$TMP" "$TMP.qcow2"
  mv "$TMP.qcow2" "$TMP"
fi

# Reject a layered base (must be standalone for the one-layer refcount model).
if [ -n "$(qemu-img info --output=json "$TMP" | jq -r '.["backing-filename"] // ""')" ]; then
  echo "seed-image: base has a backing file — not standalone" >&2
  rm -f "$TMP"; exit 1
fi

sync
mv "$TMP" "$DEST"
chgrp vmc "$DEST" 2>/dev/null || true
chmod g+r "$DEST" 2>/dev/null || true
echo "seed-image: cached $DEST"
qemu-img info --output=json "$DEST" | jq -r '"seed-image: format=\(.format) virtual-size=\(.["virtual-size"])"'
