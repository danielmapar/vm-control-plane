"""Offline regression checks: these tests must never flash a device."""

import copy
from contextlib import contextmanager, ExitStack
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from dataclasses import replace

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
spec = importlib.util.spec_from_file_location("prepare", ROOT / "prepare.py")
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)
import ssh_keys

try:
    import yaml
except ImportError:
    yaml = None


class PrepareTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.key = Path(cls.temp.name) / "test_key"
        subprocess.run(
            ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(cls.key)], check=True
        )

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def args(self, *more):
        return prepare.parser().parse_args(
            [
                "--hostname",
                "pi-worker-2",
                "--user",
                "lab",
                "--timezone",
                "Etc/UTC",
                "--country",
                "US",
                "--keyboard",
                "us",
                "--ssh-key",
                str(self.key) + ".pub",
                *more,
            ]
        )

    @contextmanager
    def flash_context(self, *, changed=False, confirmation="/dev/sdz", imager_fails=False):
        """Exercise main's destructive branch with every host operation intercepted."""
        ssh_identity = replace(
            ssh_keys.read_public(Path(str(self.key) + ".pub")), private_key=self.key
        )
        calls, credentials = [], []

        @contextmanager
        def fake_display_image(source, mode):
            self.flashed_display = mode
            yield source if mode == "auto" else Path("/mock/raspios-1080p.img")

        def fake_run(*command, **kwargs):
            calls.append(command)
            if command[:2] == ("sudo", "/mock/bin/rpi-imager"):
                userdata = Path(command[command.index("--cloudinit-userdata") + 1])
                self.assertEqual(userdata.stat().st_mode & 0o777, 0o600)
                parsed = json.loads(userdata.read_text().removeprefix("#cloud-config\n"))
                self.flashed_user = parsed
                self.assertEqual(parsed["users"][0]["passwd"], "$6$salt$testhash")
                credentials.append(userdata)
                self.flashed_network = None
                if "--cloudinit-networkconfig" in command:
                    network = Path(command[command.index("--cloudinit-networkconfig") + 1])
                    self.assertTrue(network.is_file())
                    self.assertEqual(network.stat().st_mode & 0o777, 0o600)
                    self.flashed_network = json.loads(network.read_text())
                    credentials.append(network)
                else:
                    self.assertFalse((userdata.parent / "network-config").exists())
                if imager_fails:
                    raise subprocess.CalledProcessError(1, command)
            return subprocess.CompletedProcess(
                command, 0, "--cloudinit-userdata --cloudinit-networkconfig"
            )

        identity = ("/dev/sdz", "8:240", 128_000_000_000, "test", "reader", None)
        after = (*identity[:-2], "changed-reader", None) if changed else identity
        with ExitStack() as stack:
            stack.enter_context(patch.object(prepare, "choose_ssh_key", return_value=ssh_identity))
            stack.enter_context(patch.object(prepare, "run", side_effect=fake_run))
            # The sole direct subprocess call is the pgrep check. No processes run.
            stack.enter_context(
                patch.object(
                    prepare.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)
                )
            )
            stack.enter_context(patch.object(prepare.sys.stdin, "isatty", return_value=True))
            stack.enter_context(patch.object(prepare.os, "geteuid", return_value=1000))
            stack.enter_context(
                patch.object(prepare.shutil, "which", side_effect=lambda name: f"/mock/bin/{name}")
            )
            stack.enter_context(
                patch.object(prepare, "password_hash", return_value="$6$salt$testhash")
            )
            stack.enter_context(
                patch.object(prepare, "image", return_value=Path("/mock/image.img.xz"))
            )
            stack.enter_context(
                patch.object(prepare, "display_image", side_effect=fake_display_image)
            )
            stack.enter_context(
                patch.object(
                    prepare,
                    "device_state",
                    side_effect=[("/dev/sdz", identity), ("/dev/sdz", after)],
                )
            )
            stack.enter_context(patch("builtins.input", return_value=confirmation))
            yield calls, credentials

    def flash(self, *flags):
        prepare.main(
            [
                "--hostname",
                "pi-worker-2",
                "--user",
                "lab",
                "--timezone",
                "Etc/UTC",
                "--country",
                "US",
                "--keyboard",
                "us",
                "--display",
                "auto",
                "--ssh-key",
                str(self.key) + ".pub",
                "--device",
                "/dev/sdz",
                *(flags or ["--no-wifi"]),
            ]
        )

    def test_hostname_prompt_uses_suggestion_or_retries_invalid_name(self):
        with patch("builtins.input", return_value=""):
            self.assertEqual(prepare.choose_hostname(None), "pi-worker")
        with patch("builtins.input", side_effect=["bad hostname", "lab-pi-03"]):
            self.assertEqual(prepare.choose_hostname(None), "lab-pi-03")

    def test_explicit_hostname_and_preview_do_not_prompt(self):
        with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
            self.assertEqual(prepare.choose_hostname("lab-pi-03"), "lab-pi-03")
            self.assertEqual(prepare.choose_hostname(None, dry_run=True), "pi-worker")

    def test_username_prompt_default_validation_and_explicit_value(self):
        with patch("builtins.input", return_value=""):
            self.assertEqual(prepare.choose_username(None), "lab")
        with patch("builtins.input", side_effect=["root", "bad user", "daniel"]):
            self.assertEqual(prepare.choose_username(None), "daniel")
        with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
            self.assertEqual(prepare.choose_username("daniel"), "daniel")
            self.assertEqual(prepare.choose_username(None, dry_run=True), "lab")

    def test_chosen_hostname_reaches_boot_configuration(self):
        with (
            self.flash_context(),
            patch(
                "builtins.input",
                side_effect=[
                    "lab-pi-03",
                    "US",
                    "America/Los_Angeles",
                    "us:intl",
                    "2",
                    "daniel",
                    "/dev/sdz",
                ],
            ),
        ):
            prepare.main(["--device", "/dev/sdz", "--no-wifi", "--ssh-key", str(self.key) + ".pub"])
        self.assertEqual(self.flashed_user["hostname"], "lab-pi-03")
        self.assertEqual(
            self.flashed_user["keyboard"],
            {"layout": "us", "variant": "intl", "model": "pc105", "options": ""},
        )
        self.assertEqual(self.flashed_user["users"][0]["name"], "daniel")
        self.assertEqual(self.flashed_user["timezone"], "America/Los_Angeles")

    def test_timezone_city_default_ambiguous_and_invalid_choices(self):
        with (
            patch.object(prepare, "local_timezone", return_value="Europe/London"),
            patch("builtins.input", return_value=""),
        ):
            self.assertEqual(prepare.choose_timezone(None), "Europe/London")
        with patch("builtins.input", side_effect=["missing city", "Los Angeles"]):
            self.assertEqual(prepare.choose_timezone(None), "America/Los_Angeles")
        with (
            patch.object(
                prepare,
                "available_timezones",
                return_value={"America/Indiana/Indianapolis", "America/Indianapolis"},
            ),
            patch("builtins.input", side_effect=["Indianapolis", "2"]),
        ):
            self.assertEqual(prepare.choose_timezone(None), "America/Indianapolis")
        with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
            self.assertEqual(prepare.choose_timezone("Europe/London"), "Europe/London")
            self.assertEqual(prepare.choose_timezone(None, dry_run=True), "Etc/UTC")
            with self.assertRaises(ValueError):
                prepare.choose_timezone("Not/AZone")

    def test_wifi_flash_passes_private_config_files_without_secret_arguments(self):
        import wifi_setup

        secret = "test-WPA3-only-password"
        with (
            self.flash_context() as (calls, credentials),
            patch.object(wifi_setup.ui.getpass, "getpass", side_effect=[secret, secret]),
        ):
            self.flash(
                "--wifi-ssid",
                "Hidden router",
                "--wifi-hidden",
                "--wifi-auth",
                "wpa3",
                "--wifi-country",
                "US",
            )
        wlan = self.flashed_network["network"]["wifis"]["wlan0"]
        self.assertTrue(wlan["access-points"]["Hidden router"]["hidden"])
        self.assertEqual(wlan["access-points"]["Hidden router"]["auth"]["password"], secret)
        self.assertNotIn(secret, repr(calls))
        self.assertTrue(all(not path.exists() for path in credentials))

    def test_flash_keeps_verification_and_removes_temporary_credentials(self):
        with self.flash_context() as (calls, credentials):
            self.flash()
        writes = [command for command in calls if command[:2] == ("sudo", "/mock/bin/rpi-imager")]
        self.assertEqual(len(writes), 1)
        self.assertEqual(writes[0][-2:], ("/mock/image.img.xz", "/dev/sdz"))
        for unsafe in ("--disable-verify", "--disable-eject", "--enable-writing-system-drives"):
            self.assertNotIn(unsafe, writes[0])
        self.assertTrue(credentials)
        self.assertTrue(all(not path.exists() for path in credentials))

    def test_changed_disk_is_not_flashed(self):
        with self.flash_context(changed=True) as (calls, _):
            with self.assertRaisesRegex(ValueError, "disk changed"):
                self.flash()
        self.assertFalse(any(command[:2] == ("sudo", "/mock/bin/rpi-imager") for command in calls))

    def test_1080p_flash_uses_prepared_image_with_imager_verification(self):
        with self.flash_context() as (calls, _):
            self.flash("--no-wifi", "--display", "1080p")
        writes = [command for command in calls if command[:2] == ("sudo", "/mock/bin/rpi-imager")]
        self.assertEqual(len(writes), 1)
        self.assertEqual(writes[0][-2:], ("/mock/raspios-1080p.img", "/dev/sdz"))
        self.assertNotIn("--disable-verify", writes[0])
        self.assertNotIn("--disable-eject", writes[0])

    def test_display_preparation_failure_never_reaches_sudo_or_flashing(self):
        with (
            self.flash_context() as (calls, _),
            patch.object(
                prepare,
                "display_image",
                side_effect=ValueError("Display configuration read-back failed"),
            ),
        ):
            with self.assertRaisesRegex(ValueError, "read-back"):
                self.flash("--no-wifi", "--display", "1080p")
        self.assertFalse(any(command[0] == "sudo" for command in calls))

    def test_wrong_confirmation_is_not_flashed(self):
        with self.flash_context(confirmation="yes") as (calls, _):
            with self.assertRaisesRegex(ValueError, "Cancelled"):
                self.flash()
        self.assertFalse(any(command[0] == "sudo" for command in calls))

    def test_imager_started_during_prompts_is_not_overlapped(self):
        with (
            self.flash_context() as (calls, _),
            patch.object(
                prepare.subprocess,
                "run",
                side_effect=[
                    subprocess.CompletedProcess([], 1),
                    subprocess.CompletedProcess([], 0),
                ],
            ),
        ):
            with self.assertRaisesRegex(ValueError, "started during setup"):
                self.flash()
        self.assertFalse(any(command[:2] == ("sudo", "/mock/bin/rpi-imager") for command in calls))

    def test_country_selection_and_single_timezone_need_no_extra_city_prompt(self):
        with patch("builtins.input", side_effect=["ZZ", "gb"]):
            self.assertEqual(prepare.choose_country(None), "GB")
        with patch("builtins.input", side_effect=AssertionError("unneeded prompt")):
            self.assertEqual(prepare.choose_country("br"), "BR")
            self.assertEqual(prepare.choose_timezone(None, country="GB"), "Europe/London")
        with (
            patch.object(
                prepare, "country_zones", return_value=["America/New_York", "America/Los_Angeles"]
            ),
            patch("builtins.input", side_effect=["?", "2"]),
        ):
            self.assertEqual(prepare.choose_timezone(None, country="US"), "America/New_York")

    def test_builtin_sd_and_undersized_nominal_cards_keep_disk_protections(self):
        sd = {
            "path": "/dev/mmcblk0",
            "type": "disk",
            "rm": False,
            "ro": False,
            "size": 7_800_000_000,
            "mountpoints": [],
        }
        home, cwd = Path("/home/test"), Path("/tmp")
        with patch.object(Path, "read_text", return_value="SD\n"):
            self.assertTrue(prepare.is_sd_card(sd))
            prepare.inspect_device(sd, home, cwd)
            with self.assertRaises(ValueError):
                prepare.inspect_device(dict(sd, children=[{"mountpoints": ["/"]}]), home, cwd)
        with patch.object(Path, "read_text", return_value="MMC\n"), self.assertRaises(ValueError):
            prepare.inspect_device(sd, home, cwd)

    def test_imager_failure_removes_temporary_credentials(self):
        with self.flash_context(imager_fails=True) as (_, credentials):
            with self.assertRaises(subprocess.CalledProcessError):
                self.flash()
        self.assertTrue(credentials)
        self.assertTrue(all(not path.exists() for path in credentials))

    def test_dry_run_cannot_download_or_access_a_device(self):
        output = Path(self.temp.name) / "preview"
        with (
            patch.object(prepare, "device_state", side_effect=AssertionError("device accessed")),
            patch.object(prepare, "image", side_effect=AssertionError("download started")),
            patch.object(
                prepare, "password_hash", side_effect=AssertionError("password requested")
            ),
        ):
            with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
                prepare.main(
                    ["--ssh-key", str(self.key) + ".pub", "--dry-run", "--output", str(output)]
                )
        user = json.loads((output / "user-data").read_text().removeprefix("#cloud-config\n"))
        self.assertTrue(user["users"][0]["lock_passwd"])
        self.assertNotIn("passwd", user["users"][0])
        self.assertEqual((output / "user-data").stat().st_mode & 0o777, 0o600)
        self.assertEqual(user["hostname"], "pi-worker")
        self.assertEqual(user["users"][0]["name"], "lab")
        self.assertEqual({p.name for p in output.iterdir()}, {"user-data", "display-arguments.txt"})
        self.assertEqual(
            (output / "display-arguments.txt").read_text(), prepare.DISPLAY_ARGUMENTS + "\n"
        )

    def test_automatic_display_preview_has_no_boot_arguments(self):
        output = Path(self.temp.name) / "preview-auto"
        with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
            prepare.main(
                [
                    "--ssh-key",
                    str(self.key) + ".pub",
                    "--dry-run",
                    "--display",
                    "auto",
                    "--output",
                    str(output),
                ]
            )
        self.assertEqual({p.name for p in output.iterdir()}, {"user-data"})

    def test_real_payload_uses_hash_and_only_public_key(self):
        args = self.args()
        key = ssh_keys.read_public(args.ssh_key).public_key
        user, network = prepare.payload(args, key, "$6$salt$testhash")
        account = user["users"][0]
        self.assertFalse(account["lock_passwd"])
        self.assertEqual(account["passwd"], "$6$salt$testhash")
        self.assertEqual(
            account["ssh_authorized_keys"], [Path(str(self.key) + ".pub").read_text().strip()]
        )
        self.assertNotIn("OPENSSH PRIVATE KEY", json.dumps(user))
        self.assertFalse(user["ssh_pwauth"])
        self.assertEqual(user["runcmd"], [["systemctl", "enable", "--now", "ssh"]])
        self.assertIsNone(network)

    def test_image_contains_only_standard_account_ssh_and_network_settings(self):
        user, network = prepare.payload(
            self.args(),
            ssh_keys.read_public(Path(str(self.key) + ".pub")).public_key,
            "$6$salt$testhash",
        )
        # This allowlist prevents custom boot services, packages, scripts, firewall,
        # and project files from returning to the image through cloud-init.
        self.assertEqual(
            set(user),
            {
                "hostname",
                "manage_etc_hosts",
                "timezone",
                "keyboard",
                "ssh_pwauth",
                "disable_root",
                "users",
                "runcmd",
                "cloud_final_modules",
            },
        )
        self.assertNotIn("enable_ssh", user)
        self.assertEqual(user["runcmd"], [["systemctl", "enable", "--now", "ssh"]])
        self.assertEqual(user["timezone"], "Etc/UTC")
        # Avoid the missing-module warning while keeping the SSH command runnable.
        self.assertNotIn("netplan_nm_patch", user["cloud_final_modules"])
        self.assertIn("scripts_user", user["cloud_final_modules"])
        self.assertEqual(
            set(user["users"][0]),
            {"name", "groups", "shell", "lock_passwd", "sudo", "ssh_authorized_keys", "passwd"},
        )
        self.assertIsNone(network)

    @unittest.skipIf(yaml is None, "Install PyYAML for the cloud-init YAML compatibility check")
    def test_unicode_credentials_survive_the_yaml_reader_used_by_cloud_init(self):
        ssid = "Lab 🌐\u2028\u0085"
        secret = "test-🔐-pass\u2029word"
        wifi = {
            "dhcp4": True,
            "regulatory-domain": "US",
            "access-points": {ssid: {"auth": {"key-management": "sae", "password": secret}}},
        }
        user, network = prepare.payload(
            self.args(), "ssh-ed25519 EXAMPLE 🔑", "$6$salt$testhash", wifi
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            prepare.write_payload(directory, user, network)
            for name, expected in (("user-data", user), ("network-config", network)):
                text = (directory / name).read_text(encoding="utf-8")
                parsed = yaml.safe_load(text)
                self.assertEqual(parsed, expected)
                json.dumps(parsed, ensure_ascii=False).encode("utf-8")

    def test_password_passed_to_openssl_on_stdin(self):
        with (
            patch.object(prepare.ui.getpass, "getpass", side_effect=["test-secret", "test-secret"]),
            patch.object(prepare, "run") as command,
        ):
            command.return_value.stdout = "$6$salt$hash\n"
            self.assertEqual(prepare.password_hash(), "$6$salt$hash")
        self.assertNotIn("test-secret", command.call_args.args)
        self.assertEqual(command.call_args.kwargs["input"], "test-secret\n")

    def test_unsafe_names_are_rejected(self):
        for validator, values in (
            (prepare.validate_hostname, ("bad;hostname", "bad hostname", "-start", "x" * 64)),
            (prepare.validate_username, ("root", "nobody", "bad user", "9start", "x" * 32)),
        ):
            for value in values:
                with self.subTest(value=value), self.assertRaises(ValueError):
                    validator(value)

    def test_device_picker_refreshes_and_requires_explicit_selection(self):
        disk = {"path": "/dev/sdz", "size": 128_000_000_000, "model": "USB", "serial": "test"}
        selected = ("/dev/sdz", ("identity",))
        with (
            patch.object(
                prepare, "removable_devices", side_effect=[[], [disk], [disk]]
            ) as discover,
            patch.object(prepare, "device_state", return_value=selected) as state,
            patch("builtins.input", side_effect=["r", "", "1"]),
        ):
            self.assertEqual(prepare.choose_device(), selected)
        self.assertEqual(discover.call_count, 3)
        state.assert_called_once_with("/dev/sdz")

    def test_device_picker_cancel_never_inspects_or_writes_selected_media(self):
        with (
            patch.object(prepare, "removable_devices", return_value=[]),
            patch.object(prepare, "device_state", side_effect=AssertionError("device selected")),
            patch("builtins.input", return_value="q"),
            self.assertRaises(KeyboardInterrupt),
        ):
            prepare.choose_device()

    def test_discovery_excludes_internal_mounted_readonly_and_empty_disks(self):
        removable = {
            "path": "/dev/sdz",
            "type": "disk",
            "rm": True,
            "ro": False,
            "size": 128_000_000_000,
        }
        candidates = [
            removable,
            dict(removable, path="/dev/nvme0n1", rm=False),
            dict(removable, path="/dev/sda", size=0),
            dict(removable, path="/dev/sdb", ro=True),
            dict(removable, path="/dev/sdc", children=[{"mountpoints": ["/"]}]),
        ]
        with patch.object(
            prepare,
            "run",
            return_value=subprocess.CompletedProcess(
                [], 0, json.dumps({"blockdevices": candidates})
            ),
        ):
            self.assertEqual(prepare.removable_devices(), [removable])

    def test_no_argument_wizard_carries_all_choices_to_image(self):
        disk = {"path": "/dev/sdz", "size": 128_000_000_000, "model": "USB"}
        import wifi_setup

        with (
            self.flash_context(),
            patch.object(prepare, "removable_devices", return_value=[disk]),
            patch.object(wifi_setup, "scan_networks", return_value=[]),
            patch(
                "builtins.input",
                side_effect=["1", "my-pi", "GB", "", "", "daniel", "0", "/dev/sdz"],
            ),
        ):
            prepare.main([])
        self.assertEqual(self.flashed_user["hostname"], "my-pi")
        self.assertEqual(self.flashed_user["keyboard"]["layout"], "gb")
        self.assertEqual(self.flashed_user["users"][0]["name"], "daniel")
        self.assertEqual(self.flashed_display, "1080p")
        self.assertIsNone(self.flashed_network)

    def test_device_state_requests_tree_to_include_partition_mounts(self):
        disk = {
            "path": "/dev/sdz",
            "type": "disk",
            "rm": True,
            "ro": False,
            "size": 128_000_000_000,
            "children": [{"mountpoints": ["/"]}],
        }

        def lsblk(*command, **kwargs):
            self.assertIn("--tree", command)
            return subprocess.CompletedProcess(command, 0, json.dumps({"blockdevices": [disk]}))

        with (
            patch.object(Path, "resolve", return_value=Path("/dev/sdz")),
            patch.object(Path, "is_block_device", return_value=True),
            patch.object(prepare, "run", side_effect=lsblk),
        ):
            with self.assertRaisesRegex(ValueError, "system filesystem"):
                prepare.device_state("/dev/sdz")

    def test_disk_selection(self):
        valid = {
            "path": "/dev/sdb",
            "type": "disk",
            "rm": True,
            "ro": False,
            "size": 128_000_000_000,
            "maj:min": "8:16",
            "serial": "test-reader",
            "children": [{"mountpoints": ["/run/media/test/bootfs"]}],
        }
        home, cwd = Path("/home/test"), Path("/home/test/Workspace")
        original = prepare.inspect_device(valid, home, cwd)
        changes = [
            {"type": "part"},
            {"rm": False},
            {"ro": True},
            {"size": 0},
            {"children": [{"children": [{"mountpoints": ["/"]}]}]},
            {"children": [{"mountpoints": ["/home"]}]},
            {"children": [{"mountpoints": ["/var/log"]}]},
            {"children": [{"mountpoints": ["/etc"]}]},
            {"children": [{"mountpoints": ["/home/another-user"]}]},
            {"children": [{"mountpoints": ["/home/test/Workspace"]}]},
            {"children": [{"mountpoints": ["[SWAP]"]}]},
        ]
        for change in changes:
            with self.subTest(change=change), self.assertRaises(ValueError):
                prepare.inspect_device(dict(valid, **change), home, cwd)
        swapped = copy.deepcopy(valid)
        swapped["serial"] = "different-reader"
        self.assertNotEqual(original, prepare.inspect_device(swapped, home, cwd))

    def test_corrupt_cache_is_not_used_or_downloaded_over(self):
        with tempfile.TemporaryDirectory() as directory:
            cache = Path(directory)
            cached = cache / prepare.IMAGE_NAME
            cached.write_bytes(b"not-an-image")
            with patch.object(
                prepare.urllib.request, "urlopen", side_effect=AssertionError("unexpected network")
            ):
                with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                    prepare.image(cache)
            self.assertEqual(cached.read_bytes(), b"not-an-image")

    def test_download_stream_is_verified_before_becoming_cached_image(self):
        data = b"test image data" * 100_000
        expected = prepare.hashlib.sha256(data).hexdigest()
        for checksum, succeeds in ((expected, True), ("0" * 64, False)):
            with self.subTest(succeeds=succeeds), tempfile.TemporaryDirectory() as directory:
                source = io.BytesIO(data)
                source.headers = {"Content-Length": str(len(data))}
                with (
                    patch.object(prepare.urllib.request, "urlopen", return_value=source),
                    patch.object(prepare, "IMAGE_SHA256", checksum),
                    patch.object(prepare.ui, "progress") as progress,
                ):
                    if succeeds:
                        result = prepare.image(Path(directory))
                        self.assertEqual(result.read_bytes(), data)
                        progress.assert_called_with(len(data), len(data))
                    else:
                        with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                            prepare.image(Path(directory))
                        self.assertFalse(list(Path(directory).iterdir()))

    def test_preview_refuses_overwriting_a_directory(self):
        with tempfile.TemporaryDirectory() as existing:
            marker = Path(existing) / "keep"
            marker.write_text("existing data")
            with self.assertRaises(FileExistsError):
                prepare.main(
                    [
                        "--hostname",
                        "pi-worker-2",
                        "--user",
                        "lab",
                        "--ssh-key",
                        str(self.key) + ".pub",
                        "--dry-run",
                        "--output",
                        existing,
                    ]
                )
            self.assertEqual(marker.read_text(), "existing data")


if __name__ == "__main__":
    unittest.main()
