# Turn a working lab into a reviewable engineering change

## 24.1 Transfer the method to a larger device

The device you built has a deliberately small contract. Its commands are synchronous, transfers are at most 4096 bytes, and it has one interrupt source. That makes its ownership and state transitions visible. A larger device adds new obligations rather than making the existing ones disappear.

Before adding asynchronous work, draw who owns each request and buffer, which thread can change each field, and how reset, unplug, and migration stop or finish pending work. Do not carry a raw guest-derived pointer into a worker without a lifetime and address-space argument. A pending interrupt is state; an interrupt pulse is an event. The migration test intentionally preserves a pending level so a forgotten field or output reconstruction becomes observable.

For a VirtIO device, the next boundary is the virtqueue: negotiate supported features, validate descriptor chains and lengths, distinguish device-readable from device-writable buffers, publish used entries with the required ordering, and generate notifications according to the negotiated rules. The PCI BAR/DMA/interrupt lessons still apply, but this course device's private register protocol is not the VirtIO protocol. A production VirtIO implementation also needs queue reset and in-flight request migration behavior. The preceding labs establish the test discipline to approach that work; they do not silently claim those unimplemented features.

When changing VMState, ask whether an older destination can read the new stream, whether an older stream supplies sensible defaults, and whether the reconstructed output matches the saved logical state. Versioning a structure is not by itself a compatibility test. The worked migration check is same-version, same-build device migration over a local Unix channel; it does not establish cross-release compatibility or full production live-migration readiness.

## 24.2 Choose the next KVM entry point deliberately

Use the pinned source as a map, then select one narrow question:

| **Question** | **First source path to follow** |
| --- | --- |
| How does `KVM_RUN` reach guest execution? | `arch/x86/kvm/x86.c`: `kvm_arch_vcpu_ioctl_run` → `vcpu_run` → `vcpu_enter_guest`, then the VMX/SVM implementation selected by x86 operations |
| Why did a second-stage memory access fault? | `arch/x86/kvm/mmu/mmu.c`: `kvm_mmu_page_fault`, the applicable direct/TDP path, and the SPTE operations it invokes |
| Can a memory-slot pointer remain valid here? | The memslot lookup, its SRCU read-side lifetime, and the slot-update/invalidation path in `virt/kvm/kvm_main.c` |
| Is this a userspace-visible behavior change? | The ioctl/capability definition in `Documentation/virt/kvm/api.rst`, its validation path, and existing selftests |

Treat a capability as an explicit contract, not a guess based on the kernel version string. Preserve defined error behavior and reserved-field checks. On a failure path, determine which resources and side effects already exist; returning an error does not automatically roll them back. The DMA partial-write case and the dirty-log rearm case are two concrete examples of why that matters.

## 24.3 Explain the evidence before preparing the patch

For a QEMU device change, include the register behavior that changed, reset behavior, migration implications, invalid-input cases, and the focused qtests. For a KVM change, include the affected invariant, relevant lock/lifetime assumptions, a small reproducer or selftest, and the broader controls that remained green. Separate a production bug fix from an intentionally planted training fault.

Prepare a local selftest patch from your completed kernel exercise:

```bash
# Return to the repaired training branch and check for accidental whitespace.
cd "$KVM_DEV/linux-6.12"
git switch course
git diff --check
# Export only the intended selftest and Makefile changes relative to the import.
git diff course-source -- tools/testing/selftests/kvm/Makefile \
  tools/testing/selftests/kvm/x86_64/course_dirty_log_test.c \
  > "$KVM_DEV/course-selftest.diff"
# Review the exact change; the planted kernel bug must not be in this diff.
git diff --stat course-source -- tools/testing/selftests/kvm
# Check the source file again after any edits made during the exercises.
scripts/checkpatch.pl --no-tree --file \
  tools/testing/selftests/kvm/x86_64/course_dirty_log_test.c
```

This is a local review artifact. Do not submit the fictional `Course Lab` identity or the planted bug upstream. For a real contribution, use your own author identity and the project's current submission requirements, then explain the actual problem and validation. The  [kernel submitting-patches guide](https://docs.kernel.org/process/submitting-patches.html) and  [QEMU contribution guide](https://www.qemu.org/docs/master/devel/submitting-a-patch.html) describe those project workflows.

## 24.4 Completion interview — explain without looking at the answers

