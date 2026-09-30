# Final integration, review and continued expertise

**Goal:** integrate the skills and demonstrate independent engineering judgment. **Requires:** chapters 0–18 with their practical checks passed or explicitly outstanding, and every hardware limitation recorded. **Budget:** 10–16 sessions plus external review latency.

## 19.1 Assemble and freeze the platform

Freeze versions and produce the capstone bundle: a small multi-host platform with reproducible Linux and Windows provisioning, documented security boundaries, backup/restore, useful monitoring, capacity limits, migration/drain, the resilient control plane and secure console access — plus a diagram, supported-version matrix, runbooks, tests, a recovery kit including the controller store/configuration, and a representative smoke-test suite.

**Check:** the smoke suite runs clean from the documentation alone.

## 19.2 Assessment

- **Full path — independent assessment:** another person deploys from the documentation, introduces a previously unseen fault, requests a host maintenance cycle, and attempts recovery with the original host unavailable. Measure application behavior, integrity, recovery time and denied access; record failures honestly and correct the design.

- **Solo option (reduced path only):** build a sealed fault bank well in advance, select a fault randomly after a delay long enough that the solution is not fresh in memory, and open the sealed solution notes only after completing the incident report. This is honest self-assessment and explicitly weaker evidence than independent external validation; record which form was used.

## 19.3 Source-level deliverable

1. Choose a small QEMU/libvirt/KVM improvement, diagnostic, regression test or well-supported bug report.

2. Select tests for the changed layer — for example QEMU device tests, KVM userspace-API selftests, or guest-oriented kvm-unit-tests ([QEMU testing](https://www.qemu.org/docs/master/devel/testing/index.html),  [KVM review checklist](https://docs.kernel.org/virt/kvm/review-checklist.html)).

3. Demonstrate before/after behavior and request review.

Upstream acceptance is outside your control and is not a graduation condition. Passing one suite does not prove the whole platform correct; separate tests actually run from tests merely planned.

## 19.4 Retrospective and continuation

Write a retrospective distinguishing full-path from reduced-path completions, with outstanding items named. Continue with the supplementary virt-tools feed, KVM Forum/FOSDEM archives, project release notes and selected mailing lists. Revalidate the smoke suite when upgrading, and adopt newer mechanisms only after identifying the concrete problem they solve and their support status on the actual platform.

**Chapter pass:** defend the architecture, recover from failures, explain an end-to-end API/I/O path, and demonstrate that someone else can reproduce the result. This establishes substantial practical and source-level capability; sustained expertise comes from repeated unfamiliar incidents, design reviews, changing versions and continued contributions — not completing a course once. **Recovery:** the capstone environment remains disposable and rebuildable from the recovery kit.
