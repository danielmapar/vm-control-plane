"""Select/create client SSH identities. Only public key material enters the image."""

from dataclasses import dataclass
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
import terminal_ui as ui

PUBLIC_TYPES = {
    "ssh-ed25519",
    "ssh-rsa",
    "ecdsa-sha2-nistp256",
    "ecdsa-sha2-nistp384",
    "ecdsa-sha2-nistp521",
    "sk-ssh-ed25519@openssh.com",
    "sk-ecdsa-sha2-nistp256@openssh.com",
}
PRIVATE_HEADERS = {
    b"-----BEGIN OPENSSH PRIVATE KEY-----",
    b"-----BEGIN RSA PRIVATE KEY-----",
    b"-----BEGIN EC PRIVATE KEY-----",
    b"-----BEGIN PRIVATE KEY-----",
    b"-----BEGIN ENCRYPTED PRIVATE KEY-----",
}


@dataclass(frozen=True)
class Identity:
    public_key: str
    fingerprint: str
    algorithm: str
    private_key: Path | None = None


def command(*args, **kwargs):
    return subprocess.run(args, text=True, **kwargs)


def public_identity(text, private=None):
    text = text.strip()
    fields = text.split()
    if len(text.splitlines()) != 1 or len(fields) < 2 or fields[0] not in PUBLIC_TYPES:
        raise ValueError(
            "Provide one OpenSSH public key: Ed25519, RSA, ECDSA, or a supported security key."
        )
    result = command(
        "ssh-keygen",
        "-lf",
        "/dev/stdin",
        "-E",
        "sha256",
        input=text + "\n",
        capture_output=True,
        check=False,
    )
    parts = result.stdout.split()
    if result.returncode or len(parts) < 2 or not re.fullmatch(r"SHA256:[A-Za-z0-9+/]+", parts[1]):
        raise ValueError("The selected public key is not valid.")
    if fields[0] == "ssh-rsa" and int(parts[0]) < 2048:
        raise ValueError("Use an RSA key with at least 2048 bits, or an Ed25519 key.")
    return Identity(text, parts[1], fields[0], private)


def read_public(path):
    with path.open() as stream:
        # Reject a mistakenly selected private file before reading its key data.
        line = stream.readline(16384)
        if not line.split() or line.split()[0] not in PUBLIC_TYPES:
            raise ValueError("Expected an OpenSSH public key file, not private key contents.")
        if any(part.strip() for part in stream):
            raise ValueError("Choose a file containing exactly one public key.")
    return public_identity(line)


def is_private(path):
    try:
        if not path.is_file():
            return False
        with path.open("rb") as stream:
            return stream.readline(128).strip() in PRIVATE_HEADERS
    except OSError:
        return False


def load_identity(path, dry_run=False):
    path = Path(path).expanduser().resolve(strict=True)
    if path.suffix == ".pub":
        # Explicit public-only input remains supported for keys held elsewhere.
        return read_public(path)
    if not is_private(path):
        raise ValueError("Select a private SSH key file or its .pub file.")
    if path.stat().st_mode & 0o077:
        raise ValueError(
            f"Private key permissions are too open. Run: chmod 600 {shlex.quote(str(path))}"
        )
    public_path = Path(str(path) + ".pub")
    if dry_run:
        if not public_path.is_file():
            raise ValueError(
                "A preview needs an existing .pub file; it never unlocks or creates keys."
            )
        return read_public(public_path)
    ui.note("Verifying the selected private key; enter its passphrase locally if prompted.")
    # Always derive from the actual private key. `ssh-keygen -l -f PRIVATE`
    # may read the .pub sidecar, so it cannot prove that the pair matches.
    result = command("ssh-keygen", "-y", "-f", str(path), stdout=subprocess.PIPE, check=True)
    identity = public_identity(result.stdout, path)
    if public_path.exists() and read_public(public_path).fingerprint != identity.fingerprint:
        raise ValueError(
            "The private key and its .pub file do not match; fix the pair before flashing."
        )
    return identity


