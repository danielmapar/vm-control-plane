"""Test key selection/public extraction with disposable keys, never the user's agent."""

from contextlib import redirect_stdout
from dataclasses import replace
import io
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import ssh_keys as keys


class SSHKeyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.directory = Path(cls.temp.name)
        cls.key = cls.directory / "test_ed25519"
        cls.other = cls.directory / "other_ed25519"
        cls.rsa = cls.directory / "test_rsa"
        for path, algorithm in ((cls.key, "ed25519"), (cls.other, "ed25519"), (cls.rsa, "rsa")):
            command = ["ssh-keygen", "-q", "-t", algorithm, "-N", "", "-f", str(path)]
            if algorithm == "rsa":
                command += ["-b", "2048"]
            subprocess.run(command, check=True)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def test_existing_private_key_exports_only_matching_public_key(self):
        for path in (self.key, self.rsa):
            with self.subTest(path=path), redirect_stdout(io.StringIO()) as output:
                identity = keys.load_identity(path)
            public = keys.read_public(Path(str(path) + ".pub"))
            self.assertEqual(identity.fingerprint, public.fingerprint)
            self.assertEqual(identity.private_key, path)
            self.assertNotIn("PRIVATE KEY", identity.public_key)
            self.assertNotIn("PRIVATE KEY", output.getvalue())

    def test_missing_public_sidecar_is_derived_without_writing_one(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "private_only"
            shutil.copy2(self.key, path)
            with redirect_stdout(io.StringIO()):
                identity = keys.load_identity(path)
            self.assertEqual(
                identity.fingerprint, keys.read_public(Path(str(self.key) + ".pub")).fingerprint
            )
            self.assertFalse(Path(str(path) + ".pub").exists())

    def test_mismatched_key_pair_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "mismatch"
            shutil.copy2(self.key, path)
            shutil.copy2(Path(str(self.other) + ".pub"), Path(str(path) + ".pub"))
            with self.assertRaisesRegex(ValueError, "do not match"), redirect_stdout(io.StringIO()):
                keys.load_identity(path)

    def test_preview_never_derives_private_key_or_loads_agent(self):
        original = keys.command

        def public_only(*args, **kwargs):
            self.assertEqual(args[:3], ("ssh-keygen", "-lf", "/dev/stdin"))
            return original(*args, **kwargs)

        with (
            patch.object(keys, "command", side_effect=public_only),
            patch("builtins.input", side_effect=AssertionError("prompted")),
            redirect_stdout(io.StringIO()),
        ):
            identity = keys.choose_ssh_key(self.key, dry_run=True)
        self.assertIsNone(identity.private_key)

    def test_discovery_lists_private_keys_without_unlocking_them(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copy2(self.key, root / "id_ed25519_lab_workers")
            shutil.copy2(Path(str(self.key) + ".pub"), root / "id_ed25519_lab_workers.pub")
            shutil.copy2(self.other, root / "another_key_without_pub")
            (root / "config").write_text("Host example\n")
            (root / "known_hosts").write_text("example ssh-ed25519 irrelevant\n")
            found = keys.discover_keys(root)
            self.assertEqual(
                [p.name for p, _ in found], ["id_ed25519_lab_workers", "another_key_without_pub"]
            )

    def test_picker_selects_existing_and_supports_manual_path(self):
        found = [(self.key, "fingerprint hint")]
        for inputs in (["1"], ["p", str(self.key)]):
            with (
                self.subTest(inputs=inputs),
                patch.object(keys, "discover_keys", return_value=found),
                patch("builtins.input", side_effect=inputs),
                redirect_stdout(io.StringIO()),
            ):
                identity = keys.choose_ssh_key()
            self.assertEqual(identity.private_key, self.key)

    def test_public_only_input_and_private_content_rejection(self):
        identity = keys.load_identity(Path(str(self.key) + ".pub"))
        self.assertIsNone(identity.private_key)
        with self.assertRaisesRegex(ValueError, "public key file"):
            keys.read_public(self.key)

    def test_open_private_permissions_are_rejected_without_changing_them(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "key"
            shutil.copy2(self.key, path)
            path.chmod(0o644)
            with self.assertRaisesRegex(ValueError, "permissions"):
                keys.load_identity(path)
            self.assertEqual(path.stat().st_mode & 0o777, 0o644)

    def keygen_stub(self, *args, **kwargs):
        if args[:3] == ("ssh-keygen", "-t", "ed25519"):
            self.assertNotIn("-N", args)
            self.assertNotIn("-P", args)
            self.assertNotIn("input", kwargs)
            self.assertNotIn("env", kwargs)
            target = Path(args[args.index("-f") + 1])
            shutil.copy2(self.key, target)
            shutil.copy2(Path(str(self.key) + ".pub"), Path(str(target) + ".pub"))
            return subprocess.CompletedProcess(args, 0)
        return subprocess.run(args, text=True, **kwargs)

    def test_create_uses_native_passphrase_prompts_and_private_permissions(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "my new key"
            with (
                patch.object(keys, "command", side_effect=self.keygen_stub),
                redirect_stdout(io.StringIO()),
            ):
                identity = keys.create_identity(path, "pi-worker")
            self.assertEqual(identity.private_key, path)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(Path(str(path) + ".pub").stat().st_mode & 0o777, 0o644)
            self.assertEqual(
                keys.read_public(Path(str(path) + ".pub")).fingerprint, identity.fingerprint
            )

    def test_create_never_overwrites_private_public_or_broken_symlink(self):
        for kind in ("private", "public", "symlink"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                path = Path(directory) / "key"
                target = Path(str(path) + ".pub") if kind == "public" else path
                if kind == "symlink":
                    target.symlink_to(Path(directory) / "missing")
                else:
                    target.write_text("keep this")
                with patch.object(
                    keys, "command", side_effect=AssertionError("key generation started")
                ):
                    with self.assertRaisesRegex(ValueError, "already exists"):
                        keys.create_identity(path, "pi-worker")
                if kind == "symlink":
                    self.assertTrue(target.is_symlink())
                else:
                    self.assertEqual(target.read_text(), "keep this")

    def test_publish_race_preserves_other_file_and_removes_only_own_partial_key(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "key"
            public = Path(str(path) + ".pub")
            original = os.link

            def racing_link(source, target):
                if target == public:
                    public.write_text("created by someone else")
                return original(source, target)

            with (
                patch.object(keys, "command", side_effect=self.keygen_stub),
                patch.object(keys.os, "link", side_effect=racing_link),
                redirect_stdout(io.StringIO()),
            ):
                with self.assertRaises(FileExistsError):
                    keys.create_identity(path, "pi-worker")
            self.assertFalse(path.exists())
            self.assertEqual(public.read_text(), "created by someone else")

    def test_picker_can_create_or_cancel(self):
        identity = replace(keys.read_public(Path(str(self.key) + ".pub")), private_key=self.key)
        with (
            patch.object(keys, "discover_keys", return_value=[]),
            patch.object(keys, "new_key_path", return_value=Path("/mock/new_key")),
            patch.object(keys, "create_identity", return_value=identity) as create,
            patch("builtins.input", side_effect=["n", ""]),
            redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(keys.choose_ssh_key(hostname="pi-7"), identity)
        create.assert_called_once_with(Path("/mock/new_key"), "pi-7")
        with (
            patch.object(keys, "discover_keys", return_value=[]),
            patch("builtins.input", return_value="q"),
            redirect_stdout(io.StringIO()),
            self.assertRaises(KeyboardInterrupt),
        ):
            keys.choose_ssh_key()

    def test_selection_does_not_load_or_change_the_ssh_agent(self):
        identity = replace(keys.read_public(Path(str(self.key) + ".pub")), private_key=self.key)
        with (
            patch.dict(os.environ, SSH_AUTH_SOCK="/mock/agent"),
            patch.object(keys, "load_identity", return_value=identity),
            patch.object(
                keys,
                "command",
                side_effect=AssertionError("extra credential prompt or agent change"),
            ),
            redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(keys.choose_ssh_key(self.key), identity)
