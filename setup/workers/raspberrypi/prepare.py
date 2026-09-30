#!/usr/bin/env python3
"""Flash stock Raspberry Pi OS with a hostname, account, SSH key, and optional Wi-Fi."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from zoneinfo import TZPATH, ZoneInfo, ZoneInfoNotFoundError, available_timezones

from wifi_setup import configure_wifi, country_code, validate_options
from ssh_keys import choose_ssh_key
from keyboard_setup import choose_keyboard, keyboard_config, keyboard_label, validate_keyboard
from display_setup import (
    DISPLAY_ARGUMENTS,
    DISPLAY_LABELS,
    check_display_tools,
    choose_display,
    display_image,
)
import terminal_ui as ui

IMAGE_NAME = "2026-09-15-raspios-trixie-arm64-lite.img.xz"
IMAGE_URL = (
    "https://downloads.raspberrypi.com/raspios_lite_arm64/images/"
    "raspios_lite_arm64-2026-09-15/" + IMAGE_NAME
)
# Compressed image checksum from the vendor's adjacent .img.xz.sha256 file.
IMAGE_SHA256 = "cdf4f3bfac35ae947b46e4e767f935453810549779ac3290e05a6754aee627e5"
LSBLK_COLUMNS = "PATH,TYPE,SIZE,RM,RO,MAJ:MIN,MODEL,SERIAL,WWN,MOUNTPOINTS"
PI_GROUPS = (
    "users,adm,dialout,audio,netdev,video,plugdev,cdrom,games,input,gpio,spi,i2c,render,sudo".split(
        ","
    )
)
# Final-stage list from the pinned image's /etc/cloud/cloud.cfg, omitting only
# the nonexistent netplan_nm_patch module (raspberrypi/trixie-feedback#45).
# Recheck this list when updating IMAGE_NAME: preserve the vendor's module order.
PI_FINAL_MODULES = (
    "package_update_upgrade_install",
    "write_files_deferred",
    "puppet",
    "chef",
    "ansible",
    "mcollective",
    "salt_minion",
    "reset_rmc",
    "scripts_vendor",
    "scripts_per_once",
    "scripts_per_boot",
    "scripts_per_instance",
    "scripts_user",
    "ssh_authkey_fingerprints",
    "keys_to_console",
    "install_hotplug",
    "phone_home",
    "final_message",
    "power_state_change",
)


def fail(message):
    raise ValueError(message)


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def sha256(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def parser():
    p = argparse.ArgumentParser(prog="setup.sh", description=__doc__)
    p.add_argument("--device", help="USB/SD device, e.g. /dev/sdb; otherwise choose from a list")
    p.add_argument("--hostname", help="Pi network name; suggested: pi-worker")
    p.add_argument("--user", "--username", dest="user", help="Pi login account; suggested: lab")
    p.add_argument("--timezone", help="Timezone, e.g. America/Los_Angeles; otherwise choose a city")
    p.add_argument(
        "--keyboard", help="Console layout, e.g. us, gb, br, us:intl; otherwise use the picker"
    )
    p.add_argument(
        "--display", choices=("1080p", "auto"), help="HDMI mode; suggested: 1080p at 60 Hz"
    )
    p.add_argument("--plain", action="store_true", help="Plain text without colors or box drawing")
    p.add_argument(
        "--ssh-key", type=Path, help="Private key or .pub path; otherwise select or create a key"
    )
    wifi = p.add_mutually_exclusive_group()
    wifi.add_argument(
        "--no-wifi", action="store_true", help="Skip the optional Wi-Fi picker and use Ethernet"
    )
    wifi.add_argument(
        "--wifi-ssid",
        help="Enter an SSID directly instead of scanning; password is prompted locally",
    )
    p.add_argument(
        "--wifi-hidden", action="store_true", help="The SSID supplied with --wifi-ssid is hidden"
    )
    p.add_argument(
        "--country",
        "--wifi-country",
        dest="wifi_country",
        help="Two-letter Pi country code; otherwise prompted",
    )
    p.add_argument(
        "--wifi-auth",
        choices=("wpa2", "wpa3", "open"),
        help="Wi-Fi security (otherwise detected or prompted)",
    )
    p.add_argument(
        "--dry-run",
        action="store_true",
        help="Preview only; no passwords, downloads, sudo, or media access",
    )
    p.add_argument(
        "--output", type=Path, help="New directory for dry-run files (required with --dry-run)"
    )
    p.add_argument(
        "--cache-dir",
        type=Path,
        default=Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache"))
        / "vm-control-plane/raspberrypi",
    )
    return p


def validate_hostname(value):
    if not isinstance(value, str) or not re.fullmatch(
        r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", value
    ):
        fail(
            "Invalid hostname: use lowercase letters, digits, and internal hyphens; at most 63 characters."
        )
    return value


def choose_hostname(value, dry_run=False):
    if value is not None:
        return validate_hostname(value)
    if dry_run:
        return "pi-worker"
    ui.note("Choose a unique name for this Raspberry Pi on your network.")
    while True:
        try:
            return validate_hostname(ui.ask("Hostname", "pi-worker").strip() or "pi-worker")
        except ValueError as error:
            ui.warning(error)


def validate_username(value):
    if (
        not isinstance(value, str)
        or not re.fullmatch(r"[a-z_][a-z0-9_-]{0,30}", value)
        or value in {"root", "nobody"}
    ):
        fail(
            "Invalid username: use lowercase letters, digits, underscores, or hyphens; start with a letter or underscore. Root and nobody are reserved."
        )
    return value


def choose_username(value, dry_run=False):
    if value is not None:
        return validate_username(value)
    if dry_run:
        return "lab"
    while True:
        try:
            return validate_username(ui.ask("Username", "lab").strip() or "lab")
        except ValueError as error:
            ui.warning(error)


def validate_timezone(value):
    try:
        ZoneInfo(value)
    except (ZoneInfoNotFoundError, ValueError):
        fail(
            "Unknown timezone. Use a timezone such as America/Los_Angeles, Europe/London, or Etc/UTC."
        )
    return value


def local_timezone():
    local = Path("/etc/localtime").resolve()
    for directory in TZPATH:
        try:
            return validate_timezone(str(local.relative_to(Path(directory).resolve())))
        except ValueError:
            continue
    return "Etc/UTC"


def country_zones(country):
    table = Path("/usr/share/zoneinfo/zone.tab")
    if not table.is_file():
        return []
    return sorted(
        {
            fields[2]
            for line in table.read_text().splitlines()
            if line
            and not line.startswith("#")
            and len(fields := line.split()) >= 3
            and fields[0] == country
        }
    )


def choose_country(value):
    if value:
        return country_code(value)
    default = ""
    table = Path("/usr/share/zoneinfo/zone.tab")
    if table.is_file():
        zone = local_timezone()
        for line in table.read_text().splitlines():
            fields = line.split()
            if len(fields) >= 3 and not line.startswith("#") and fields[2] == zone:
                default = fields[0]
                break
    ui.note("Where will the Pi live? US = United States, GB = Britain, BR = Brazil.")
    while True:
        try:
            return country_code(ui.ask("Country code", default).strip() or default)
        except ValueError as error:
            ui.warning(error)


def choose_timezone(value, dry_run=False, country=None):
    if value is not None:
        return validate_timezone(value)
    if dry_run:
        return "Etc/UTC"
    zones = set(country_zones(country)) if country else set()
    if len(zones) == 1:
        return validate_timezone(next(iter(zones)))
    default = local_timezone()
    if zones and default not in zones:
        default = ""
    zones = zones or available_timezones()
    ui.note("Enter a timezone city (e.g. Los Angeles), a full timezone, or ? to list choices.")
    matches = []
    while True:
        entered = ui.ask("City/timezone", default).strip() or default
        if entered == "?" or not entered:
            matches = sorted(zones)
            for index, zone in enumerate(matches, 1):
                ui.option(index, zone)
            continue
        if entered.isdecimal() and 1 <= int(entered) <= len(matches):
            return matches[int(entered) - 1]
        if entered in zones:
            return validate_timezone(entered)
        city = entered.replace("_", " ").casefold()
        matches = sorted(
            z for z in zones if z.rsplit("/", 1)[-1].replace("_", " ").casefold() == city
        )
        if len(matches) == 1:
            return validate_timezone(matches[0])
        if matches:
            for index, zone in enumerate(matches, 1):
                ui.option(index, zone)
            ui.note("Choose a number or enter the full timezone.")
        else:
            ui.warning(
                "City not found. Use a nearby timezone city, or its full name such as America/Los_Angeles."
            )


def is_sd_card(info):
    try:
        name = Path(info["path"]).name
        return (Path("/sys/class/block") / name / "device/type").read_text().strip() == "SD"
    except (KeyError, OSError):
        return False


def inspect_device(info, home, cwd):
    """Reject internal disks, partitions, and removable disks backing this session."""
    if info.get("type") != "disk" or not (info.get("rm") or is_sd_card(info)):
        fail(
            "Target must be a removable whole disk or SD card. Internal disks and partitions are refused."
        )
    if info.get("ro"):
        fail("This device is read-only; check the card's write-protect switch.")
    if int(info.get("size") or 0) < 7_500_000_000:
        fail("Use an 8 GB or larger card for Raspberry Pi OS Lite.")

    def inspect(node):
        for mount in node.get("mountpoints") or []:
            if not mount:
                continue
            if mount in {"[SWAP]", "/"} or any(
                Path(mount).is_relative_to(root)
                for root in ("/home", "/boot", "/etc", "/usr", "/var", "/opt")
            ):
                fail("Target contains a mounted system filesystem or swap.")
            if home.is_relative_to(Path(mount)) or cwd.is_relative_to(Path(mount)):
                fail("Target backs your home or working directory.")
        for child in node.get("children", []):
            inspect(child)

    inspect(info)
    return tuple(info.get(k) for k in ("path", "maj:min", "size", "model", "serial", "wwn"))


def device_state(device):
    path = Path(device).resolve(strict=True)
    if not path.is_block_device():
        fail("--device must point to a connected block device.")
    result = run(
        "lsblk",
        "--tree",
        "--json",
        "--bytes",
        "--paths",
        "--output",
        LSBLK_COLUMNS,
        str(path),
        capture_output=True,
    )
    nodes = json.loads(result.stdout)["blockdevices"]
    if len(nodes) != 1 or nodes[0]["path"] != str(path):
        fail("Could not identify a single whole disk.")
    return str(path), inspect_device(nodes[0], Path.home().resolve(), Path.cwd().resolve())


def removable_devices():
    result = run(
        "lsblk",
        "--tree",
        "--json",
        "--bytes",
        "--paths",
        "--output",
        LSBLK_COLUMNS,
        capture_output=True,
    )
    candidates = []
    for info in json.loads(result.stdout)["blockdevices"]:
        if info.get("type") != "disk" or not (info.get("rm") or is_sd_card(info)):
            continue
        try:
            inspect_device(info, Path.home().resolve(), Path.cwd().resolve())
        except ValueError as error:
            reason = "No media inserted" if not info.get("size") else str(error)
            ui.warning(f"Unavailable: {info['path']} - {reason}")
            continue
        candidates.append(info)
    return candidates


def choose_device(explicit=None):
    if explicit:
        return device_state(explicit)
    while True:
        devices = removable_devices()
        if not devices:
            ui.note("No eligible USB/SD media found. Insert writable media of 8 GB or larger.")
        for index, info in enumerate(devices, 1):
            label = f"{info['path']} / {int(info['size']) / 1_000_000_000:.1f} GB / {(info.get('model') or 'Removable media').strip()}"
            ui.option(index, label, f"Serial: {info['serial']}" if info.get("serial") else None)
        ui.option("r", "Refresh devices")
        ui.option("q", "Cancel setup")
        choice = ui.ask("Device").strip().lower()
        if choice == "q":
            raise KeyboardInterrupt
        if choice == "r":
            continue
        if choice.isdecimal() and 1 <= int(choice) <= len(devices):
            return device_state(devices[int(choice) - 1]["path"])
        ui.warning("Choose a listed number, r, or q.")


def password_hash():
    while True:
        first = ui.secret("New Pi account password")
        if not first:
            ui.warning("Choose a non-empty password.")
            continue
        if first == ui.secret("Confirm password"):
            return run(
                "openssl", "passwd", "-6", "-stdin", input=first + "\n", capture_output=True
            ).stdout.strip()
        ui.warning("Passwords did not match.")


def payload(args, key, hashed, wifi=None):
    """Use only the stock image's account, SSH, and network customization."""
    account = {
        "name": args.user,
        "groups": PI_GROUPS,
        "shell": "/bin/bash",
        "lock_passwd": hashed is None,
        "sudo": "ALL=(ALL) ALL",
        "ssh_authorized_keys": [key],
    }
    if hashed:
        account["passwd"] = hashed
    userdata = {
        "hostname": args.hostname,
        "manage_etc_hosts": True,
        "timezone": args.timezone,
        "keyboard": keyboard_config(args.keyboard or "us"),
        "ssh_pwauth": False,
        "disable_root": True,
        "users": [account],
        "cloud_final_modules": list(PI_FINAL_MODULES),
        # The old enable_ssh key is ignored. Match Raspberry Pi Imager's fix:
        # enable the existing SSH daemon; no project service is installed.
        "runcmd": [["systemctl", "enable", "--now", "ssh"]],
    }
    if wifi is None:
        return userdata, None  # Keep the stock image's Ethernet/network settings.
    network = {
        "network": {
            "version": 2,
            "renderer": "NetworkManager",
            "ethernets": {"eth0": {"dhcp4": True, "optional": True}},
            "wifis": {"wlan0": wifi},
        }
    }
    return userdata, network


