"""Choose a console mode and prepare boot settings on the workstation only."""

from contextlib import contextmanager
import json
import lzma
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

import terminal_ui as ui

DISPLAY_LABELS = {
    "1080p": "1080p / 1920 x 1080 / 60 Hz",
    "auto": "Automatic / monitor preferred mode",
}
# Use the existing 1080p timing, not a newly calculated CVT mode (the M suffix).
# No force-enable suffix: an unplugged HDMI port should stay inactive.
DISPLAY_ARGUMENTS = "video=HDMI-A-1:1920x1080@60 video=HDMI-A-2:1920x1080@60"


def choose_display(value, dry_run=False):
    if value is not None:
        if value not in DISPLAY_LABELS:
            raise ValueError("Display must be 1080p or auto.")
        return value
    if dry_run:
        return "1080p"
    print()
    ui.note("HDMI display", "accent")
    ui.option(
        "1",
        "1080p at 60 Hz (recommended)",
        "Worked with Wi-Fi in our Pi 5 test. Use a 1080p-capable monitor.",
        suggested=True,
    )
    ui.option("2", "Automatic", "Let the monitor choose its preferred resolution and refresh rate.")
    ui.note("You can also run the Pi without a monitor.")
    while True:
        answer = ui.ask("Display mode", "1").strip().lower()
        if answer in {"1", "1080p"}:
            return "1080p"
        if answer in {"2", "auto"}:
            return "auto"
        ui.warning("Choose 1 for 1080p or 2 for Automatic.")


def check_display_tools(mode):
    if mode == "1080p":
        for command in ("sfdisk", "mtype", "mcopy"):
            if not shutil.which(command):
                raise ValueError(
                    f"Missing command: {command}; 1080p preparation needs util-linux and mtools. See README prerequisites."
                )


def display_cmdline(original):
    """Keep the stock boot arguments, replacing only the two HDMI mode options."""
    line = original.rstrip("\r\n")
    if not line.strip() or any(c in line for c in "\r\n\x00"):
        raise ValueError("Unexpected cmdline.txt: expected one nonempty boot-argument line.")
    line = re.sub(r"(?<!\S)video=HDMI-A-[12]:\S+[ \t]*", "", line).rstrip()
    return line + " " + DISPLAY_ARGUMENTS + "\n"


def boot_offset(image):
    result = subprocess.run(
        ["sfdisk", "--json", str(image)], check=True, capture_output=True, text=True
    )
    table = json.loads(result.stdout)["partitiontable"]
    # The pinned official image uses a DOS partition table with a single FAT32
    # boot partition. Refuse another layout rather than guess an offset.
    partitions = [p for p in table.get("partitions", []) if p.get("type", "").lower() in {"b", "c"}]
    if table.get("label") != "dos" or table.get("unit") != "sectors" or len(partitions) != 1:
        raise ValueError("Unexpected OS image partition layout; cannot configure HDMI safely.")
    sector = table.get("sectorsize", 0)
    start, size = partitions[0].get("start", 0), partitions[0].get("size", 0)
    if sector != 512 or start <= 0 or size <= 0 or (start + size) * sector > image.stat().st_size:
        raise ValueError("Invalid boot partition bounds in OS image.")
    return start * sector


@contextmanager
def display_image(source, mode):
    """Yield an image for Imager; never modify the verified compressed cache."""
    if mode == "auto":
        yield source
        return
    if mode != "1080p":
        raise ValueError("Display must be 1080p or auto.")
    check_display_tools(mode)
    # Use the cache filesystem, not /tmp (often a small RAM-backed filesystem).
    with tempfile.TemporaryDirectory(prefix="display-", dir=source.parent) as directory:
        directory = Path(directory)
        prepared = directory / "raspios-1080p.img"
        ui.note("Preparing 1080p / 60 Hz boot settings (about 3.1 GB of temporary disk space)...")
        with lzma.open(source, "rb") as compressed, prepared.open("xb") as output:
            shutil.copyfileobj(compressed, output, length=1024 * 1024)
        drive = f"{prepared}@@{boot_offset(prepared)}"
        original = subprocess.run(
            ["mtype", "-i", drive, "::cmdline.txt"], check=True, capture_output=True
        ).stdout.decode("ascii")
        updated = display_cmdline(original).encode("ascii")
        cmdline = directory / "cmdline.txt"
        cmdline.write_bytes(updated)
        subprocess.run(["mcopy", "-o", "-i", drive, str(cmdline), "::cmdline.txt"], check=True)
        actual = subprocess.run(
            ["mtype", "-i", drive, "::cmdline.txt"], check=True, capture_output=True
        ).stdout
        if actual != updated:
            raise ValueError("Display configuration read-back failed; no card was written.")
        ui.success("1080p / 60 Hz configured for either HDMI port.")
        yield prepared
