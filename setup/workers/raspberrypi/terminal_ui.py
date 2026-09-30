"""Small, scrollback-friendly terminal UI. No packages, raw mode, or screen clearing."""

import getpass
import os
import shutil
import sys
import unicodedata

PLAIN = False
PALETTE = {"accent": "36", "brand": "35", "good": "32", "warn": "33", "bold": "1", "muted": "2"}


def color():
    return (
        not PLAIN
        and sys.stdout.isatty()
        and "NO_COLOR" not in os.environ
        and os.environ.get("TERM", "") not in ("", "dumb")
    )


def unicode():
    try:
        "╭─╮│╰╯›✓".encode(sys.stdout.encoding or "ascii")
        return not PLAIN and os.environ.get("TERM") != "dumb"
    except UnicodeError:
        return False


def safe(value):
    """Render labels literally, including terminal controls in device/SSID/key names."""
    text = "".join(
        f"\\u{ord(c):04x}" if unicodedata.category(c) in {"Cc", "Cf", "Zl", "Zp"} else c
        for c in str(value)
    )
    return text.encode(sys.stdout.encoding or "ascii", errors="backslashreplace").decode(
        sys.stdout.encoding or "ascii"
    )


def styled(value, tone="accent"):
    value = safe(value)
    return f"\033[{PALETTE[tone]}m{value}\033[0m" if color() else value


def width():
    return max(20, min(88, shutil.get_terminal_size((80, 24)).columns - 2))


def cells(text):
    """Count terminal columns, allowing for combining and full-width characters."""
    count = 0
    for char in text:
        if not unicodedata.combining(char):
            count += 2 if unicodedata.east_asian_width(char) in "WF" else 1
    return count


def wrap(value, limit):
    """Wrap without losing characters, including long paths and fingerprints."""
    text = safe(value)
    lines, line, size = [], "", 0
    for char in text:
        n = cells(char)
        if size + n > limit and line:
            # Prefer spaces for prose; split only tokens longer than the line.
            split = line.rfind(" ")
            if split > limit // 3:
                lines.append(line[:split])
                line = line[split + 1 :]
                size = cells(line)
            else:
                lines.append(line)
                line, size = "", 0
        line += char
        size += n
    return lines + [line]


def note(message, tone=None, hanging=0):
    for index, line in enumerate(wrap(message, width() - 4 - hanging)):
        print(
            "  " + (" " * hanging if index else "") + (styled(line, tone) if tone else line),
            flush=True,
        )


def success(message):
    prefix = "✓ " if unicode() else "OK  "
    note(prefix + str(message), "good", hanging=len(prefix))


def warning(message):
    note("! " + str(message), "warn", hanging=2)


def panel(title, rows, tone="accent"):
    wide = width()
    if wide < 45 or not unicode():
        note(title.upper(), tone)
        note("-" * (wide - 4), tone)
        for label, value in rows:
            note(f"{label}: {value}")
        return
    inner = wide - 6
    print("  " + styled("╭" + "─" * (inner + 2) + "╮", tone))
    for line in wrap(title.upper(), inner):
        print(
            "  "
            + styled("│", tone)
            + " "
            + styled(line, "bold")
            + " " * (inner - cells(line))
            + " "
            + styled("│", tone)
        )
    print("  " + styled("│" + " " * (inner + 2) + "│", tone))
    label_width = min(12, max((len(label) for label, _ in rows), default=0))
    for label, value in rows:
        parts = wrap(value, inner - label_width - 2)
        for index, part in enumerate(parts):
            prefix = f"{label:<{label_width}}  " if index == 0 else " " * (label_width + 2)
            body = prefix + part
            print(
                "  "
                + styled("│", tone)
                + " "
                + body
                + " " * (inner - cells(body))
                + " "
                + styled("│", tone)
            )
    print("  " + styled("╰" + "─" * (inner + 2) + "╯", tone), flush=True)


def banner():
    print()
    note("VM CONTROL PLANE   /   DEVICE SETUP", "brand")
    note("Raspberry Pi, ready for your network.", "bold")
    note("OS Lite 64-bit  ·  Raspberry Pi 5" if unicode() else "OS Lite 64-bit  /  Raspberry Pi 5")
    print()
    note("Six steps. Choose your settings, review, then flash.")
    note("Type a choice and press Enter. Ctrl+C cancels before writing.")


def step(number, title, detail=None):
    print()
    bar = ("━" * number + "─" * (6 - number)) if unicode() else ("#" * number + "-" * (6 - number))
    heading = f"{bar}  {number:02d} / 06   {title}"
    if cells(heading) <= width() - 4:
        note(heading, "accent")
    else:
        note(f"{bar}  {number:02d} / 06", "accent")
        note(title, "bold")
    if detail:
        note(detail)
    print()


def option(key, title, detail=None, suggested=False, code=None):
    prefix = f"{str(key):>2}  "
    for i, line in enumerate(wrap(title, width() - 8)):
        rendered = styled(line, "accent") if suggested else line
        if code and not suggested:
            token = f"[{safe(code)}]"
            rendered = rendered.replace(token, styled(token, "muted"))
        print("  " + (styled(prefix, "accent") if i == 0 else " " * len(prefix)) + rendered)
    if detail:
        for line in wrap(detail, width() - 8):
            print("      " + line)


def prompt(label, default=None):
    suffix = f" [{safe(default)}]" if default not in (None, "") else ""
    marker = "›" if unicode() else ">"
    text = f"{safe(label)}{suffix}"
    # Keep long paths readable and leave input room in narrow terminals.
    if cells(text) > width() - 12:
        note(text)
        text = ""
    return "  " + styled(marker) + (" " + text + ": " if text else " ")


def ask(label, default=None):
    value = input(prompt(label, default))
    return value if value or default is None else str(default)


def secret(label):
    # getpass writes to /dev/tty rather than stdout. Retain its no-echo handling.
    return getpass.getpass(prompt(label))


def progress(done, total):
    if not color():
        return  # Plain logs get start/completion messages, not carriage returns.
    ratio = min(1, done / total) if total else 0
    count = max(8, min(24, width() - 42))
    bar = "#" * int(ratio * count) + "-" * (count - int(ratio * count))
    value = f"{done / 1_000_000:.0f} MB" + (f" / {total / 1_000_000:.0f} MB" if total else "")
    print(
        "\r\033[2K  " + styled(f"[{bar}] {ratio:.0%}  " if total else "Downloading  ") + value,
        end="",
        flush=True,
    )
