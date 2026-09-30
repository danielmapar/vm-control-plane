"""Keyboard choices must be usable offline and survive native cloud-init output."""

from contextlib import redirect_stdout
import io
from pathlib import Path
import sys
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import keyboard_setup as keyboard
import prepare


class KeyboardTests(unittest.TestCase):
    def choose(self, answers, **kwargs):
        with patch("builtins.input", side_effect=answers), redirect_stdout(io.StringIO()):
            return keyboard.choose_keyboard(**kwargs)

    def test_country_suggestion_is_explicit_and_can_be_overridden(self):
        self.assertEqual(self.choose([""], country="BR"), "br")
        self.assertEqual(self.choose([""], country="GB"), "gb")
        self.assertEqual(self.choose(["us:intl"], country="BR"), "us:intl")
        self.assertEqual(self.choose([""], country="XX"), "us")

    def test_search_number_paging_and_no_match_recovery(self):
        self.assertEqual(self.choose(["/brazil", "1"]), "br")
        self.assertEqual(self.choose(["/colemak", "1"]), "us:colemak")
        self.assertEqual(self.choose(["n", "p", "/missing", "/all", "2"]), "gb")
        self.assertEqual(self.choose(["99", "not-a-layout", "us:dvorak"]), "us:dvorak")
        self.assertEqual(self.choose(["brazil", "1"]), "br")
        self.assertEqual(self.choose(["²", "?", "1"]), "us")
        self.assertEqual(self.choose(["1"], country="SE"), "se")

    def test_cancel_and_eof_are_not_defaults(self):
        with self.assertRaises(KeyboardInterrupt):
            self.choose(["q"])
        with self.assertRaises(EOFError):
            self.choose([EOFError()])

    def test_flags_and_preview_never_prompt(self):
        with patch("builtins.input", side_effect=AssertionError("unexpected prompt")):
            self.assertEqual(keyboard.choose_keyboard("br"), "br")
            self.assertEqual(keyboard.choose_keyboard(" BR "), "br")
            self.assertEqual(keyboard.choose_keyboard(country="GB", dry_run=True), "gb")
            with self.assertRaises(ValueError):
                keyboard.choose_keyboard("us\nmalicious")

    def test_native_keyboard_fields_and_generic_model(self):
        for choice in ("br", "us:intl", "gb"):
            args = prepare.parser().parse_args(["--keyboard", choice])
            args.hostname, args.user, args.timezone = "pi-test", "lab", "Etc/UTC"
            user, network = prepare.payload(args, "ssh-ed25519 EXAMPLE", "$6$test")
            config = user["keyboard"]
            self.assertEqual(config["layout"], choice.partition(":")[0])
            self.assertEqual(config["variant"], choice.partition(":")[2])
            self.assertEqual(config["model"], "pc105")
            self.assertEqual(user["runcmd"], [["systemctl", "enable", "--now", "ssh"]])
            self.assertIsNone(network)

    def test_non_latin_countries_default_to_latin_login_and_explicit_choices_keep_it(self):
        for country in ("RU", "UA", "BG", "RS", "GR", "IL", "IR", "IN", "TH", "VN", "NL"):
            self.assertEqual(keyboard.choose_keyboard(country=country, dry_run=True), "us")
        for layout in keyboard.NON_LATIN:
            config = keyboard.keyboard_config(layout)
            self.assertEqual(config["layout"], f"us,{layout}")
            self.assertEqual(config["options"], "grp:alt_shift_toggle")
            self.assertEqual(config["variant"], ",")
            self.assertIn("Alt+Shift", keyboard.keyboard_label(layout))


if __name__ == "__main__":
    unittest.main()
