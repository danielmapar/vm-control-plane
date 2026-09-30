# Observability, performance and capacity

**Goal.** Decide whether the service is healthy, why it is slow and whether host-a has room for maintenance. Collect host, hypervisor, guest and application signals separately.

**Read first.** Statistics sections of  [virsh](https://libvirt.org/manpages/virsh.html), the installed distribution’s performance guide, and  [Prometheus node\_exporter](https://github.com/prometheus/node_exporter).

## 8.1 Measure a baseline

1. Record healthy behavior for linux-01 and linux-02: application HTTP read/write latency and result, sudo virsh -c qemu:///system domstats --cpu-total --vcpu --balloon --block --interface, host CPU/memory/disk/network, and guest service/SQLite health. Repeat measurements and record units, scrape intervals and versions.

2. Distinguish counters from rates, guest CPU use from host contention, absent fields from zero, and collector failure from service failure.

**Check:** Host, hypervisor, guest and app baseline values have units and repeatable observations.

## 8.2 Add one host-local collector

1. Use  [Ubuntu Noble’s packaged](https://packages.ubuntu.com/noble-updates/prometheus-libvirt-exporter) [`prometheus-libvirt-exporter`](https://packages.ubuntu.com/noble-updates/prometheus-libvirt-exporter) as the first hypervisor collector. The course baseline is the Noble package, and the exact installed version goes in the manifest; do not substitute an unrecorded container or download. The recorded validation used `0.2.0-1ubuntu0.24.04.3`; record any repository update and rerun the checks below. The upstream project is archived, so this is a small-baseline choice backed by the Ubuntu package rather than a recommendation for a new production deployment. Chapter 17 replaces it with your own bulk-stats collector.

2. The package's default is `:9177`, which listens on every address, and its installer adds the `prometheus` service account to the broad `libvirt` management group. Prevent that default service from starting, configure the host-only listener and libvirt's read-only socket, then remove the broad group grant:

```bash
# Stop on unhandled errors, unset variables and failed pipeline stages.
set -euo pipefail
# Mask before installation so the package cannot briefly bind its default :9177.
sudo systemctl mask prometheus-libvirt-exporter.service
# Refresh the package index for the recorded Ubuntu Noble repositories.
sudo apt update
# Install Ubuntu's release-packaged exporter rather than an unpinned binary.
sudo apt install prometheus-libvirt-exporter
# Record the exact version selected from Noble in the course manifest.
dpkg-query -W -f='${Package} ${Version} ${Status}\n' prometheus-libvirt-exporter
# Require the reference host's standard read-only libvirt socket.
test -S /run/libvirt/libvirt-sock-ro
# Write the complete, intentionally small exporter configuration.
sudo tee /etc/default/prometheus-libvirt-exporter >/dev/null <<'CONFIG'
# Expose metrics only to this host and query libvirt through its read-only socket.
ARGS="--web.listen-address=127.0.0.1:9177 --libvirt.uri=qemu+unix:///system?socket=/run/libvirt/libvirt-sock-ro"
CONFIG
# Undo the package's broad read-write group grant when it was added.
if id -nG prometheus | grep -qw libvirt; then
  # Keep the exporter identity limited to the read-only socket path above.
  sudo gpasswd --delete prometheus libvirt
fi
# Remove the temporary installation mask now that safe arguments are present.
sudo systemctl unmask prometheus-libvirt-exporter.service
# Reload unit/configuration state after unmasking the packaged unit.
sudo systemctl daemon-reload
# Enable the configured exporter and start it now.
sudo systemctl enable --now prometheus-libvirt-exporter.service
```

During installation, package scripts may report that a unit is masked or that systemctl preset failed. That is expected while the deliberate mask prevents startup: apt must still finish successfully. Investigate any nonzero apt exit status; do not ignore other errors. The explicit Unix transport is documented by libvirt's  [connection URI guide](https://libvirt.org/uri.html); `/run/libvirt/libvirt-sock-ro` is the standard  [read-only daemon socket](https://libvirt.org/remote.html). Binding to `127.0.0.1` prevents a network client from reaching the exporter, while the read-only socket prevents its libvirt connection from mutating domains. These controls address different boundaries. Do not run unrelated code as the `prometheus` account.

3. Smoke-test readiness, listener scope and one running VM, then compare the exporter with the official CLI control:

```bash
# Stop if a readiness or verification command fails.
set -euo pipefail
# Use a private temporary file for one captured Prometheus scrape.
METRICS_FILE=$(mktemp)
# Remove that temporary file whenever this shell exits.
trap 'rm -f "$METRICS_FILE"' EXIT
# Poll readiness with per-request limits and a 20-second total bound.
timeout 20s curl --fail --silent --show-error --retry 10 \
  --retry-connrefused --retry-delay 1 --max-time 2 \
  http://127.0.0.1:9177/metrics >"$METRICS_FILE"
# Reject an empty response before inspecting its metrics.
test -s "$METRICS_FILE"
# Require a successful libvirt connection, not merely a running HTTP process.
grep -Fx 'libvirt_up 1' "$METRICS_FILE"
# Require at least one metric for the known running course guest.
grep -F 'domain="linux-01"' "$METRICS_FILE" | sed -n '1,10p'
# Show the service account; it should no longer list the libvirt group.
id prometheus
# Prove the TCP listener is exactly host-local port 9177.
sudo ss -H -lntp '( sport = :9177 )'
# Capture libvirt's control reading at a recorded instant for comparison.
sudo virsh -c qemu:///system domstats linux-01 \
  --cpu-total --vcpu --balloon --block --interface --raw
```

**Expected observations:** `libvirt_up 1`; domain metrics labelled `domain="linux-01"`; `id prometheus` without the `libvirt` group; and an `ss` local address of `127.0.0.1:9177`, never `0.0.0.0:9177` or `[::]:9177`. `virsh` reports `cpu.time` in nanoseconds while the exporter reports `libvirt_domain_info_cpu_time_seconds_total` in seconds. Block/network counters should name the same disk and interface and move in the same direction; separately timed scrapes need not be numerically identical. Memory-use meanings must be checked before graphing rather than assuming every field is guest working-set memory.

4. Keep 8.1's host readings and Appendix A's HTTP/SQLite checks beside this endpoint. `libvirt_up 1` means the exporter reached libvirt; it does not prove that linux-01 is running, SSH works, or the course service is healthy. A stopped collector must become a distinct collector-loss alert in 8.3. Add node\_exporter or a central Prometheus server only after this local baseline works, with a recorded package/version and a trusted management path; do not make port 9177 public to simplify scraping.

**Check:** the packaged exporter is versioned in the manifest, active on loopback only, connected through the read-only libvirt socket, and exposes linux-01 counters that agree in identity, units and direction with a nearby `virsh domstats` reading. Stop it with `sudo systemctl disable --now prometheus-libvirt-exporter.service` if you cannot preserve those boundaries; diagnose with `journalctl -u prometheus-libvirt-exporter.service --no-pager -n 50`.

## 8.3 Evaluate one alert end to end

1. Install Ubuntu Noble's packaged  [Prometheus server](https://packages.ubuntu.com/noble/prometheus) with the same boundaries as 8.2 — loopback listener, recorded version, no public port. The masked-unit package messages explained in 8.2 are expected here too; apt must still succeed:

```bash
# Stop on unhandled errors, unset variables and failed pipeline stages.
set -euo pipefail
# Keep the server off its default all-interfaces :9090 until configured.
sudo systemctl mask prometheus.service
# Install the server and promtool without optional exporters or other servers.
sudo apt install --no-install-recommends prometheus
# Record the exact packaged version in the course manifest.
dpkg-query -W -f='${Package} ${Version} ${Status}\n' prometheus
# Serve the API only on this host, matching the exporter's boundary.
sudo tee /etc/default/prometheus >/dev/null <<'CONFIG'
# Loopback-only web/API listener for the lab.
ARGS="--web.listen-address=127.0.0.1:9090"
CONFIG
```

2. Save the packaged configuration, then replace it with this small local setup. Refuse an existing backup rather than overwriting your previous lab state:

```bash
# Refuse an existing backup so this step cannot overwrite earlier evidence.
test ! -e /etc/prometheus/prometheus.yml.course-original
# Save the packaged configuration before replacing it.
sudo cp -a /etc/prometheus/prometheus.yml /etc/prometheus/prometheus.yml.course-original
# Scrape only the two known local endpoints; load the course rules file.
sudo tee /etc/prometheus/prometheus.yml >/dev/null <<'CONFIG'
global:                       # Defaults for every scrape and rule group.
  scrape_interval: 15s        # Collect each target every 15 seconds.
  evaluation_interval: 15s    # Evaluate alerting rules on the same cadence.
rule_files:                   # Load alert definitions from this local file.
  - /etc/prometheus/course-alerts.yml   # The starter alerts defined below.
scrape_configs:               # Each job names one independently checked target.
  - job_name: prometheus      # The server's own metrics, for self-diagnosis.
    static_configs:          # Use explicit endpoints for this one-host lab.
      - targets: ['127.0.0.1:9090']     # Loopback listener from step 1.
  - job_name: libvirt         # The 8.2 exporter; the job name is used in rules.
    static_configs:          # Use explicit endpoints for this one-host lab.
      - targets: ['127.0.0.1:9177']     # Loopback-only exporter from 8.2.
CONFIG
```

3. Write three starter rules that separate the failure layers 8.1 distinguished, validate both files, then start the server:

```bash
# Three alerts: scrape failure, libvirt-connection failure, missing guest metrics.
sudo tee /etc/prometheus/course-alerts.yml >/dev/null <<'CONFIG'
groups:                      # Prometheus evaluates named groups of rules.
  - name: course-starter      # Extend this group with measured thresholds later.
    rules:                   # Each rule identifies one failure signal.
      - alert: LibvirtCollectorDown     # Failure to scrape the exporter.
        expr: up{job="libvirt"} == 0    # The last scrape of the exporter failed.
        for: 1m                          # Require sustained failure, not one miss.
        labels:                         # Attach routing/filtering metadata.
          severity: lab                  # Label only; no notification router yet.
        annotations:                    # Human-readable troubleshooting context.
          summary: libvirt exporter scrape failing on host-a # Check exporter service.
      - alert: LibvirtConnectionLost    # Exporter cannot query libvirt.
        expr: libvirt_up{job="libvirt"} == 0            # Exporter answers but libvirt does not.
        for: 1m                         # Require a sustained one-minute failure.
        labels:                         # Attach routing/filtering metadata.
          severity: lab                 # Lab severity; no notification router yet.
        annotations:                    # Human-readable troubleshooting context.
          summary: exporter cannot reach libvirt # Inspect socket and daemon.
      - alert: CourseGuestMetricsAbsent # No series for the expected guest.
        # The 8.2-observed metric for linux-01 has disappeared entirely.
        expr: absent(libvirt_domain_info_cpu_time_seconds_total{job="libvirt",domain="linux-01"})
        for: 2m                         # Tolerate brief inventory transitions.
        labels:                         # Attach routing/filtering metadata.
          severity: lab                 # Lab severity; no notification router yet.
        annotations:                    # Human-readable troubleshooting context.
          summary: linux-01 metrics absent # Check collector chain before guest state.
CONFIG
# Validate configuration and rules before starting anything.
promtool check config /etc/prometheus/prometheus.yml
# Remove the installation mask now that safe configuration exists.
sudo systemctl unmask prometheus.service
# Reload unit state, then enable and start the configured server.
sudo systemctl daemon-reload
sudo systemctl enable --now prometheus.service
# Bounded readiness check against the loopback API.
timeout 20s curl --fail --silent --retry 10 --retry-connrefused \
  --retry-delay 1 --max-time 2 http://127.0.0.1:9090/-/ready
```

4. First require a healthy baseline. These queries print JSON so you can see the target labels, timestamp and value rather than matching an unrelated string:

```bash
# Fail on HTTP errors and pipeline errors; bound every API request.
set -euo pipefail
# Inspect the libvirt scrape result; expect status success and value "1".
curl --fail --silent --show-error --max-time 5 --get \
  --data-urlencode 'query=up{job="libvirt"}' \
  http://127.0.0.1:9090/api/v1/query | python3 -m json.tool
# Inspect current alerts; the healthy baseline has an empty alerts array.
curl --fail --silent --show-error --max-time 5 \
  http://127.0.0.1:9090/api/v1/alerts | python3 -m json.tool
# Confirm the server listens only on the intended loopback address.
sudo ss -H -lntp '( sport = :9090 )'
```

**Before the drill:** wait for at least one successful 15-second scrape; require `status: success`, the libvirt result value `"1"`, an empty alerts array (no pending or firing starter alert), and listener `127.0.0.1:9090`. On a fresh server, CourseGuestMetricsAbsent may be pending before the first successful scrape; wait one more 15-second evaluation interval and query again. Keep the course guest and its application healthy. Then stop only the collector:

```bash
# Arrange recovery even if this drill exits early.
trap 'sudo systemctl start prometheus-libvirt-exporter.service' EXIT
# Trigger collector loss; leave the VM and application untouched.
sudo systemctl stop prometheus-libvirt-exporter.service
# Repeat this bounded query until LibvirtCollectorDown reports state firing.
curl --fail --silent --show-error --max-time 5 \
  http://127.0.0.1:9090/api/v1/alerts | python3 -m json.tool
```

Expect roughly 1–2 minutes: a failed scrape, then the rule’s one-minute hold and evaluation interval. Repeat the query while waiting. If it has not fired after three minutes, restore the collector and inspect `/api/v1/targets`, the rules and the journal; do not keep waiting indefinitely. The missing-guest-metrics alert may follow: collector loss also removes guest series, which is why that alert does not claim the VM is down. Now recover:

```bash
# Restore the collector so Prometheus can scrape domain statistics again.
sudo systemctl start prometheus-libvirt-exporter.service
# Repeat until the collector is up and no starter alert is firing.
curl --fail --silent --show-error --max-time 5 \
  http://127.0.0.1:9090/api/v1/alerts | python3 -m json.tool
# The recovery action succeeded; remove this shell's temporary exit handler.
trap - EXIT
```

The  [Prometheus HTTP API](https://prometheus.io/docs/prometheus/latest/querying/api/) documents these responses. Prometheus evaluates and displays alerts here; it does not send a notification until a notification path is configured.

5. Extend the group with measured thresholds from your 8.1 baseline — guest/service availability, CPU pressure or vCPU delay, guest/host memory pressure, storage latency/free space — and trigger each on a disposable workload. Note that one underlying fault can raise several rules; routing, grouping, silencing and notification belong to  [Alertmanager](https://prometheus.io/docs/alerting/latest/overview/), which this course deliberately defers. Rule syntax:  [alerting rules](https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/).

**Check:** `promtool` accepts the configuration; the server answers only on `127.0.0.1:9090`; the collector-loss alert visibly fires and resolves through the API; and a stopped collector is never read as "all VMs healthy" — the missing-metrics alert proves silence is itself a signal.

## 8.4 Create one bottleneck

1. Change **one bounded resource at a time** on a disposable guest: CPU contention, small test-filesystem pressure, or stopped collector. Use host and guest evidence to predict the failing resource, then remove the fault and compare the baseline. Save repeated measurements. Never fill host root or exhaust shared production resources.

**Check:** The bounded fault explains measured change and its removal restores baseline.

## 8.5 Set capacity limits

1. Write a capacity rule that reserves host RAM/CPU and disk headroom for QEMU, backups and migration. Revisit overcommit, ballooning, NUMA, hugepages, pinning, IOThreads and cache/discard/flush as hypotheses to benchmark, not universal tuning rules. Check how each chosen setting affects migration compatibility.

**Check:** The admission rule reserves capacity and accounts for measured migration constraints.

**Pass and cleanup.** Show a firing and recovering alert, explain a cross-layer bottleneck, and justify admission with measured reserve. Restore the baseline; account for counter resets and missing data. One benchmark win is not a general tuning rule.
