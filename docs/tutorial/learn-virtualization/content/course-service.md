# Appendix A · The course service

This intentionally small, unauthenticated service is for the isolated disposable guest lab only. It gives the backup and migration exercises concrete acknowledged records to verify. Keep the guest on the lab network and do not publish port 8080. It is not a production application or security reference.

**1. Install inside linux-01.** Run these commands in the guest:

```bash
# Refresh the package index so the installs below resolve current versions.
sudo apt update
# Python runs the service; curl exercises it; sqlite3 inspects its database.
sudo apt install python3 curl sqlite3
# Create the code directory: root-owned and world-readable, so the
# unprivileged service user can execute but never modify the code.
sudo install -d -o root -g root -m 0755 /opt/kvm-course
# Create the source file as root; sudoedit avoids running your editor as root.
sudoedit /opt/kvm-course/service.py
```

Paste the following Python source into that file and save it. Keep the file root-owned and readable by the service user.

```python
"""Disposable KVM course service. Copy into a guest; do not expose outside lab NAT."""

# JSON encoding for every HTTP response body.
import json
# Embedded database engine; no separate server process needed in the guest.
import sqlite3
# Guarantees the database handle is closed on every exit path of a request.
from contextlib import closing
# Minimal threaded HTTP server from the standard library.
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Database file lives under the systemd StateDirectory (service-writable).
DB = "/var/lib/kvm-course/service.sqlite"
# Reject request bodies larger than this many bytes.
MAX_BODY = 1024


def connect():
    # Fresh connection per request; wait up to 5 s if another thread holds a lock.
    db = sqlite3.connect(DB, timeout=5)
    # Create the schema on first use; IF NOT EXISTS makes this safe to repeat.
    db.execute(
        "CREATE TABLE IF NOT EXISTS records "
        "(id INTEGER PRIMARY KEY, value TEXT NOT NULL)"
    )
    # Caller closes the handle (see closing() at each call site).
    return db


class Handler(BaseHTTPRequestHandler):
    # One handler serves one client connection; dispatch is by HTTP verb.

    def reply(self, status, payload):
        # Encode once so the Content-Length header is exact.
        body = json.dumps(payload).encode("utf-8")
        # Status line first, then headers, then body — HTTP wire order.
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        # Blank line ending the header block.
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        # Only two read endpoints exist; everything else is 404.
        if self.path not in ("/health", "/records"):
            return self.reply(404, {"error": "not found"})
        try:
            # closing() releases the connection even if a query raises.
            with closing(connect()) as db:
                if self.path == "/health":
                    # A trivial query proves the database file is actually usable.
                    db.execute("SELECT 1").fetchone()
                    return self.reply(200, {"status": "ok"})
                # Stable id ordering lets backup labs compare exact records.
                rows = db.execute(
                    "SELECT id, value FROM records ORDER BY id"
                ).fetchall()
            return self.reply(200, {"records": rows})
        except sqlite3.Error:
            # Database trouble is a service-side failure, not a client error.
            return self.reply(503, {"error": "database unavailable"})

    def do_POST(self):
        # Records are created at exactly one endpoint.
        if self.path != "/records":
            return self.reply(404, {"error": "not found"})
        try:
            # A missing header counts as zero length and is rejected below.
            length = int(self.headers.get("Content-Length", "0"))
            # Enforce 1..MAX_BODY bytes before reading anything from the socket.
            if not 0 < length <= MAX_BODY:
                return self.reply(413, {"error": "body must be 1–1024 bytes"})
            # Read exactly the declared length; strip to catch whitespace-only bodies.
            value = self.rfile.read(length).decode("utf-8").strip()
            if not value:
                return self.reply(400, {"error": "empty record"})
            with closing(connect()) as db:
                # Parameterized insert: input is stored as literal data and is
                # never interpolated into the SQL text.
                cursor = db.execute(
                    "INSERT INTO records(value) VALUES (?)", (value,)
                )
                # The generated primary key acknowledges this exact record.
                record_id = cursor.lastrowid
                # Persist before replying, so an acknowledged record is durable.
                db.commit()
            return self.reply(201, {"id": record_id, "value": value})
        except (UnicodeDecodeError, ValueError):
            # Non-UTF-8 bodies or a malformed Content-Length are client errors.
            return self.reply(400, {"error": "invalid body"})
        except sqlite3.Error:
            # Same service-side failure mapping as GET.
            return self.reply(503, {"error": "database unavailable"})


if __name__ == "__main__":
    # Listen on all guest interfaces, port 8080 — acceptable only on the
    # isolated lab NAT network. One thread per request; runs until stopped.
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
```

