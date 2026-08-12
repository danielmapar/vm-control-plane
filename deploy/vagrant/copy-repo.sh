#!/usr/bin/env bash
# Unprivileged provisioner (runs as `vagrant`): copies the repo source from
# the synced mount into the VM's OWN filesystem so builds and the spike run
# off /vagrant/... never a shared mount (plan §8, host-setup.sh rule).
set -euo pipefail
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

DEST="$HOME/vm-control-plane"
echo "copy-repo: syncing /repo -> $DEST (excluding .git, bin, .vagrant)"
mkdir -p "$DEST"
rsync -a --delete \
  --exclude '.git' --exclude 'bin' --exclude 'deploy/vagrant/.vagrant' \
  --exclude '*.exe' \
  /repo/ "$DEST/"

cd "$DEST"
echo "copy-repo: go version: $(go version)"
echo "copy-repo: warming the module cache (may take a minute on first run)"
go build ./... 2>&1 | tail -5 || echo "copy-repo: build reported issues (expected if real drivers are unfinished)"

echo "copy-repo: OK — 'vagrant ssh' then 'cd ~/vm-control-plane && ./scripts/spike/spike.sh'"