1. A command completes while interrupts are masked. What must happen when the driver unmasks the source?

2. DMA returns an error after crossing a RAM boundary. Is `DMA_RESULT=0` proof that RAM did not change?

3. Why does the migration negative control catch a missing `SCRATCH` field even if reset and ordinary reads still pass?

4. Why can the broken KVM kernel report the first page-0 write but miss the repeated page-0 write?

5. Why is an unrelated boot timeout a bisect skip rather than a bad result?

6. Why can `insmod` return success without proving that a PCI device passed its probe checks?

7. Which observed results would you need before claiming a worker-thread device or cross-version migration is correct?

**Worked answers:** (1) The pending condition remains latched; unmasking asserts the level until acknowledged. (2) No: a valid prefix may already have been written, so zero is a completion-contract value, not a rollback guarantee. (3) Reset starts from defaults, while migration must preserve the non-default source value; the destination assertion exposes the lost state. (4) Initial mapping/fault bookkeeping can mark the first write, but the missing MMU rearm leaves later writes undetectable through that existing mapping. (5) It does not establish whether the tested behavior regressed at that commit. (6) PCI driver registration and an individual device's probe result are distinct events; require the explicit driver success marker and data checks. (7) New concurrency/lifetime/reset tests, pending-work migration tests, and an explicit source/destination version matrix; none is supplied merely by passing today's synchronous, same-build fixture.

You are ready for a supervised first contribution when you can reproduce the green–red–green sequence, explain why each independent control changed or stayed green, navigate from a userspace operation into the right source function, and describe the limits of your evidence. A larger patch earns confidence through its own tests and review, not through a score attached to a reading plan.

## 24.5 Validation record for this extension

The executable results below were recorded on **23 September 2026**. The QEMU source pin is v11.1.1 and the kernel pin is v6.12. Kernel binaries and selftests were compiled in Ubuntu 24.04 userspace with GCC 13; the physical runner was Fedora 44 on Intel hardware with nested VMX available. The original installed host kernel was not replaced. QEMU was run from its private build directory.

| **Validation** | **Recorded result and scope** |
| --- | --- |
| QEMU device build and focused qtests | ABI, reset/INTx, DMA, and migration tests passed. Exact source integration, dependencies, configure/build, focused tests, and Meson integration were replayed in fresh pinned trees on Ubuntu 24.04 and Fedora. |
| QEMU negative controls | Wrong PCI device ID and omitted migrated `SCRATCH` state caused the intended tests to fail; restoration returned them to green. |
| DMA boundary behavior | Argument guards, valid transfers, bus-master disable, unmapped addresses, and a RAM-to-unassigned crossing were exercised; partial-write semantics are explicit. |
| New KVM selftest | Unlogged-slot `ENOENT`, flags-only enable, three synchronized writes, immediate clearing checks, and repeated-page rearm passed on good/repaired kernels. |
| Planted kernel regression | The repeated-write check failed with page 0 absent in phase 3; the default bitmap control failed while the independent manual/ring controls passed. |
| Selected upstream KVM controls | Applicable memory-slot checks, all three selected dirty-log modes, and bounded slot stress passed on the repaired kernel. Guest\_memfd subcases were skipped and are not covered. |
| Source-level diagnosis | Function-graph tracing and a hardware GDB breakpoint exposed the expected dirty-log path and slot 1; the appliance resumed and completed. |
| Bisect | Three built/booted candidates located exactly the planted fault; final test improvements preserved the same classifications. |
| Real guest integration | The Linux module passed MMIO, shared INTx, every byte of coherent DMA, and removal against the source-built QEMU device. |
| Review boundary | Functional results on the stated Intel setup; no claim of full-suite, AMD, cross-release migration, asynchronous-device, fuzzing, or production security validation. |

A final clean-room replay started from the verified release archive in a new Git repository. The printed commands built committed good, faulty, and repaired kernels; the strict harness returned 0, 1, and 0 respectively, then 0 for the repaired driver run. The faulty build had no dirty-tree suffix. Both kernel C listings survived the documented copy/indentation normalization byte-for-byte.

Complete source and every required command appear above. Preserve your own source pin, configuration, compiler version, test logs, negative-control failure, and repaired result when repeating the labs. That evidence is more useful for the next engineering decision than an unexplained “all green.”