**2. Install the service unit.** Use `sudoedit /etc/systemd/system/kvm-course.service` to create the file below. It runs as the cloud image's ubuntu user; change that user explicitly if your image uses another account. StateDirectory creates the writable database directory at startup.

```text
[Unit]
# Name shown by systemctl status and in the journal.
Description=Disposable KVM course SQLite service
# Order startup after the network reports online (ordering only)...
After=network-online.target
# ...and pull that target in so the ordering has an effect.
Wants=network-online.target

[Service]
# Plain foreground process; systemd tracks the python3 PID directly.
Type=simple
# Run as the cloud image's unprivileged default user, never root.
User=ubuntu
# systemd creates /var/lib/kvm-course, writable by the service user, at start.
StateDirectory=kvm-course
# The single process this unit manages.
ExecStart=/usr/bin/python3 /opt/kvm-course/service.py
# Restart after crashes, but not after clean stops.
Restart=on-failure
# Hardening: the process cannot gain privileges (e.g. via setuid binaries).
NoNewPrivileges=yes
# Hardening: mount most of the filesystem read-only for this service.
ProtectSystem=strict
# Hardening: hide user home directories from the service.
ProtectHome=yes
# The one exception to ProtectSystem=strict: the database directory.
ReadWritePaths=/var/lib/kvm-course

[Install]
# Start with the normal multi-user boot target when enabled.
WantedBy=multi-user.target
```

**3. Start and check it.** Still inside linux-01, run:

```bash
# Stop this check sequence if any prerequisite or HTTP check fails.
set -euo pipefail
# Reload unit definitions so systemd sees the new kvm-course.service file.
sudo systemctl daemon-reload
# Enable the service at boot and start it immediately.
sudo systemctl enable --now kvm-course.service
# Confirm it is active; --no-pager prints directly to the terminal.
systemctl status kvm-course.service --no-pager
# Type=simple does not wait for application readiness. Allow startup time.
# Retry refused connections, fail on HTTP errors, and bound the whole wait.
timeout 20s curl --fail --retry 10 --retry-connrefused \
  --retry-delay 1 --max-time 2 http://127.0.0.1:8080/health
# Create record A; expect a JSON reply with its generated id.
curl --fail --data-binary A http://127.0.0.1:8080/records
# Create record B the same way.
curl --fail --data-binary B http://127.0.0.1:8080/records
# Create record C the same way.
curl --fail --data-binary C http://127.0.0.1:8080/records
# Read back all records; expect A, B and C with distinct ids.
curl --fail http://127.0.0.1:8080/records
# Ask SQLite itself to verify the database file as the service user; expect "ok".
sudo -u ubuntu sqlite3 /var/lib/kvm-course/service.sqlite 'PRAGMA integrity_check;'
```

**Check:** health reports `ok`; every POST returns a created ID/value; GET includes A, B and C; the integrity check reports `ok`. If startup fails, inspect `journalctl -u kvm-course.service --no-pager -n 50` before retrying. Repeating POST adds another record; the HTTP endpoint is deliberately not an idempotent provisioning API. For recovery comparisons, use the recorded acknowledged IDs and values, not an assumed row count. In migration exercises, test from a client that can reach the active guest on either host; host-local NAT alone does not preserve cross-host connectivity.

Locally checked again for this revision, against the commented listing: HTTP health, record insertion/readback, SQL-like input stored as literal data, body-size rejection, unknown-route handling, and database integrity/persistence after server shutdown. Those checks used a temporary database and loopback HTTP server. Its Python syntax tree was also compared with the previously reviewed listing: comments and wrapping did not change behavior. Verify your pasted copy starts cleanly. The same source and unit were also installed successfully inside the nested Ubuntu guest: health, A/B/C records, database integrity and persistence across a systemd restart passed. VM backup/restore and migration were not part of that run.
