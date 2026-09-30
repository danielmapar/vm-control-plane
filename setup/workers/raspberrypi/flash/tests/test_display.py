"""Display customization must preserve boot arguments and never touch the cache."""

import json
import lzma
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import display_setup as display


class DisplayTests(unittest.TestCase):
    def test_picker_default_automatic_and_invalid_input(self):
        with patch("builtins.input", return_value=""):
            self.assertEqual(display.choose_display(None), "1080p")
        with patch("builtins.input", side_effect=["4k", "2"]):
            self.assertEqual(display.choose_display(None), "auto")
        with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
            self.assertEqual(display.choose_display(None, dry_run=True), "1080p")
            self.assertEqual(display.choose_display("auto"), "auto")

    def test_boot_settings_preserve_root_and_other_connectors_and_are_idempotent(self):
        original = (
            "console=serial0,115200 console=tty1 root=PARTUUID=1234-02 rootwait "
            "video=HDMI-A-1:3840x2160@60 video=DSI-1:800x480@60 "
            'video=HDMI-A-2:3440x1440@100 cfg="two words"\n'
        )
        updated = display.display_cmdline(original)
        self.assertEqual(
            updated,
            "console=serial0,115200 console=tty1 root=PARTUUID=1234-02 rootwait "
            'video=DSI-1:800x480@60 cfg="two words" ' + display.DISPLAY_ARGUMENTS + "\n",
        )
        self.assertEqual(display.display_cmdline(updated), updated)
        self.assertEqual(updated.count("\n"), 1)
        for malformed in ("", "\n", "root=x\nconsole=tty1", "root=x\x00"):
            with self.subTest(malformed=malformed), self.assertRaises(ValueError):
                display.display_cmdline(malformed)

    def test_auto_uses_original_without_tools_or_temporary_copy(self):
        source = Path("/not-accessed.img.xz")
        with (
            patch.object(
                display.subprocess, "run", side_effect=AssertionError("unexpected command")
            ),
            patch.object(
                display.tempfile,
                "TemporaryDirectory",
                side_effect=AssertionError("unexpected copy"),
            ),
        ):
            with display.display_image(source, "auto") as result:
                self.assertEqual(result, source)

    def test_layout_rejects_missing_duplicate_or_out_of_bounds_boot_partition(self):
        table = {
            "label": "dos",
            "unit": "sectors",
            "sectorsize": 512,
            "partitions": [{"type": "c", "start": 1, "size": 8}],
        }
        invalid = [
            dict(table, label="gpt"),
            dict(table, sectorsize=4096),
            dict(table, partitions=[]),
            dict(table, partitions=table["partitions"] * 2),
            dict(table, partitions=[{"type": "c", "start": 1, "size": 100}]),
        ]
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "image.img"
            image.write_bytes(bytes(5120))
            for candidate in [table, *invalid]:
                with patch.object(
                    display.subprocess,
                    "run",
                    return_value=subprocess.CompletedProcess(
                        [], 0, json.dumps({"partitiontable": candidate})
                    ),
                ):
                    if candidate is table:
                        self.assertEqual(display.boot_offset(image), 512)
                    else:
                        with self.assertRaises(ValueError):
                            display.boot_offset(image)

    def test_temporary_copy_is_removed_on_success_and_failures(self):
        original = b"root=PARTUUID=test-02 console=tty1 rootwait\n"
        expected = display.display_cmdline(original.decode()).encode()
        for failure in (None, "mcopy", "verify", "imager"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                source = Path(directory) / "stock.img.xz"
                source.write_bytes(lzma.compress(b"original OS contents"))
                compressed = source.read_bytes()
                reads = 0

                def command(args, **kwargs):
                    nonlocal reads
                    if args[0] == "mtype":
                        reads += 1
                        return subprocess.CompletedProcess(
                            args, 0, original if reads == 1 or failure == "verify" else expected
                        )
                    self.assertEqual(args[0], "mcopy")
                    self.assertEqual(Path(args[-2]).read_bytes(), expected)
                    if failure == "mcopy":
                        raise subprocess.CalledProcessError(1, args)
                    return subprocess.CompletedProcess(args, 0)

                def prepare():
                    with display.display_image(source, "1080p") as image:
                        self.assertNotEqual(image, source)
                        self.assertEqual(image.read_bytes(), b"original OS contents")
                        if failure == "imager":
                            raise subprocess.CalledProcessError(1, ["imager"])

                with (
                    patch.object(display, "check_display_tools"),
                    patch.object(display, "boot_offset", return_value=512),
                    patch.object(display.subprocess, "run", side_effect=command),
                ):
                    if failure:
                        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                            prepare()
                    else:
                        prepare()
                self.assertEqual(source.read_bytes(), compressed)
                self.assertEqual(list(Path(directory).iterdir()), [source])


if __name__ == "__main__":
    unittest.main()
