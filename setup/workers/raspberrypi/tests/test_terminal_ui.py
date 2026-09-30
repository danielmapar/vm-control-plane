"""Terminal fallbacks, untrusted labels, and narrow-screen readability."""

from contextlib import redirect_stdout
import io
import os
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import terminal_ui as ui


class UTF8Terminal(io.StringIO):
    encoding = "utf-8"

    def isatty(self):
        return True


class TerminalTests(unittest.TestCase):
    def test_no_color_redirected_and_dumb_terminals(self):
        with (
            patch.object(ui, "PLAIN", False),
            patch.dict(os.environ, {"TERM": "xterm-256color"}, clear=True),
        ):
            with redirect_stdout(UTF8Terminal()) as stream:
                self.assertTrue(ui.color())
                ui.note("Modern", "accent")
                self.assertIn("\x1b[", stream.getvalue())
                with patch.dict(os.environ, {"NO_COLOR": ""}):
                    self.assertFalse(ui.color())
            with redirect_stdout(io.StringIO()):
                self.assertFalse(ui.color())
            with redirect_stdout(UTF8Terminal()), patch.dict(os.environ, {"TERM": "dumb"}):
                self.assertFalse(ui.color())
                self.assertFalse(ui.unicode())

    def test_plain_output_and_prompts_never_contain_escapes(self):
        with patch.object(ui, "PLAIN", True), redirect_stdout(UTF8Terminal()) as stream:
            ui.banner()
            ui.step(2, "Name and keyboard")
            ui.panel("Review", [("Wi-Fi", "lab\x1b[2J\nsecret\u202e")])
            self.assertNotIn("\x1b", stream.getvalue())
            self.assertNotIn("╭", stream.getvalue())
            self.assertIn(r"\u001b", stream.getvalue())
            self.assertIn(r"\u202e", stream.getvalue())
            self.assertNotIn("\x1b", ui.prompt("Pi name"))

    def test_panels_fit_narrow_terminals_without_truncating_identity(self):
        fingerprint = "SHA256:" + "Z" * 43
        for columns in (32, 48, 80):
            with (
                patch.object(ui, "PLAIN", False),
                patch.dict(os.environ, {"NO_COLOR": "1"}),
                patch.object(
                    ui.shutil, "get_terminal_size", return_value=os.terminal_size((columns, 24))
                ),
                redirect_stdout(UTF8Terminal()) as stream,
            ):
                ui.panel(
                    "Review your Pi", [("SSH key", fingerprint), ("Wi-Fi", "Example 日本語 Wi-Fi")]
                )
                output = stream.getvalue()
                self.assertTrue(all(ui.cells(line) <= columns for line in output.splitlines()))
                self.assertEqual(output.count("Z"), 43)

    def test_cancel_is_not_swallowed_and_spaces_are_preserved(self):
        with patch("builtins.input", return_value=" spaced SSID "):
            self.assertEqual(ui.ask("SSID"), " spaced SSID ")
        with (
            patch("builtins.input", side_effect=KeyboardInterrupt),
            self.assertRaises(KeyboardInterrupt),
        ):
            ui.ask("Device")

    def test_long_prompt_remains_identifiable_without_generic_choice_label(self):
        with (
            patch.object(ui, "PLAIN", True),
            patch.object(ui.shutil, "get_terminal_size", return_value=os.terminal_size((32, 24))),
            redirect_stdout(UTF8Terminal()) as stream,
        ):
            prompt = ui.prompt("Type /dev/mmcblk0 to confirm")
            self.assertIn("Type /dev/mmcblk0", stream.getvalue())
            self.assertEqual(prompt, "  > ")
            self.assertNotIn("Choice", stream.getvalue())


if __name__ == "__main__":
    unittest.main()
