"""Portable, curated XKB layouts supported by Raspberry Pi OS Trixie."""

import shutil
import terminal_ui as ui

# XKB layout[:variant], human name. A fixed catalog avoids offering newer
# workstation-only layouts that are absent from the pinned Raspberry Pi image.
LAYOUTS = (
    ("us", "English (US)"),
    ("gb", "English (UK)"),
    ("br", "Portuguese (Brazil, ABNT2)"),
    ("pt", "Portuguese (Portugal)"),
    ("us:intl", "English (US, international with dead keys)"),
    ("us:altgr-intl", "English (US, international via AltGr)"),
    ("us:dvorak", "English (US, Dvorak)"),
    ("us:colemak", "English (US, Colemak)"),
    ("es", "Spanish (Spain)"),
    ("latam", "Spanish (Latin America)"),
    ("fr", "French (France, AZERTY)"),
    ("de", "German (Germany, QWERTZ)"),
    ("de:nodeadkeys", "German (no dead keys)"),
    ("be", "Belgian"),
    ("ca", "French (Canada)"),
    ("ca:eng", "English (Canada)"),
    ("ch", "German (Switzerland)"),
    ("ch:fr", "French (Switzerland)"),
    ("it", "Italian"),
    ("nl", "Dutch"),
    ("dk", "Danish"),
    ("fi", "Finnish"),
    ("no", "Norwegian"),
    ("se", "Swedish"),
    ("is", "Icelandic"),
    ("pl", "Polish"),
    ("cz", "Czech (QWERTZ)"),
    ("cz:qwerty", "Czech (QWERTY)"),
    ("sk", "Slovak"),
    ("hu", "Hungarian"),
    ("ro", "Romanian"),
    ("bg", "Bulgarian + English"),
    ("hr", "Croatian"),
    ("si", "Slovenian"),
    ("rs", "Serbian + English"),
    ("gr", "Greek + English"),
    ("tr", "Turkish (Q)"),
    ("ru", "Russian + English"),
    ("ua", "Ukrainian + English"),
    ("ara", "Arabic + English"),
    ("il", "Hebrew + English"),
    ("ir", "Persian + English"),
    ("in", "Hindi (India) + English"),
    ("jp", "Japanese"),
    ("kr", "Korean"),
    ("th", "Thai + English"),
    ("vn", "Vietnamese"),
)
NAMES = dict(LAYOUTS)
NON_LATIN = {"ara", "bg", "gr", "il", "in", "ir", "rs", "ru", "th", "ua"}
COUNTRY_DEFAULTS = {
    "AU": "us",
    "NZ": "us",
    "IE": "gb",
    "AT": "de",
    "MX": "latam",
    "AR": "latam",
    "CL": "latam",
    "CO": "latam",
    "PE": "latam",
    "VE": "latam",
    "CA": "us",
    "IN": "us",
    "VN": "us",
    "NL": "us",
    # These countries commonly use US keyboards; every suggestion is explicit.
}


def validate_keyboard(value):
    value = value.strip().lower()
    if value not in NAMES:
        raise ValueError(
            "Unknown keyboard layout. Run interactively to choose a layout (examples: us, gb, br, us:intl)."
        )
    return value


def keyboard_config(value):
    layout, _, variant = validate_keyboard(value).partition(":")
    if layout in NON_LATIN:
        # Keep Latin login credentials typeable on the physical console. Both
        # groups and the switch are native XKB keyboard-configuration settings.
        return {
            "model": "pc105",
            "layout": f"us,{layout}",
            "variant": ",",
            "options": "grp:alt_shift_toggle",
        }
    # Match Imager's generic model. The pinned image has br (ABNT2 layout),
    # but no separate abnt2 model in its keyboard catalog.
    return {"model": "pc105", "layout": layout, "variant": variant, "options": ""}


def choose_keyboard(explicit=None, country=None, dry_run=False):
    default = COUNTRY_DEFAULTS.get(country, (country or "").lower())
    default = default if default in NAMES and default not in NON_LATIN else "us"
    if explicit is not None:
        return validate_keyboard(explicit)
    if dry_run:
        return default
    ui.note("Keyboard for the Pi's physical console. SSH uses your laptop's layout.")
    ui.note("Choose the layout printed on your keys. Enter accepts the highlighted suggestion.")
    ordered = sorted(LAYOUTS, key=lambda item: item[0] != default)
    matches, page = ordered, 0
    while True:
        entries = [
            (index, code, f"{name}  [{code}]" + ("  [suggested]" if code == default else ""))
            for index, (code, name) in enumerate(matches, 1)
        ]
        # Budget actual wrapped lines, so the list and its controls fit small terminals.
        help_text = "Type a number, code, or search (e.g. brazil). Enter accepts " + default + "."
        nav = "n next / p previous / ? all / q cancel"
        controls = len(ui.wrap(help_text, ui.width() - 4)) + len(ui.wrap(nav, ui.width() - 4)) + 5
        budget = max(3, shutil.get_terminal_size((80, 24)).lines - controls)
        pages, used = [[]], 0
        for entry in entries:
            height = len(ui.wrap(entry[2], ui.width() - 8))
            if pages[-1] and (used + height > budget or len(pages[-1]) == 10):
                pages.append([])
                used = 0
            pages[-1].append(entry)
            used += height
        page = min(page, len(pages) - 1)
        print()
        count = f"{len(matches)} option" + ("s" if len(matches) != 1 else "")
        ui.note(f"KEYBOARD   /   {count}   /   {page + 1} of {len(pages)}", "bold")
        for index, code, label in pages[page]:
            ui.option(index, label, suggested=code == default, code=code)
        if not matches:
            ui.warning("No layouts matched. Try another name or ? for all layouts.")
        ui.note(help_text)
        ui.note(nav if len(pages) > 1 else "? all layouts / q cancel")
        choice = ui.ask("Keyboard", default).strip().casefold()
        if choice == "q":
            raise KeyboardInterrupt
        if choice in {"n", "p"}:
            page = (page + (1 if choice == "n" else -1)) % len(pages)
        elif choice.isdecimal() and 1 <= int(choice) <= len(matches):
            return matches[int(choice) - 1][0]
        elif choice in NAMES:
            return choice
        else:
            query = choice.removeprefix("/").strip()
            matches = [
                item
                for item in ordered
                if query in {"", "all", "?"} or query in " ".join(item).casefold()
            ]
            page = 0


def keyboard_label(value):
    label = f"{NAMES[value]} ({value})"
    return label + "; Alt+Shift switches, English starts active" if value in NON_LATIN else label
