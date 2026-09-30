# Flash Raspberry Pi OS

Flash Raspberry Pi OS Lite (64-bit) with your hostname, account, SSH key,
location, keyboard, display mode, and optional Wi-Fi. All setup scripts run on
**your Linux workstation**. The Pi uses the OS's built-in configuration;
no project service or worker packages are installed.

## Install the workstation tools

Use Python 3.11+ and Raspberry Pi Imager 2.x with cloud-init CLI support.
On Fedora:

```bash
sudo dnf install -y rpi-imager python3 openssl openssh-clients \
  util-linux procps-ng NetworkManager mtools
```

Use a writable microSD card or removable USB drive of at least 8 GB. Allow about
3.1 GB of extra workstation disk space when selecting 1080p. The verified OS
image is cached in `~/.cache/vm-control-plane/raspberrypi/` (or `$XDG_CACHE_HOME`).
The pinned image is Raspberry Pi OS Lite Trixie arm64, dated **2026-09-15**.

## Prepare the card

Insert the media and run this from the repository root as your normal user:

```bash
./setup/workers/raspberrypi/setup.sh
```

Follow the six steps:

1. **Media:** select the device by size, model, and serial. Use `r` to refresh.
2. **Name and location:** choose a unique hostname, country, timezone city,
   keyboard layout, and display mode. Defaults include `pi-worker` and 1080p/60 Hz.
3. **Account:** choose a username (default `lab`) and a password for console
   login and sudo.
4. **SSH key:** select an existing key, enter a key path, or create an Ed25519 key.
5. **Wi-Fi:** select a nearby network, enter an SSID manually, choose a hidden
   network with `h`, or press Enter to skip Wi-Fi and use Ethernet.
6. **Review and write:** check the settings, then type the exact device path to
   confirm erasing it. Enter your workstation's sudo password when requested.

**All data on the selected media is erased.** Setup checks the image checksum,
rechecks the device identity, and lets Imager write, verify, and eject it.
Internal disks, partitions, and media backing your system or working directory
are refused. Wait for **Card written and verified** before removing the media.

Menus use ordinary line input. Press Enter to accept a displayed default.
Ctrl+C cancels before writing. Use `--plain` for a simple text interface.
The keyboard list accepts numbers, layout codes, or searches such as `brazil`;
`n`/`p` changes pages and `?` shows all layouts. In the timezone prompt, `?`
lists that country's choices.

## Boot and connect

Move the card to the Pi, connect power, and allow a few minutes for first boot.
Connect your workstation to the same LAN. Use the SSH command printed by setup:

```bash
ssh -4 -o IdentitiesOnly=yes -i ~/.ssh/id_ed25519_lab_workers lab@pi-worker.local
```

Replace the key, username, and hostname with your choices. If `.local` does not
resolve, use the Pi's IP address from your router's client list.

After an intentional reflash, the Pi has new SSH host keys. Verify it is your
Pi before removing its old entry with `ssh-keygen -R pi-worker.local`, using
its actual hostname or IP. Setup does not edit your known-hosts file.

## Post-setup

After first boot, use the optional [Ansible post-setup](../post-setup/README.md) to
keep worker fans at full speed across reboots.

## Choosing settings

| Setting | What to choose |
| --- | --- |
| Hostname | A unique network name, such as `pi-worker-1`. This also names the Pi. |
| Country and timezone | Where the Pi will operate. Country controls Wi-Fi channels; timezone controls its clock. |
| Keyboard | The layout on the Pi's physical keyboard. SSH uses your workstation's layout. |
| Display | **1080p/60 Hz** for a compatible monitor; **Automatic** for its preferred mode. Headless use also works. |
| SSH key | Use an existing private key or `.pub` file, or create a new pair. Only the public key goes onto the Pi. |
| Wi-Fi | Choose WPA2 Personal for WPA2/WPA3 mixed networks. Mark hidden SSIDs as hidden even if the laptop lists them. |

The suggested SSH key is `~/.ssh/id_ed25519_lab_workers` when available.
Selected private keys are checked against their public files. New keys never
replace existing files; choose a passphrase at the local `ssh-keygen` prompt.
The account password and SSH key passphrase are separate. SSH password login
is disabled. To unlock a key in your local agent, run `ssh-add /path/to/key`.

Passwords are entered privately. The Pi account password is hashed, and Wi-Fi
credentials go into its boot configuration. Temporary workstation configuration
files are private and removed after success or failure. A newly created SSH key
remains on your workstation if you cancel flashing.

