# Raspberry Pi 5 worker setup

Prepare a Pi in two stages: flash its operating system, then configure the
running Pi over SSH. Run the commands below from the repository root.

## 1. Flash the card

Follow the [flashing guide](flash/README.md) to install the workstation tools,
then run:

```bash
./setup/workers/raspberrypi/setup.sh
```

The wizard prepares Raspberry Pi OS Lite with your account, SSH key, hostname,
location, keyboard, display settings, and optional Wi-Fi. Boot the Pi and check
that you can reach it over SSH.

## 2. Configure the running Pi

Follow the [post-setup guide](post-setup/README.md) to prepare your private
inventory and build the Ansible container. Preview its changes with:

```bash
./setup/workers/raspberrypi/post-setup/run.sh --check --diff
```

Run the same command without `--check --diff` to apply. Post-setup currently
keeps fans at full speed across reboots. It supports one or several Pis and
reboots changed machines one at a time.

## Reference

- [Flashing options and development checks](flash/README.md)
- [Post-setup, Docker/Podman, and restoring automatic cooling](post-setup/README.md)
- [HDMI and Wi-Fi investigation](docs/hdmi-wifi.md)
