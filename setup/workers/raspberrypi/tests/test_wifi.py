"""Picker, credentials, and generated networking tests; no real credentials or media."""

from contextlib import redirect_stdout
import io
import json
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import prepare
import wifi_setup as wifi


class WifiTests(unittest.TestCase):
    def args(self, *flags):
        return prepare.parser().parse_args(
            ["--hostname", "pi-test", "--timezone", "Etc/UTC", *flags]
        )

    def test_scan_preserves_escaped_names_and_deduplicates(self):
        rows = wifi.parse_scan(
            " Space\\:Lab\\\\5 :WPA2:30\n Space\\:Lab\\\\5 :WPA2:80\n:WPA2:99\n"
            "Open:--:40\nSecure:WPA3:70\nOffice:WPA2 802.1X:60\n"
        )
        self.assertEqual(len(rows), 4)
        self.assertEqual(rows[0]["ssid"], " Space:Lab\\5 ")
        self.assertEqual(rows[0]["signal"], 80)
        self.assertEqual(rows[1]["auth"], "wpa3")
        self.assertIsNone(rows[2]["auth"])
        self.assertEqual(rows[3]["auth"], "open")

    def test_scan_failure_retains_manual_and_skip_choices(self):
        with (
            patch.object(wifi.shutil, "which", return_value="nmcli"),
            patch.object(
                wifi.subprocess, "run", side_effect=subprocess.TimeoutExpired("nmcli", 20)
            ),
            patch("builtins.input", return_value="0"),
            redirect_stdout(io.StringIO()) as output,
        ):
            self.assertIsNone(wifi.choose_network())
        self.assertIn("Enter an SSID manually", output.getvalue())
        self.assertIn("hidden SSID", output.getvalue())

    def test_skip_never_requests_wifi_password(self):
        with (
            patch.object(wifi, "scan_networks", return_value=[]),
            patch("builtins.input", return_value="0"),
            patch.object(
                wifi.ui.getpass, "getpass", side_effect=AssertionError("password requested")
            ),
            redirect_stdout(io.StringIO()),
        ):
            self.assertIsNone(wifi.configure_wifi(self.args()))
        with patch.object(wifi, "scan_networks", side_effect=AssertionError("scan requested")):
            self.assertIsNone(wifi.configure_wifi(self.args("--no-wifi")))

    def test_rescan_and_visible_network_selection(self):
        row = {"ssid": "Lab", "security": "WPA2 WPA3", "signal": 80, "auth": "wpa2"}
        with (
            patch.object(wifi, "scan_networks", side_effect=[[], [row]]) as scan,
            patch("builtins.input", side_effect=["r", "1", ""]),
            redirect_stdout(io.StringIO()),
        ):
            chosen = wifi.choose_network()
        self.assertEqual(scan.call_count, 2)
        self.assertEqual(chosen["ssid"], "Lab")
        self.assertFalse(chosen["hidden"])

    def test_listed_hidden_network_choice_reaches_image_configuration(self):
        row = {"ssid": "PROTHEUS", "security": "WPA3", "signal": 80, "auth": "wpa3"}
        with (
            patch.object(wifi, "scan_networks", return_value=[row]),
            patch("builtins.input", side_effect=["1", "unsure", "YES"]),
            patch.object(wifi, "wifi_secret", return_value="test-wifi-secret"),
            redirect_stdout(io.StringIO()) as output,
        ):
            args = self.args("--country", "US")
            wlan = wifi.configure_wifi(args)
            _, network = prepare.payload(args, "ssh-ed25519 EXAMPLE", None, wlan)
        ap = network["network"]["wifis"]["wlan0"]["access-points"]["PROTHEUS"]
        self.assertTrue(ap["hidden"])
        self.assertEqual(ap["auth"]["key-management"], "sae")
        self.assertIn("Enter yes or no", output.getvalue())
        self.assertNotIn("test-wifi-secret", output.getvalue())

    def test_listed_network_can_explicitly_be_non_hidden(self):
        row = {"ssid": "Lab", "security": "WPA2", "signal": 70, "auth": "wpa2"}
        with (
            patch.object(wifi, "scan_networks", return_value=[row]),
            patch("builtins.input", side_effect=["1", "No"]),
            redirect_stdout(io.StringIO()),
        ):
            self.assertFalse(wifi.choose_network()["hidden"])

    def test_hidden_network_uses_correct_psk_without_printing_password(self):
        with (
            patch.object(wifi, "scan_networks", return_value=[]),
            patch("builtins.input", side_effect=["h", "IEEE", "", "us"]),
            patch.object(wifi.ui.getpass, "getpass", side_effect=["password", "password"]),
            redirect_stdout(io.StringIO()) as output,
        ):
            config = wifi.configure_wifi(self.args())
        ap = config["access-points"]["IEEE"]
        self.assertTrue(ap["hidden"])
        self.assertEqual(config["regulatory-domain"], "US")
        # Known WPA-PSK PBKDF2 test vector (passphrase password, SSID IEEE).
        self.assertEqual(
            ap["auth"]["password"],
            "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e",
        )
        self.assertNotIn('"password": "password"', json.dumps(config))
        self.assertNotIn("password", output.getvalue())

    def test_wpa3_uses_sae_and_keeps_secret_out_of_output(self):
        secret = "Example SAE passphrase"
        args = self.args(
            "--wifi-ssid", "Other router", "--wifi-auth", "wpa3", "--wifi-country", "BR"
        )
        with (
            patch.object(wifi, "scan_networks", side_effect=AssertionError("scan requested")),
            patch.object(wifi.ui.getpass, "getpass", side_effect=[secret, secret]),
            redirect_stdout(io.StringIO()) as output,
        ):
            config = wifi.configure_wifi(args)
        self.assertEqual(
            config["access-points"]["Other router"]["auth"],
            {"key-management": "sae", "password": secret},
        )
        self.assertNotIn(secret, output.getvalue())

    def test_open_network_never_requests_password(self):
        args = self.args("--wifi-ssid", "Open", "--wifi-auth", "open", "--wifi-country", "US")
        with (
            patch.object(
                wifi.ui.getpass, "getpass", side_effect=AssertionError("password requested")
            ),
            redirect_stdout(io.StringIO()),
        ):
            config = wifi.configure_wifi(args)
        self.assertNotIn("auth", config["access-points"]["Open"])

    def test_password_mismatch_and_bad_length_retry(self):
        with (
            patch.object(
                wifi.ui.getpass,
                "getpass",
                side_effect=["short", "valid-pass", "mismatch", "password", "password"],
            ),
            redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(
                wifi.wifi_secret("IEEE", "wpa2"),
                "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e",
            )

    def test_dry_run_never_scans_or_prompts(self):
        args = self.args(
            "--dry-run", "--wifi-ssid", "Preview: Lab", "--wifi-hidden", "--wifi-country", "US"
        )
        with (
            patch.object(wifi, "scan_networks", side_effect=AssertionError("scan requested")),
            patch("builtins.input", side_effect=AssertionError("input requested")),
            patch.object(
                wifi.ui.getpass, "getpass", side_effect=AssertionError("password requested")
            ),
            redirect_stdout(io.StringIO()),
        ):
            wlan = wifi.configure_wifi(args)
        args.user = "lab"
        user, network = prepare.payload(args, "ssh-ed25519 EXAMPLE", None, wlan)
        ap = network["network"]["wifis"]["wlan0"]["access-points"]["Preview: Lab"]
        self.assertEqual(ap["auth"]["password"], "PREVIEW-NOT-A-REAL-PASSWORD")
        self.assertTrue(ap["hidden"])
        self.assertEqual(user["runcmd"], [["systemctl", "enable", "--now", "ssh"]])
        self.assertFalse(user["ssh_pwauth"])
        self.assertNotIn("write_files", user)
        self.assertNotIn("access-points", json.dumps(user))

    def test_invalid_options_and_ssid_are_rejected(self):
        for flags in [
            ("--no-wifi", "--wifi-auth", "wpa2"),
            ("--wifi-hidden",),
            ("--wifi-ssid", ""),
            ("--wifi-country", "ZZ"),
            ("--wifi-ssid", "x" * 33),
        ]:
            with self.subTest(flags=flags), self.assertRaises(ValueError):
                wifi.validate_options(self.args(*flags))
        for ssid in ("a\nb", "a\x1bb", "☃" * 11):
            with self.subTest(ssid=ssid), self.assertRaises(ValueError):
                wifi.validate_ssid(ssid)