Non-Latin keyboard choices include English for typing login credentials;
Alt+Shift switches layouts. Keyboard selection does not change the OS language.
If Wi-Fi is skipped, setup preserves the stock networking configuration.
Enterprise Wi-Fi and captive-portal login are not supported.

## Optional flags and preview

Flags skip individual prompts. For example:

```bash
./setup/workers/raspberrypi/setup.sh \
  --hostname pi-worker-1 --user lab \
  --country US --timezone America/Los_Angeles \
  --keyboard us --display 1080p \
  --ssh-key ~/.ssh/id_ed25519_lab_workers
```

Add `--no-wifi` for Ethernet, or `--wifi-ssid 'Your network' --wifi-auth wpa2`
with `--wifi-hidden` for a hidden SSID. Passwords are always prompted locally.
Run `./setup/workers/raspberrypi/setup.sh --help` for all options.

Preview settings without accessing media, downloading, or requesting passwords:

```bash
./setup/workers/raspberrypi/setup.sh --dry-run \
  --ssh-key ~/.ssh/id_ed25519_lab_workers.pub \
  --output /tmp/pi-preview
```

The output directory must be new. Preview defaults are `pi-worker`, `lab`,
`Etc/UTC`, a country-based keyboard suggestion (US by default), and 1080p/60 Hz.
Wi-Fi previews also require `--wifi-ssid` and `--country`. Preview passwords are
placeholders; these files are for inspection only. `display-arguments.txt`
contains additions to `cmdline.txt`, not a complete replacement.

## Known issues and tested behavior

- **WPA3-only networks:** the pinned stock image has a known SAE compatibility
  issue. Use a WPA2-compatible network or Ethernet. Setup warns when WPA3 is
  selected and does not install a beta Wi-Fi package. See the
  [Raspberry Pi maintainer's report](https://forums.raspberrypi.com/viewtopic.php?p=2388522).
- **Wi-Fi with HDMI:** our Pi 5 repeatedly failed hidden-network discovery at
  3440×1440/100 Hz. 1080p/60 Hz passed fresh flashes, reconnection tests, and two
  successive warm reboots. One earlier restart failure remains unexplained.
  See [hardware test results](../docs/hdmi-wifi.md).
- **Cloud-init warning:** setup omits the pinned image's stale reference to the
  missing `netplan_nm_patch` module. A fresh flash completed with no cloud-init
  errors or recoverable warnings and no failed services. Other OS diagnostics
  remain enabled. See the [upstream issue](https://github.com/raspberrypi/trixie-feedback/issues/45).

1080p is set for both HDMI ports by adding native `video=` arguments to a
verified image copy before flashing. Unplugged ports remain inactive. To restore
automatic display selection later, remove these two arguments from the existing
single line in `/boot/firmware/cmdline.txt`, preserving all other arguments:

```text
video=HDMI-A-1:1920x1080@60 video=HDMI-A-2:1920x1080@60
```

Reboot after editing. Reflash an older project image to get this minimal setup;
updating the repository alone does not remove software already on a Pi.

## Development

```bash
bash -n setup/workers/raspberrypi/setup.sh
python3 -B -m unittest discover -s setup/workers/raspberrypi/flash/tests -q
```

The tests simulate flashing and never write media. Install `python3-pyyaml` on
Fedora to include the cloud-init YAML compatibility test; otherwise it is skipped.
The flasher itself uses only Python's standard library.

`prepare.py` coordinates the wizard, validation, download, and flashing.
The `wifi_setup`, `ssh_keys`, `keyboard_setup`, and `display_setup` modules handle
their named settings; `terminal_ui.py` handles presentation.

When updating `IMAGE_NAME` and `IMAGE_SHA256` in `prepare.py`, compare
`PI_FINAL_MODULES` with the new image's `/etc/cloud/cloud.cfg`. Preserve every
working module and its order, and remove the override when the vendor fixes the
missing reference. Until updated or removed, this override also takes precedence
over final-stage module changes delivered by OS package updates.

Implementation references: [Imager CLI](https://github.com/raspberrypi/rpi-imager/blob/v2.0.6/src/cli.cpp),
[cloud-init module settings](https://docs.cloud-init.io/en/25.2/reference/base_config_reference.html#module-keys),
[Netplan Wi-Fi](https://netplan.readthedocs.io/en/stable/netplan-yaml/),
[Pi display settings](https://www.raspberrypi.com/documentation/computers/configuration.html#configure-display-settings).