def write_payload(directory, userdata, network):
    for name, data, header in (
        ("user-data", userdata, "#cloud-config\n"),
        ("network-config", network, ""),
    ):
        if data is None:
            continue
        target = directory / name
        # Cloud-init reads YAML. JSON surrogate-pair escapes are not combined by
        # YAML readers, so keep non-ASCII characters in UTF-8. Escape YAML 1.1
        # line separators to preserve them inside SSIDs and WPA3 passphrases.
        encoded = json.dumps(data, indent=2, ensure_ascii=False)
        for separator in ("\u0085", "\u2028", "\u2029"):
            encoded = encoded.replace(separator, f"\\u{ord(separator):04x}")
        with target.open("x", encoding="utf-8") as stream:
            stream.write(header + encoded + "\n")
        target.chmod(0o600)


def image(cache):
    cache.mkdir(parents=True, exist_ok=True, mode=0o700)
    target = cache / IMAGE_NAME
    if target.exists():
        ui.note("Checking the cached OS image...")
        if sha256(target) != IMAGE_SHA256:
            fail(f"Cached image checksum mismatch; remove {target} and retry.")
        ui.success("Cached Raspberry Pi OS image verified.")
        return target.resolve()
    ui.note("Downloading Raspberry Pi OS Lite (64-bit)...")
    fd, temporary = tempfile.mkstemp(prefix="download-", suffix=".part", dir=cache)
    try:
        with os.fdopen(fd, "wb") as output, urllib.request.urlopen(IMAGE_URL, timeout=60) as source:
            total = int(source.headers.get("Content-Length", "0"))
            done = 0
            while chunk := source.read(1024 * 1024):
                output.write(chunk)
                done += len(chunk)
                ui.progress(done, total)
            if ui.color():
                print()
        ui.note("Verifying the OS image checksum...")
        if sha256(temporary) != IMAGE_SHA256:
            fail("Downloaded image checksum mismatch; no card was written.")
        os.replace(temporary, target)
    finally:
        Path(temporary).unlink(missing_ok=True)
    ui.success("OS image downloaded and verified.")
    return target.resolve()