def discover_keys(directory):
    if not directory.is_dir():
        return []
    keys = []
    for path in sorted(directory.iterdir()):
        if path.name.startswith(".") or not is_private(path):
            continue
        public_path = Path(str(path) + ".pub")
        try:
            hint = (
                read_public(public_path).fingerprint
                if public_path.is_file()
                else "public key derived after selection"
            )
        except (ValueError, OSError):
            hint = "public key checked after selection"
        keys.append((path, hint))
    return sorted(keys, key=lambda item: (item[0].name != "id_ed25519_lab_workers", item[0].name))


def new_key_path(hostname):
    root = Path.home() / ".ssh"
    base = root / f"id_ed25519_{hostname}"
    path, number = base, 2
    while os.path.lexists(path) or os.path.lexists(str(path) + ".pub"):
        path = root / f"{base.name}_{number}"
        number += 1
    return path


def create_identity(path, hostname):
    path = Path(path).expanduser().absolute()
    public_path = Path(str(path) + ".pub")
    if os.path.lexists(path) or os.path.lexists(public_path):
        raise ValueError(
            "That private key or .pub path already exists. Choose a new path; nothing will be overwritten."
        )
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    ui.note(
        "Creating an Ed25519 key. A passphrase is recommended; enter and confirm it in the local ssh-keygen prompts."
    )
    with tempfile.TemporaryDirectory(prefix=".vmc-keygen-", dir=path.parent) as directory:
        temporary = Path(directory) / "key"
        # No passphrase in arguments, environment variables, logs, or our Python memory.
        command(
            "ssh-keygen",
            "-t",
            "ed25519",
            "-a",
            "100",
            "-C",
            f"lab-workers:{hostname}",
            "-f",
            str(temporary),
            check=True,
        )
        generated_public = Path(str(temporary) + ".pub")
        identity = read_public(generated_public)
        temporary.chmod(0o600)
        generated_public.chmod(0o644)
        installed = []
        try:
            # Hard links on the same filesystem publish without an overwrite race.
            for source, target in ((temporary, path), (generated_public, public_path)):
                os.link(source, target)
                installed.append((source, target))
        except OSError:
            for source, target in installed:
                if target.exists() and os.path.samefile(source, target):
                    target.unlink()
            raise
    ui.success(f"New private key saved locally: {json.dumps(str(path))}")
    return Identity(identity.public_key, identity.fingerprint, identity.algorithm, path)


def choose_ssh_key(explicit=None, hostname="pi-worker", dry_run=False):
    if dry_run:
        path = explicit or Path.home() / ".ssh/id_ed25519_lab_workers.pub"
        identity = load_identity(path, dry_run=True)
    elif explicit is not None:
        identity = load_identity(explicit)
    else:
        keys = discover_keys(Path.home() / ".ssh")
        ui.note("Only the public key goes onto the Pi. Your private key stays here.")
        for index, (path, fingerprint) in enumerate(keys, 1):
            suggestion = " (suggested lab key)" if path.name == "id_ed25519_lab_workers" else ""
            ui.option(index, f"{json.dumps(path.name)}{suggestion}", fingerprint)
        ui.option("p", "Enter an existing private key or .pub path")
        ui.option("n", "Create a new Ed25519 key pair")
        ui.option("q", "Cancel setup")
        while True:
            choice = ui.ask("SSH key").strip().lower()
            try:
                if choice == "q":
                    raise KeyboardInterrupt
                if choice == "n":
                    suggestion = new_key_path(hostname)
                    entered = ui.ask("New private key path", suggestion).strip()
                    identity = create_identity(Path(entered) if entered else suggestion, hostname)
                elif choice == "p":
                    identity = load_identity(ui.ask("Existing private key or .pub path").strip())
                elif choice.isdecimal() and 1 <= int(choice) <= len(keys):
                    identity = load_identity(keys[int(choice) - 1][0])
                else:
                    ui.warning("Choose a listed number, p, n, or q.")
                    continue
                break
            except (ValueError, OSError, subprocess.CalledProcessError) as error:
                ui.warning(f"SSH key selection failed: {error}")
    ui.success(f"Selected SSH key: {identity.algorithm} {identity.fingerprint}")
    if identity.private_key:
        ui.note(f"Client private key stays on this laptop: {json.dumps(str(identity.private_key))}")
    elif not dry_run:
        ui.note("Public-only input: use its matching private key on your SSH client to connect.")
    return identity
