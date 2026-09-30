# Raspberry Pi post-setup

Configure one or more Raspberry Pi 5 workers over SSH after their first boot.
The first task keeps the fan at **100% PWM**, including after reboot. RPM varies
with the fan model.

Ansible adds a marked block to `/boot/firmware/config.txt` and saves the original
once at `/var/backups/vmc-pi-config.txt`. It uses native Raspberry Pi fan settings;
no extra service or package is installed on the Pi. Changed Pis **reboot one at
a time**, then Ansible checks PWM output and fan rotation. Repeating a successful
run makes no changes.

You need Linux, Docker or Podman, SSH access, and the Pi account's sudo password.
The Pi needs Raspberry Pi OS, Python 3, and a PWM fan on its four-pin fan header.

On Fedora, the `podman-docker` compatibility package provides a `docker` command
that runs **Podman**, even when Docker Engine is not installed. This is the setup
used to test this runner. The message `Emulate Docker CLI using podman` is normal;
the commands below work with it. Run `docker --version` to see which engine your
command uses.

## 1. Prepare your inventory and SSH access

Keep your inventory outside the public repository, for example at
`~/.config/vm-control-plane/raspberrypi.ini`. From this folder, copy the example
if you do not already have an inventory:

```bash
mkdir -p ~/.config/vm-control-plane
cp -i inventory.example.ini ~/.config/vm-control-plane/raspberrypi.ini
```

Edit the copy, replacing the example addresses and account with yours.

Use reserved IPs or DNS names that resolve inside the container. IPs avoid
container-specific `.local` discovery issues. Omit the second worker for one Pi.

Load your key into the workstation's SSH agent, then connect to each inventory
address once. Verify new host fingerprints before accepting them:

```bash
ssh-add ~/.ssh/id_ed25519_lab_workers
ssh -i ~/.ssh/id_ed25519_lab_workers lab@192.0.2.10
ssh -i ~/.ssh/id_ed25519_lab_workers lab@192.0.2.11
```

Run `exit` after each connection. Keep passwords and private key contents out of
the inventory. The container uses your SSH agent; it does not copy private keys.

## 2. Build the runner

From this folder:

```bash
docker build -t vmc-pi-post-setup .
```

`docker` can be replaced with `podman` in these commands.

## 3. Preview and apply

Run `run.sh` from an interactive terminal. It selects Docker when available,
otherwise Podman, and forwards your options to the existing Ansible playbook.
Use `CONTAINER_ENGINE=podman ./run.sh ...` to select Podman explicitly.
Use `INVENTORY=/path/to/your.ini ./run.sh ...` to select another inventory.

The launcher's SELinux option lets the container use the existing agent socket
on Fedora without relabeling it. SSH host-key checking stays enabled. The
mounted agent allows the runner to authenticate using your unlocked keys.

Preview without changing or rebooting the Pis:

```bash
./run.sh --check --diff
```

Apply to all inventory hosts, or just one:

```bash
./run.sh
# Or:
./run.sh --limit worker1
```

Enter the **Pi account's sudo password**, not the SSH key passphrase. If the Pis
have different sudo passwords, run separately with `--limit` for each worker.
Preview shows the proposed configuration; fan verification happens on apply.

## Restore automatic cooling

```bash
./run.sh -e fan_full_speed=false
```

This removes only the managed block and reboots changed Pis, restoring the fan
settings that existed beforehand. Other boot settings stay intact. If a run is
interrupted after writing the configuration but before rebooting, reboot the
affected Pi manually before running the playbook again.

Fan parameters are documented in the [Raspberry Pi firmware reference](https://github.com/raspberrypi/firmware/blob/master/boot/overlays/README).
All four cooling levels use `255` so later temperature thresholds cannot lower
the fan speed. The first threshold is `0` millidegrees Celsius, keeping the fan
active at normal operating temperatures.

## Tested

Verified on 2026-09-30 with Podman 5.8.7 and Raspberry Pi 5 workers with 8 GB and
16 GB RAM. Both rebooted with full PWM output and confirmed fan rotation. A
second run reported zero changes and no reboots. Backups matched the original
boot files, and settings outside the managed block were unchanged.