def preview(args):
    """Render sample configuration without accessing media or requesting secrets."""
    args.hostname = choose_hostname(args.hostname, dry_run=True)
    args.user = choose_username(args.user, dry_run=True)
    args.timezone = choose_timezone(args.timezone, dry_run=True)
    args.keyboard = choose_keyboard(
        args.keyboard, country=(args.wifi_country or "US").upper(), dry_run=True
    )
    args.display = choose_display(args.display, dry_run=True)
    if args.output is None:
        fail("--dry-run requires --output pointing to a NEW preview directory.")
    identity = choose_ssh_key(args.ssh_key, args.hostname, dry_run=True)
    wifi = configure_wifi(args)
    args.output.mkdir(parents=True, exist_ok=False, mode=0o700)
    write_payload(args.output, *payload(args, identity.public_key, None, wifi))
    if args.display == "1080p":
        (args.output / "display-arguments.txt").write_text(
            DISPLAY_ARGUMENTS + "\n", encoding="ascii"
        )
    ui.success(f"Preview written to {args.output.resolve()}.")
    ui.note(f"Display: {DISPLAY_LABELS[args.display]}.")
    ui.note("No device accessed, password requested, or image downloaded.")
    ui.note("Preview only: the account password is locked and any Wi-Fi password is a placeholder.")


