"""Optional workstation Wi-Fi picker and Pi Netplan configuration (stdlib only)."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import terminal_ui as ui


def validate_ssid(ssid):
    if not 1 <= len(ssid.encode("utf-8")) <= 32 or any(ord(c) < 32 or ord(c) == 127 for c in ssid):
        raise ValueError("SSID must contain 1–32 UTF-8 bytes without control characters.")
    return ssid


def country_code(value):
    value = value.strip().upper()
    if not re.fullmatch(r"[A-Z]{2}", value):
        raise ValueError("Enter a two-letter country code, such as US, BR, or GB.")
    table = Path("/usr/share/zoneinfo/iso3166.tab")
    if table.exists():
        codes = {
            line.split()[0]
            for line in table.read_text().splitlines()
            if line and not line.startswith("#")
        }
        if value not in codes:
            raise ValueError("Unknown country code; use the country where the Pi will operate.")
    return value


def parse_scan(output):
    """nmcli terse output escapes colons and backslashes; keep SSID whitespace."""
    found = {}
    for line in output.splitlines():
        fields, field, escaped = [], "", False
        for char in line:
            if escaped:
                field += char
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == ":":
                fields.append(field)
                field = ""
            else:
                field += char
        fields.append(field)
        if escaped or len(fields) != 3:
            continue
        ssid, security, signal = fields
        try:
            validate_ssid(ssid)
            signal = int(signal)
            if not 0 <= signal <= 100:
                continue
        except ValueError:
            continue  # Nameless hidden APs cannot supply a selectable SSID.
        security = security.upper()
        if "802.1X" in security or "EAP" in security or "WEP" in security:
            auth = None
        elif "WPA2" in security:
            auth = "wpa2"
        elif "WPA3" in security:
            auth = "wpa3"
        elif security in ("", "--"):
            auth = "open"
        else:
            auth = None
        key = (ssid, security)
        if key not in found or signal > found[key]["signal"]:
            found[key] = {
                "ssid": ssid,
                "security": security or "open",
                "signal": signal,
                "auth": auth,
            }
    return sorted(found.values(), key=lambda n: (-n["signal"], n["ssid"]))


def scan_networks():
    if not shutil.which("nmcli"):
        ui.note("Wi-Fi scanning needs nmcli (NetworkManager). You can still enter an SSID or skip.")
        return []
    try:
        result = subprocess.run(
            [
                "nmcli",
                "--colors",
                "no",
                "-t",
                "-e",
                "yes",
                "-f",
                "SSID,SECURITY,SIGNAL",
                "device",
                "wifi",
                "list",
                "--rescan",
                "yes",
            ],
            capture_output=True,
            text=True,
            check=True,
            timeout=20,
            env=dict(os.environ, LC_ALL="C"),
        )
    except (OSError, subprocess.SubprocessError):
        ui.warning("Wi-Fi scan unavailable. Enter the SSID manually, rescan, or skip.")
        return []
    return parse_scan(result.stdout)


def ask_valid(prompt, validator):
    while True:
        try:
            return validator(ui.ask(prompt.removesuffix(": ")))
        except ValueError as error:
            ui.warning(error)


def ask_hidden():
    ui.note("A listed network can still have a hidden SSID.")
    while True:
        answer = ui.ask("Hidden SSID? (yes/no)", "no").strip().lower()
        if answer in ("y", "yes"):
            return True
        if answer in ("n", "no"):
            return False
        ui.warning("Enter yes or no.")


def choose_network():
    ui.note("Finding nearby Wi-Fi networks...")
    networks = scan_networks()
    while True:
        ui.note("NETWORKS FOUND BY THIS LAPTOP", "bold")
        ui.option("0", "Skip Wi-Fi / use Ethernet")
        for index, network in enumerate(networks, 1):
            support = "" if network["auth"] else "; unsupported authentication"
            ui.option(
                index,
                json.dumps(network["ssid"], ensure_ascii=False),
                f"Signal {network['signal']}% / {network['security']}{support}",
            )
        ui.option("m", "Enter an SSID manually", "Use this for a network at another location.")
        ui.option("h", "Enter a hidden SSID")
        ui.option("r", "Rescan networks")
        choice = ui.ask("Network", "0").strip().lower()
        if choice in ("", "0"):
            return None
        if choice == "r":
            ui.note("Scanning again...")
            networks = scan_networks()
            continue
        if choice in ("m", "h"):
            if choice == "h":
                ui.note("Hidden networks do not broadcast their names; enter the exact SSID.")
            return {
                "ssid": ask_valid("SSID: ", validate_ssid),
                "hidden": choice == "h",
                "auth": None,
            }
        if choice.isdecimal() and 1 <= int(choice) <= len(networks):
            selected = networks[int(choice) - 1]
            if selected["auth"] is None:
                ui.warning(
                    "This setup supports WPA2/WPA3 Personal and open networks; choose another network or skip."
                )
                continue
            # NetworkManager may already know a hidden SSID. Appearing in its
            # scan results does not establish that the AP broadcasts its name.
            return dict(selected, hidden=ask_hidden())
        ui.warning("Choose one of the listed numbers, m, h, or r.")


def wifi_secret(ssid, auth):
    while True:
        first = ui.secret("Wi-Fi password")
        if auth == "wpa2":
            valid = bool(re.fullmatch(r"[0-9a-fA-F]{64}", first)) or (
                8 <= len(first) <= 63 and all(32 <= ord(c) <= 126 for c in first)
            )
            explanation = (
                "WPA2 requires 8–63 printable ASCII characters, or a 64-digit hexadecimal PSK."
            )
        else:
            valid = 1 <= len(first.encode("utf-8")) <= 63 and not any(
                ord(c) < 32 or ord(c) == 127 for c in first
            )
            explanation = "Use a WPA3 password of 1–63 UTF-8 bytes without control characters."
        if not valid:
            ui.warning(explanation)
            continue
        if first != ui.secret("Confirm Wi-Fi password"):
            ui.warning("Wi-Fi passwords did not match.")
            continue
        if auth == "wpa2":
            # Netplan/NM accept a raw PSK, so the original passphrase need not go on the card.
            if len(first) == 64:
                return first.lower()
            return hashlib.pbkdf2_hmac(
                "sha1", first.encode("ascii"), ssid.encode("utf-8"), 4096, 32
            ).hex()
        return first  # SAE requires the password itself, not a WPA2-derived PSK.


def validate_options(args):
    if args.no_wifi and (args.wifi_auth or args.wifi_hidden):
        raise ValueError("--no-wifi cannot be combined with Wi-Fi settings.")
    if args.wifi_hidden and args.wifi_ssid is None:
        raise ValueError("--wifi-hidden requires --wifi-ssid.")
    if args.wifi_ssid is not None:
        validate_ssid(args.wifi_ssid)
    if args.wifi_country:
        country_code(args.wifi_country)
    if args.dry_run and not args.wifi_ssid and args.wifi_auth:
        raise ValueError("For a Wi-Fi preview, supply --wifi-ssid and --country.")


def configure_wifi(args):
    if args.no_wifi or (args.dry_run and args.wifi_ssid is None):
        return None
    if args.wifi_ssid is not None:
        selected = {"ssid": args.wifi_ssid, "hidden": args.wifi_hidden, "auth": None}
    else:
        selected = choose_network()
    if selected is None:
        return None
    auth = args.wifi_auth or selected["auth"]
    if auth is None and not args.dry_run:
        while auth not in ("wpa2", "wpa3", "open"):
            ui.note("Security: wpa2 (including mixed WPA2/WPA3), wpa3, or open.")
            auth = ui.ask("Security", "wpa2").strip().lower() or "wpa2"
    auth = auth or "wpa2"
    if auth == "wpa3":
        ui.warning(
            "WPA3 note: this stock Pi OS image has a known Wi-Fi compatibility issue. "
            "If it cannot connect, use Ethernet or a WPA2 Personal network; see the README."
        )
    if args.dry_run and not args.wifi_country:
        raise ValueError("Wi-Fi previews require --wifi-country; previews never prompt.")
    country = (
        country_code(args.wifi_country)
        if args.wifi_country
        else ask_valid("Country where the Pi will operate (e.g. US, BR, GB): ", country_code)
    )
    ap = {"hidden": selected["hidden"]}
    if auth != "open":
        secret = (
            "PREVIEW-NOT-A-REAL-PASSWORD" if args.dry_run else wifi_secret(selected["ssid"], auth)
        )
        ap["auth"] = {"key-management": "psk" if auth == "wpa2" else "sae", "password": secret}
    ui.success(
        f"Wi-Fi: {json.dumps(selected['ssid'], ensure_ascii=False)} ({auth}, {country}"
        f"{', hidden' if selected['hidden'] else ''})."
    )
    return {
        "dhcp4": True,
        "optional": True,
        "regulatory-domain": country,
        "access-points": {selected["ssid"]: ap},
    }
