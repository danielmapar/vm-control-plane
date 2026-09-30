# Programming bridge, libvirt APIs and small automation

**Goal:** turn a workflow you already run by hand into a small, tested Go program against the libvirt API. **Requires:** chapter 11. **Budget:** 4–14 sessions depending on Go experience. Budgets in chapters 12–19 are planning assumptions, not promises. **Evidence convention for chapters 12–19:** save each sublab's commands, code and output in that chapter's evidence directory and pin versions/commits in the manifest (Setup 1 and 3). The steps below do not repeat this.

**Read first:**  [libvirt API concepts](https://libvirt.org/api.html), the  [official Go bindings](https://pkg.go.dev/libvirt.org/go/libvirt) and  [libvirtxml](https://pkg.go.dev/libvirt.org/go/libvirtxml). Keep  [A Tour of Go](https://go.dev/tour/welcome/1) as a refresher only if the 12.1 check is hard. The supplementary pure-Go RPC binding is comparative reading for after the default implementation works.

Before the first exercise,  [install a supported Go toolchain](https://go.dev/doc/install) if needed. Record `go version` and pin the toolchain and module versions in your manifest; use that same version for the exercises and tests.

## 12.1 Go skill check

You know enough Go for this course when you can do the following without a tutorial. Treat it as a challenge, not homework:

1. Write a program that reads a JSON configuration file and makes one HTTP request with a `context` timeout.

2. Add table-driven unit tests, and wrap errors with `%w` so context survives each layer.

**Check:** a missing file, malformed JSON and an unreachable server each produce a distinct, actionable error message that you predicted before running. If this took real effort, do the Tour and repeat the exercise — the check is the gate, not the reading.

## 12.2 Concurrency and uncertain outcomes

This is the core failure model of chapters 17–18, practiced here without libvirt.

1. With `httptest`, build a local server whose POST handler records a job, then delays its response past the client's timeout.

2. Send concurrent requests carrying request IDs, with explicit synchronization.

3. Write the test that matters: the client times out, yet querying the job ID proves the server already recorded the job.

**Check:** your code reports that outcome as *uncertain* until observed, and does not blindly retry the mutation. `go test -race` runs clean, and you can explain what the race detector verified and what it cannot verify.

## 12.3 Read-only libvirt inventory

Toolchain first — the official bindings use CGo, and toolchain failures look nothing like permission failures:

1. `sudo apt install build-essential pkg-config libvirt-dev`. Confirm `pkg-config --modversion libvirt` succeeds and `go env CGO_ENABLED` reports 1 before debugging anything else.

2. Set up access: authorize a dedicated lab identity for the system-libvirt APIs using the distribution's documented group/polkit/ACL mechanism, verify a non-interactive connection, and remove the grant with the lab lifecycle. A libvirt-group grant is broad host-level management authority, not per-VM isolation; keep end-user application identities separate, and never embed a sudo password in code.

3. Pin the Go bindings module (retain go.mod/go.sum) and check it against the installed library's API support. Compile a minimal program: `NewConnectReadOnly("qemu:///system")`, print `GetURI()`, close the connection. Run it as the authorized lab identity.

4. Grow it into an inventory tool: list VMs, look one up by UUID, and print a typed summary parsed with libvirtxml.

*Refresher — handle lifetimes:* binding objects wrap C references. Learn Free/reference-release and callback ownership before passing domain objects to goroutines.

**Check:** output agrees with `virsh list --all`; create a VM out of band with virsh, and your next run reports it.

## 12.4 Typed XML, lifecycle and idempotency

1. Generate domain XML with libvirtxml for a disposable lab VM; define, start and stop it with explicit target-state checks.

2. Unit-test the generated configuration; run integration tests against disposable domains only.

3. Make provisioning idempotent: look up existing state before acting.

**Check:** rerunning the same request creates no duplicate VM, and errors preserve useful context through every layer.

## 12.5 Events reconciled with inventory

1. Register the event-loop implementation *before* creating any connection, and keep the loop running ([Go event-loop ordering](https://pkg.go.dev/libvirt.org/go/libvirt#EventRegisterDefaultImpl),  [domain event contract](https://libvirt.org/html/libvirt-libvirt-domain.html#virConnectDomainEventRegisterAny)).

2. Register lifecycle callbacks.

3. Failure drill: disconnect the client, change a VM through virsh, reconnect, discover the current state.

**Check:** state converges from a fresh inventory even when events were missed — callbacks are an accelerator, not the source of truth.

**Chapter pass:** all five checks, plus a short written explanation of the path from your client through the binding and libvirt's driver to QEMU, and of what this tool still lacks for production reliability. **Recovery:** integration tests clean up by undefining disposable domains with the correct NVRAM flags; never point tests at guests you depend on.