def check_requirements():
    """Check workstation tools before prompting; return the compatible Imager path."""
    for command in ("openssl", "ssh-keygen", "rpi-imager", "lsblk", "sudo", "pgrep"):
        if not shutil.which(command):
            fail(f"Missing command: {command}; see README prerequisites.")
    imager = str(Path(shutil.which("rpi-imager")).resolve())
    if subprocess.run(["pgrep", "-x", "rpi-imager"], stdout=subprocess.DEVNULL).returncode == 0:
        fail("Another Imager process is open. Finish its current write and close it first.")
    help_text = run(imager, "--cli", "--help", capture_output=True).stdout
    if "--cloudinit-userdata" not in help_text or "--cloudinit-networkconfig" not in help_text:
        fail("Install Raspberry Pi Imager 2.x with cloud-init CLI support.")
    return imager


def main(argv=None):
    args = parser().parse_args(argv)
    ui.PLAIN = args.plain
    os.umask(0o077)
    if not args.dry_run and (os.geteuid() == 0 or not sys.stdin.isatty()):
        fail(
            "Run as your normal user in an interactive terminal; this script invokes sudo when needed."
        )
    validate_options(args)
    if args.hostname is not None:
        validate_hostname(args.hostname)
    if args.user is not None:
        validate_username(args.user)
    if args.timezone is not None:
        validate_timezone(args.timezone)
    if args.keyboard is not None:
        args.keyboard = validate_keyboard(args.keyboard)
    if args.dry_run:
        preview(args)
        return
    if args.output is not None:
        fail("--output is only supported with --dry-run; real credential files are temporary.")

    imager = check_requirements()
    ui.banner()
    ui.step(1, "Choose your USB / SD card", "Select the removable media you want to prepare.")
    device, disk_identity = choose_device(args.device)
    ui.success(f"Selected {device} - {int(disk_identity[2]) / 1_000_000_000:.1f} GB")
    ui.step(2, "Make it yours", "Name, location, keyboard, and display.")
    args.hostname = choose_hostname(args.hostname)
    args.wifi_country = choose_country(args.wifi_country)
    args.timezone = choose_timezone(args.timezone, country=args.wifi_country)
    ui.success(f"Timezone: {args.timezone}")
    args.keyboard = choose_keyboard(args.keyboard, country=args.wifi_country)
    ui.success(f"Keyboard: {keyboard_label(args.keyboard)}")
    args.display = choose_display(args.display)
    check_display_tools(args.display)
    ui.success(f"Display: {DISPLAY_LABELS[args.display]}")
    ui.step(3, "Create your Pi account")
    args.user = choose_username(args.user)
    ui.note("This password is for console login and sudo. SSH uses your selected key.")
    ui.note("Passwords stay hidden as you type.")
    hashed = password_hash()
    if not hashed.startswith("$6$"):
        fail("Account password hashing failed.")
    ui.success(f"Account ready: {args.user}")
    ui.step(4, "Choose your SSH key", "Select an existing identity or create one on this laptop.")
    identity = choose_ssh_key(args.ssh_key, args.hostname)
    ui.step(5, "Connect to your network", "Wi-Fi is optional. You can use Ethernet instead.")
    wifi = configure_wifi(args)
    ui.step(6, "Review and write", "Check your settings before erasing the selected media.")
    review(args, device, disk_identity, identity, wifi)
    ui.warning(f"All data on {device} will be erased.")
    if ui.ask(f"Type {device} to confirm") != device:
        fail("Cancelled; no card was written.")
    downloaded = image(args.cache_dir.expanduser())
    with (
        display_image(downloaded, args.display) as prepared,
        tempfile.TemporaryDirectory(prefix="vmc-pi-config-") as directory,
    ):
        directory = Path(directory)
        write_payload(directory, *payload(args, identity.public_key, hashed, wifi))
        ui.note("Your laptop may ask for its administrator password to write the image.")
        run("sudo", "-v")
        if device_state(device)[1] != disk_identity:
            fail("The selected disk changed while preparing the image; refusing to write.")
        if subprocess.run(["pgrep", "-x", "rpi-imager"], stdout=subprocess.DEVNULL).returncode == 0:
            fail("Another Imager process started during setup. Close it before retrying.")
        command = ["sudo", imager, "--cli", "--cloudinit-userdata", str(directory / "user-data")]
        if wifi is not None:
            command += ["--cloudinit-networkconfig", str(directory / "network-config")]
        ui.note("Writing and verifying with Raspberry Pi Imager. Keep the media connected.", "bold")
        run(*command, str(prepared), device)
    print()
    ui.success(
        "Card written and verified. Move it into the Pi and power on "
        + ("within range of the configured Wi-Fi network." if wifi else "with Ethernet attached.")
    )
    ui.note("Connect your laptop to the same LAN to reach the Pi over SSH.")
    ui.note("Allow a few minutes for Raspberry Pi OS to complete its standard first boot.")
    ssh_command = ["ssh", "-4", "-o", "IdentitiesOnly=yes"]
    if identity.private_key:
        ssh_command += ["-i", str(identity.private_key)]
    else:
        ssh_command += ["-i", "/path/to/matching/private-key"]
    ssh_command.append(f"{args.user}@{args.hostname}.local")
    ui.note("CONNECT TO YOUR PI", "accent")
    # Keep the command on one line so it can be copied verbatim.
    print(ui.safe(shlex.join(ssh_command)))


def review(args, device, disk_identity, identity, wifi):
    rows = [
        ("OS", "Raspberry Pi OS Lite / 64-bit / Trixie"),
        ("Erase target", f"{device} / {int(disk_identity[2]) / 1_000_000_000:.1f} GB"),
        ("Model", str(disk_identity[3] or "Removable media").strip()),
        ("Serial", disk_identity[4] or "Not reported"),
        ("Hostname", f"{args.hostname}.local"),
        ("Account", args.user),
        ("Country", args.wifi_country),
        ("Timezone", args.timezone),
        ("Keyboard", keyboard_label(args.keyboard)),
        ("Display", DISPLAY_LABELS[args.display]),
        ("SSH key", identity.fingerprint),
    ]
    if wifi:
        ssid, ap = next(iter(wifi["access-points"].items()))
        auth = ap.get("auth", {}).get("key-management", "open")
        rows += [
            ("Wi-Fi", json.dumps(ssid, ensure_ascii=False)),
            ("Security", dict(psk="WPA2 Personal", sae="WPA3 Personal", open="Open")[auth]),
            ("Hidden SSID", "Yes" if ap.get("hidden") else "No"),
        ]
    else:
        rows.append(("Network", "Ethernet / Wi-Fi skipped"))
    ui.panel("Ready to prepare your Pi", rows)
    if wifi and auth == "sae":
        ui.warning(
            "WPA3-only Wi-Fi is affected by a known issue in this stock Pi OS image. "
            "Have Ethernet available, or choose a WPA2-compatible network."
        )


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        ui.warning(f"Setup stopped: {error}")
        sys.exit(1)
    except (KeyboardInterrupt, EOFError):
        print()
        ui.note("Setup cancelled.")
        sys.exit(130)
