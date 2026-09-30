# Build and test a migration-safe QEMU PCI/MMIO device

**Goal:** add one small conventional PCI device to a pinned QEMU tree and prove its register ABI, reset behavior, INTx delivery, bounded DMA, and migration state with qtest. This continues chapters 13–15: you already know the KVM/QEMU boundary, can build QEMU out of tree, and can read C with explicit ownership and bounds.

**Safety boundary:** everything here is a user-process source build. Do not install the resulting QEMU over the distribution binary, attach host devices, or point it at a disk containing data you value. qtest starts disposable QEMU processes with 32 MiB of anonymous guest RAM. The final KVM command only realizes a paused, diskless machine.

**Time:** three to six focused sessions. Stop after each check; a later failure is easier to isolate when the previous contract is already green.

## 21.1 Freeze the device contract

The device is a conventional PCI function at `1234:11e9`, revision 1. BAR0 is 4 KiB. Every defined access is an aligned, little-endian 32-bit operation. Invalid sizes and unaligned accesses are rejected: reads return all ones and writes do nothing.

| **Offset** | **Name** | **Access** | **Meaning** |
| --- | --- | --- | --- |
| `0x00` | `ID` | RO | `0x4b564d44` (`KVMD`) |
| `0x04` | `VERSION` | RO | ABI version 1 |
| `0x08` | `SCRATCH` | RW | ordinary state for the first MMIO test |
| `0x0c` | `STATUS` | RO | bit 0 `DONE`, bit 1 `ERROR` |
| `0x10` | `IRQ_MASK` | RW | bit 0 enables completion INTx |
| `0x14` | `IRQ_STATUS` | R/W1C | bit 0 is a latched completion |
| `0x18` | `COMMAND` | WO | 1 `PING`, 2 `DMA_FILL`; other values finish with error |
| `0x20/0x24` | `DMA_ADDR_LO/HI` | RW | 64-bit device DMA address |
| `0x28` | `DMA_LEN` | RW | valid range 1–4096 bytes |
| `0x2c` | `DMA_PATTERN` | RW | four little-endian bytes repeated by `DMA_FILL` |
| `0x30` | `DMA_RESULT` | RO | full length only on complete success; zero on error |

`DMA_RESULT=0` does **not** mean that memory was unchanged. A multi-region `pci_dma_write()` can modify a valid prefix before a later address-space transaction fails. The qtest deliberately crosses from RAM into an unassigned range and permits that prefix to change.

![MMIO, DMA, and interrupt paths](assets/image10.png)

*Figure 13. The driver starts a command through MMIO; the device uses its PCI DMA address space for bytes and its masked INTx level for completion. The guest driver in chapter 23 tests this complete path.*

## 21.2 Lab Q1 — pin, patch, and build

1. **Install the minimal build environment.** On the Ubuntu 24.04 reference host, use the package names from QEMU's pinned build-environment document. QEMU 11.1.1 requires Meson 1.5 or newer; `configure` creates `pyvenv` and may fetch eligible Python build tools when the distribution Meson is older.

```bash
# Refresh package metadata before installing build-only dependencies.
sudo apt update
# Install QEMU's documented minimal Debian-family toolchain without extras.
sudo apt install --no-install-recommends \
  bash bc bison bzip2 ca-certificates flex gcc git libc6-dev \
  libfdt-dev libffi-dev libglib2.0-dev libpixman-1-dev make \
  meson ninja-build pkgconf python3 python3-venv sed tar
```

2. **Clone the exact source.** Use a new path. The guard prevents an accidental overwrite, and the full commit check makes a moved or mistyped tag fail closed.

```bash
# Keep the course build separate from packaged QEMU.
export QEMU_LAB="$HOME/src/qemu-course-v11.1.1"
# Refuse to reuse a path whose contents or configuration may be unknown.
test ! -e "$QEMU_LAB" || { echo "Refusing existing path: $QEMU_LAB" >&2; exit 1; }
# Create only the parent directory; git creates the qemu leaf.
mkdir -p "$(dirname "$QEMU_LAB")"
# Clone the official release tag with a shallow history.
git clone --depth 1 --branch v11.1.1 \
  https://gitlab.com/qemu-project/qemu.git "$QEMU_LAB/qemu"
# Enter the source tree before validating its identity.
cd "$QEMU_LAB/qemu"
# Pin the full release commit rather than trusting a short hash.
EXPECTED_QEMU_COMMIT=c3d48b7d1e89604920e5b81b91140c2ad39a1943
# Resolve HEAD once and compare it exactly.
ACTUAL_QEMU_COMMIT="$(git rev-parse HEAD)"
# Stop before patching if source identity differs.
test "$ACTUAL_QEMU_COMMIT" = "$EXPECTED_QEMU_COMMIT"
# Require HEAD to be exactly at the named release tag.
test "$(git describe --tags --exact-match HEAD)" = v11.1.1
```

3. **Create the three complete source files.** Copy the three code blocks in “Complete source” to these exact paths:

```text
include/hw/misc/kvm-course-pci.h
hw/misc/kvm-course-pci.c
tests/qtest/kvm-course-pci-test.c
```

4. **Connect the files to Kconfig and Meson.** Save and apply this exact patch from the QEMU source root. `git apply --check` is the guard: it changes nothing and must succeed before `git apply` mutates the tree.

```bash
# Keep the small build-system patch beside the isolated source tree.
COURSE_BUILD_PATCH="$QEMU_LAB/qemu-build-integration.patch"
# Write the exact v11.1.1-context patch; the quoted delimiter prevents expansion.
cat >"$COURSE_BUILD_PATCH" <<'PATCH'
diff --git a/hw/misc/Kconfig b/hw/misc/Kconfig
index 1543ee6..5d0c7a3 100644
--- a/hw/misc/Kconfig
+++ b/hw/misc/Kconfig
@@ -35,6 +35,11 @@ config EDU
     default y if TEST_DEVICES
     depends on PCI && MSI_NONBROKEN

+config KVM_COURSE_PCI
+    bool
+    default y if TEST_DEVICES
+    depends on PCI
+
 config I2C_ECHO
     bool
     default y if TEST_DEVICES
diff --git a/hw/misc/meson.build b/hw/misc/meson.build
index 23265f6..7bdb8fa 100644
--- a/hw/misc/meson.build
+++ b/hw/misc/meson.build
@@ -1,5 +1,6 @@
 system_ss.add(when: 'CONFIG_APPLESMC', if_true: files('applesmc.c'))
 system_ss.add(when: 'CONFIG_EDU', if_true: files('edu.c'))
+system_ss.add(when: 'CONFIG_KVM_COURSE_PCI', if_true: files('kvm-course-pci.c'))
 system_ss.add(when: 'CONFIG_FW_CFG_DMA', if_true: files('vmcoreinfo.c'))
 system_ss.add(when: 'CONFIG_ISA_DEBUG', if_true: files('debugexit.c'))
 system_ss.add(when: 'CONFIG_ISA_TESTDEV', if_true: files('pc-testdev.c'))
diff --git a/tests/qtest/meson.build b/tests/qtest/meson.build
index 56ff860..ac1e214 100644
--- a/tests/qtest/meson.build
+++ b/tests/qtest/meson.build
@@ -74,6 +74,7 @@ qtests_i386 = \
   (config_all_devices.has_key('CONFIG_WDT_IB700') ? ['wdt_ib700-test'] : []) +              \
   (config_all_devices.has_key('CONFIG_PVPANIC_ISA') ? ['pvpanic-test'] : []) +              \
   (config_all_devices.has_key('CONFIG_PVPANIC_PCI') ? ['pvpanic-pci-test'] : []) +          \
+  (config_all_devices.has_key('CONFIG_KVM_COURSE_PCI') ? ['kvm-course-pci-test'] : []) +    \
   (config_all_devices.has_key('CONFIG_HDA') ? ['intel-hda-test'] : []) +                    \
   (config_all_devices.has_key('CONFIG_I82801B11') ? ['i82801b11-test'] : []) +             \
   (config_all_devices.has_key('CONFIG_IOH3420') ? ['ioh3420-test'] : []) +                  \
PATCH
# Confirm all three hunks match the pinned source before changing any file.
git apply --check "$COURSE_BUILD_PATCH"
# Apply only after the read-only check succeeds.
git apply "$COURSE_BUILD_PATCH"
# Verify the device, model, and qtest entries are now present.
git diff --check
git diff -- hw/misc/Kconfig hw/misc/meson.build tests/qtest/meson.build
```

5. **Configure an isolated x86\_64 system-emulation build.** Eight jobs is a ceiling, not a requirement; lower it on a smaller host.

```bash
# Return to the lab root so source and build are siblings.
cd "$QEMU_LAB"
# Reuse only this lab's known build directory.
mkdir -p build
# Enter the out-of-tree build directory.
cd build
# Configure once; docs are unnecessary for this executable test lab.
if test ! -f build.ninja; then
  ../qemu/configure \
    --target-list=x86_64-softmmu \
    --enable-debug \
    --disable-docs
fi
# Build only the emulator and this qtest, with bounded parallelism.
ninja -j8 qemu-system-x86_64 tests/qtest/kvm-course-pci-test
```

**Check:** the compile exits 0, `./qemu-system-x86_64 --version` says 11.1.1, and `./qemu-system-x86_64 -device help | grep kvm-course-pci` finds the device. If configuration cannot find a dependency, fix the first missing dependency and rerun `configure`; do not install this binary system-wide.

## 21.3 Lab Q2 — test the PCI and MMIO ABI

*Refresher:* PCI configuration space identifies the function and describes BAR geometry. Firmware or a guest assigns a BAR address; the device model defines its size and callbacks. qtest's `libqos` helpers enable the function and map the BAR without booting firmware or an OS.

1. Read the shared header first. Treat offsets, bit meanings, sizes, and endianness as a guest-visible compatibility contract.

2. In the model, follow `kvm_course_realize()` into `memory_region_init_io()` and `pci_register_bar()`.

3. Follow the read/write callbacks. Notice that the `MemoryRegionOps` accepts sizes 1–8 so the callback itself can reject wrong sizes deterministically rather than let the memory core split them.

4. Run only the ABI test:

```bash
# Point qtest at the emulator built in this directory.
export QTEST_QEMU_BINARY="$PWD/qemu-system-x86_64"
# Bound the test and select only the PCI/MMIO ABI case.
timeout 30s ./tests/qtest/kvm-course-pci-test \
  -p /x86_64/kvm-course-pci/abi --tap
```

5. Read `test_abi()`: it checks IDs, revision, class, BAR size, reset value, scratch round trip, read-only writes, byte/word/qword rejection, unaligned access, and an unknown offset.

**Check:** TAP reports `ok 1 /x86_64/kvm-course-pci/abi`. For the tested red control, change `pc->device_id = 0x11e9;` in the model to `0x11ea`, rebuild the emulator, and rerun this focused test. It must abort at the expected `0x11e9` identity check. Restore `0x11e9`, rebuild, and require green before continuing.

## 21.4 Lab Q3 — reset and level-triggered INTx

*Refresher:* this INTx source is `irq_mask & irq_status`. Completion sets the pending bit even while masked. Enabling the mask later must raise the line. The guest acknowledges the event by writing 1 to the W1C status bit; only then may the level fall.

1. Follow `kvm_course_complete()` to the latched pending bit and `kvm_course_update_irq()` to `pci_set_irq()`.

2. Observe that every command first clears the previous completion and updates the line, so a new command has one well-defined terminal result.

3. Run the focused test:

```bash
# Reuse the exact built emulator selected in Lab Q2.
export QTEST_QEMU_BINARY="$PWD/qemu-system-x86_64"
# Exercise masked completion, unmask, W1C acknowledgement, and reset.
timeout 30s ./tests/qtest/kvm-course-pci-test \
  -p /x86_64/kvm-course-pci/irq-reset --tap
```

4. The test intercepts q35 IOAPIC inputs 16–23. It proves masked completion does not assert a physical line, unmasking does, W1C lowers it, and exactly one routed line is active.

5. It then dirties every mutable register, asserts the line, issues `system_reset`, and verifies every field is zero and the line is low.

**Check:** TAP reports `ok 1 /x86_64/kvm-course-pci/irq-reset`. If status passes but the line remains high, inspect every state transition that calls `kvm_course_update_irq()`.

## 21.5 Lab Q4 — bounded DMA and honest failure semantics

*Refresher:* a PCI DMA address is in the device's bus-master address space. It is not a host pointer. `pci_dma_write()` handles RAM/IOMMU translation and returns a `MemTxResult`; an error can occur after an earlier region was written.

1. Follow the three pre-DMA guards: length is nonzero, length is at most 4096, and `address + length - 1` cannot wrap.

2. Confirm the staging buffer is bounded and the pattern is explicitly converted to little endian before its bytes are repeated.

3. Confirm `DMA_RESULT` becomes the requested length only when `pci_dma_write()` returns `MEMTX_OK`. On error it becomes zero without promising rollback.

4. Run the focused test:

```bash
# Reuse the exact built emulator selected in Lab Q2.
export QTEST_QEMU_BINARY="$PWD/qemu-system-x86_64"
# Exercise successful DMA plus guarded and address-space failures.
timeout 30s ./tests/qtest/kvm-course-pci-test \
  -p /x86_64/kvm-course-pci/dma --tap
```

5. The positive case checks all 64 result bytes. Argument negatives cover zero, oversize, and arithmetic wrap. Transaction negatives cover an unmapped target and disabled PCI bus mastering.

6. The cross-boundary negative starts 32 bytes before the end of 32 MiB RAM and requests 64 bytes. It requires `DONE|ERROR` and result zero, while allowing the valid 32-byte prefix to be either original or written. The observed reference run printed `failed cross-boundary DMA left its valid prefix modified`.

**Check:** TAP reports `ok 1 /x86_64/kvm-course-pci/dma`. Be able to explain why checking `DMA_RESULT==0` before reusing a buffer is insufficient for transactional recovery.

## 21.6 Lab Q5 — migrate device state and interrupt behavior

![Device state and migration reconstruction](assets/image7.png)

*Figure 14. Save logical and PCI state, reconstruct outputs, and test a non-default pending completion at the destination.*

*Refresher:* `VMStateDescription` is a wire contract. Its version is independent of the MMIO ABI version even though both begin at 1 here. `VMSTATE_PCI_DEVICE` carries PCI parent state; the remaining entries carry every mutable register owned by this model.

1. Match every mutable state field to one VMState entry. Do not migrate the `MemoryRegion`: it is reconstructed by realization, not guest state.

2. Read `kvm_course_post_load()`. It masks reserved status/IRQ bits, then asks the PCI layer to reconcile the intended level from migrated mask and pending state.

3. Run the focused two-process migration test:

```bash
# Reuse the exact built emulator selected in Lab Q2.
export QTEST_QEMU_BINARY="$PWD/qemu-system-x86_64"
# Bound source-to-destination migration and select only its qtest.
timeout 30s ./tests/qtest/kvm-course-pci-test \
  -p /x86_64/kvm-course-pci/migration --tap
```

4. The test creates a private Unix socket, starts the incoming destination first, seeds every independent field on the source, asserts a completion, issues QMP `migrate`, and polls for at most ten seconds.

5. It verifies all fields at the destination. qtest's IRQ interceptor records edges rather than synthesizing a level that was restored before observation; the test therefore toggles the migrated mask to force a fresh edge, proves the pending completion drives the same routed line, then acknowledges it and verifies deassertion.

6. Perform the red test once: remove only `VMSTATE_UINT32(scratch, KVMCoursePCIState)`, rebuild the emulator, and rerun the migration test. It must fail with destination scratch `0x00000000` instead of `0xfeedc0de`. Restore the line, rebuild, and require green again.

**Check:** the normal case reports `ok 1 /x86_64/kvm-course-pci/migration`; the deliberate omission fails at the scratch assertion; the restored build passes. This is evidence that the test observes migrated device state rather than merely launching two processes.

## 21.7 Lab Q6 — run the integrated gate and a KVM realization smoke test

1. Run the registered Meson test. This proves the new test is part of QEMU's build graph rather than only a manually invoked binary.

```bash
# Run the registered x86_64 qtest and print child output on failure.
timeout 120s ./pyvenv/bin/meson test --print-errorlogs \
  qtest-x86_64/kvm-course-pci-test
```

2. Require one Meson test and four GLib subtests to pass. Then inspect the diff for whitespace errors.

```bash
# Check the source patch for whitespace damage.
git -C ../qemu diff --check
```

3. If `/dev/kvm` is available to your lab user, realize a paused diskless q35 machine under KVM and query PCI. This does not exercise guest MMIO, DMA, or interrupts; it closes only the “device realizes under KVM” gap.

```bash
# Bound a paused, diskless KVM process with only anonymous RAM.
timeout 10s ./qemu-system-x86_64 \
  -accel kvm \
  -machine q35 \
  -nodefaults \
  -display none \
  -m 64M \
  -device kvm-course-pci,addr=04.0 \
  -S \
  -qmp stdio <<'QMP'
{"execute":"qmp_capabilities"}
{"execute":"query-pci"}
{"execute":"quit"}
QMP
```

4. In `query-pci`, find decimal vendor 4660 (`0x1234`), device 4585 (`0x11e9`), interrupt pin 1, and BAR0 size 4096. An address of `-1` is expected here because `-S -nodefaults` never boots firmware to assign the BAR.

5. Preserve the source commit, configuration output, compiler versions, full TAP output, Meson output, mutation failure, and KVM query in the lab manifest.

**Pass:** all four qtests pass both directly and through Meson; the migration mutant fails for the expected missing field; `git diff --check` is clean; and the optional KVM query realizes the exact device. A guest-driver run is a separate integration layer: it should use `pcim_enable_device()`, `pci_set_master()`, `pcim_iomap_regions()`, `pcim_iomap_table()`, a shared INTx handler that reads and W1C-acknowledges `IRQ_STATUS`, and a coherent DMA buffer whose DMA address—not CPU pointer—is programmed into `DMA_ADDR_LO/HI`.

**Recovery and cleanup:** qtests and the KVM smoke command exit on their own and use anonymous memory. If a test is interrupted, first check for a process whose executable is this lab's `qemu-system-x86_64`; terminate only that PID. Keep the source and evidence. To reclaim build artifacts, verify the path and remove only `"$QEMU_LAB/build"`. Do not run `ninja install`.

## 21.8 Primary references

- [QEMU v11.1.1 tag and release commit](https://gitlab.com/qemu-project/qemu/-/tags/v11.1.1)

- [Pinned QEMU build-environment source](https://gitlab.com/qemu-project/qemu/-/blob/v11.1.1/docs/devel/build-environment.rst)

- [QEMU memory API](https://www.qemu.org/docs/master/devel/memory.html)

- [QTest device-emulation framework](https://www.qemu.org/docs/master/devel/testing/qtest.html)

- [QEMU migration framework and VMState](https://www.qemu.org/docs/master/devel/migration/main.html)

- [Pinned educational PCI model used only as a comparison](https://gitlab.com/qemu-project/qemu/-/blob/v11.1.1/hw/misc/edu.c)

## 21.9 Complete source

Save each block at the path shown before applying the three build-system insertions in Lab 1.

##### include/hw/misc/kvm-course-pci.h

```c
/*
 * Register ABI for the KVM course PCI device.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

#ifndef HW_MISC_KVM_COURSE_PCI_H
#define HW_MISC_KVM_COURSE_PCI_H

#define TYPE_KVM_COURSE_PCI "kvm-course-pci"

/* The single BAR is deliberately one page and uses 32-bit little-endian I/O. */
#define KVM_COURSE_BAR_SIZE          0x1000

/* Identity registers are read-only. */
#define KVM_COURSE_REG_ID            0x00
#define KVM_COURSE_REG_VERSION       0x04
/* SCRATCH is an ordinary read/write register used to learn MMIO. */
#define KVM_COURSE_REG_SCRATCH       0x08
/* STATUS is read-only and describes the last command. */
#define KVM_COURSE_REG_STATUS        0x0c
/* IRQ_MASK is read/write; IRQ_STATUS is read and write-one-to-clear. */
#define KVM_COURSE_REG_IRQ_MASK      0x10
#define KVM_COURSE_REG_IRQ_STATUS    0x14
/* COMMAND is write-only: a write starts and synchronously completes work. */
#define KVM_COURSE_REG_COMMAND       0x18
/* The DMA address is split into low and high 32-bit read/write registers. */
#define KVM_COURSE_REG_DMA_ADDR_LO   0x20
#define KVM_COURSE_REG_DMA_ADDR_HI   0x24
#define KVM_COURSE_REG_DMA_LEN       0x28
#define KVM_COURSE_REG_DMA_PATTERN   0x2c
/* RESULT is the full length on success and zero on error, not bytes written. */
#define KVM_COURSE_REG_DMA_RESULT    0x30

#define KVM_COURSE_ID                0x4b564d44U /* ASCII "KVMD". */
#define KVM_COURSE_VERSION           1U

#define KVM_COURSE_STATUS_DONE       (1U << 0)
#define KVM_COURSE_STATUS_ERROR      (1U << 1)

#define KVM_COURSE_IRQ_COMPLETION    (1U << 0)

#define KVM_COURSE_CMD_PING          1U
#define KVM_COURSE_CMD_DMA_FILL      2U

#define KVM_COURSE_DMA_MAX           4096U

#endif
```

##### hw/misc/kvm-course-pci.c

```c
/*
 * Small PCI/MMIO device for the KVM course.
 *
 * The device exposes a versioned register ABI, a masked level-triggered INTx
 * completion interrupt, and a bounded device-to-guest DMA command.  It is
 * intentionally synchronous so the labs can focus on correctness boundaries.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

#include "qemu/osdep.h"
#include "hw/misc/kvm-course-pci.h"
#include "hw/pci/pci_device.h"
#include "migration/vmstate.h"
#include "qemu/log.h"
#include "qemu/module.h"
#include "qom/object.h"

OBJECT_DECLARE_SIMPLE_TYPE(KVMCoursePCIState, KVM_COURSE_PCI)

struct KVMCoursePCIState {
    /* QOM embeds the parent PCI function as the first field. */
    PCIDevice parent_obj;
    /* This object backs BAR0 and dispatches accesses to the callbacks. */
    MemoryRegion mmio;
    /* Every mutable, guest-visible register is explicit device state. */
    uint32_t scratch;
    uint32_t status;
    uint32_t irq_mask;
    uint32_t irq_status;
    uint64_t dma_addr;
    uint32_t dma_len;
    uint32_t dma_pattern;
    uint32_t dma_result;
};

static void kvm_course_update_irq(KVMCoursePCIState *s)
{
    /* A pending completion reaches INTx only when its mask bit is enabled. */
    bool level = s->irq_mask & s->irq_status & KVM_COURSE_IRQ_COMPLETION;

    /* pci_set_irq() preserves PCI's level-triggered interrupt semantics. */
    pci_set_irq(&s->parent_obj, level);
}

static void kvm_course_complete(KVMCoursePCIState *s, bool error,
                                uint32_t result)
{
    /* Every recognized command reaches a terminal DONE state. */
    s->status = KVM_COURSE_STATUS_DONE;
    if (error) {
        s->status |= KVM_COURSE_STATUS_ERROR;
    }
    s->dma_result = result;
    /* Latch completion even while masked; enabling the mask asserts it later. */
    s->irq_status |= KVM_COURSE_IRQ_COMPLETION;
    kvm_course_update_irq(s);
}

static void kvm_course_dma_fill(KVMCoursePCIState *s)
{
    /* Bound host stack use before indexing this fixed-size staging buffer. */
    uint8_t buffer[KVM_COURSE_DMA_MAX];
    /* Define the byte stream independently of host endianness. */
    uint32_t le_pattern = cpu_to_le32(s->dma_pattern);
    MemTxResult result;
    size_t offset;

    /* Reject zero, oversize, and address-wrap requests before any DMA access. */
    if (s->dma_len == 0 || s->dma_len > KVM_COURSE_DMA_MAX ||
        s->dma_addr > UINT64_MAX - (s->dma_len - 1)) {
        kvm_course_complete(s, true, 0);
        return;
    }

    /* Repeat the four little-endian pattern bytes across the requested span. */
    for (offset = 0; offset < s->dma_len; offset++) {
        buffer[offset] = ((uint8_t *)&le_pattern)[offset % sizeof(le_pattern)];
    }

    /*
     * A failed address-space transaction may already have written a prefix.
     * DMA_RESULT therefore reports the full length only on complete success;
     * zero is an error marker, not a rollback or bytes-written guarantee.
     */
    result = pci_dma_write(&s->parent_obj, s->dma_addr, buffer, s->dma_len);
    kvm_course_complete(s, result != MEMTX_OK,
                        result == MEMTX_OK ? s->dma_len : 0);
}

static void kvm_course_command(KVMCoursePCIState *s, uint32_t command)
{
    /* Starting a command retires the prior completion and lowers INTx. */
    s->status = 0;
    s->dma_result = 0;
    s->irq_status &= ~KVM_COURSE_IRQ_COMPLETION;
    kvm_course_update_irq(s);

    /* Unknown opcodes complete with ERROR instead of hanging the guest. */
    switch (command) {
    case KVM_COURSE_CMD_PING:
        kvm_course_complete(s, false, 0);
        break;
    case KVM_COURSE_CMD_DMA_FILL:
        kvm_course_dma_fill(s);
        break;
    default:
        kvm_course_complete(s, true, 0);
        break;
    }
}

static uint64_t kvm_course_mmio_read(void *opaque, hwaddr addr, unsigned size)
{
    KVMCoursePCIState *s = opaque;

    /* The ABI accepts only aligned 32-bit accesses. */
    if (size != sizeof(uint32_t) || (addr & (sizeof(uint32_t) - 1))) {
        qemu_log_mask(LOG_GUEST_ERROR,
                      TYPE_KVM_COURSE_PCI ": invalid %u-byte read at 0x%"
                      HWADDR_PRIx "\n", size, addr);
        /* Return all ones, which truncates correctly for every invalid size. */
        return UINT64_MAX;
    }

    /* Read-only and mutable registers share one explicit dispatch table. */
    switch (addr) {
    case KVM_COURSE_REG_ID:
        return KVM_COURSE_ID;
    case KVM_COURSE_REG_VERSION:
        return KVM_COURSE_VERSION;
    case KVM_COURSE_REG_SCRATCH:
        return s->scratch;
    case KVM_COURSE_REG_STATUS:
        return s->status;
    case KVM_COURSE_REG_IRQ_MASK:
        return s->irq_mask;
    case KVM_COURSE_REG_IRQ_STATUS:
        return s->irq_status;
    case KVM_COURSE_REG_DMA_ADDR_LO:
        return s->dma_addr;
    case KVM_COURSE_REG_DMA_ADDR_HI:
        return s->dma_addr >> 32;
    case KVM_COURSE_REG_DMA_LEN:
        return s->dma_len;
    case KVM_COURSE_REG_DMA_PATTERN:
        return s->dma_pattern;
    case KVM_COURSE_REG_DMA_RESULT:
        return s->dma_result;
    default:
        qemu_log_mask(LOG_GUEST_ERROR,
                      TYPE_KVM_COURSE_PCI ": unknown read at 0x%" HWADDR_PRIx
                      "\n", addr);
        return UINT32_MAX;
    }
}

static void kvm_course_mmio_write(void *opaque, hwaddr addr, uint64_t value,
                                  unsigned size)
{
    KVMCoursePCIState *s = opaque;
    uint32_t val = value;

    /* Ignore invalid writes without partially updating a register. */
    if (size != sizeof(uint32_t) || (addr & (sizeof(uint32_t) - 1))) {
        qemu_log_mask(LOG_GUEST_ERROR,
                      TYPE_KVM_COURSE_PCI ": invalid %u-byte write at 0x%"
                      HWADDR_PRIx "\n", size, addr);
        return;
    }

    switch (addr) {
    case KVM_COURSE_REG_SCRATCH:
        s->scratch = val;
        break;
    case KVM_COURSE_REG_IRQ_MASK:
        /* Reserved mask bits always read back as zero. */
        s->irq_mask = val & KVM_COURSE_IRQ_COMPLETION;
        kvm_course_update_irq(s);
        break;
    case KVM_COURSE_REG_IRQ_STATUS:
        /* W1C lets software acknowledge only the events it handled. */
        s->irq_status &= ~(val & KVM_COURSE_IRQ_COMPLETION);
        kvm_course_update_irq(s);
        break;
    case KVM_COURSE_REG_COMMAND:
        kvm_course_command(s, val);
        break;
    case KVM_COURSE_REG_DMA_ADDR_LO:
        s->dma_addr = (s->dma_addr & 0xffffffff00000000ULL) | val;
        break;
    case KVM_COURSE_REG_DMA_ADDR_HI:
        s->dma_addr = (s->dma_addr & 0x00000000ffffffffULL) |
                      ((uint64_t)val << 32);
        break;
    case KVM_COURSE_REG_DMA_LEN:
        s->dma_len = val;
        break;
    case KVM_COURSE_REG_DMA_PATTERN:
        s->dma_pattern = val;
        break;
    default:
        qemu_log_mask(LOG_GUEST_ERROR,
                      TYPE_KVM_COURSE_PCI ": ignored write at 0x%" HWADDR_PRIx
                      "\n", addr);
        break;
    }
}

static const MemoryRegionOps kvm_course_mmio_ops = {
    .read = kvm_course_mmio_read,
    .write = kvm_course_mmio_write,
    .endianness = DEVICE_LITTLE_ENDIAN,
    /* Let callbacks see wrong sizes and alignments so rejection is testable. */
    .valid = {
        .min_access_size = 1,
        .max_access_size = 8,
        .unaligned = true,
    },
    .impl = {
        .min_access_size = 1,
        .max_access_size = 8,
        .unaligned = true,
    },
};

static void kvm_course_reset(DeviceState *dev)
{
    KVMCoursePCIState *s = KVM_COURSE_PCI(dev);

    /* Reset every guest-visible mutable field, including the IRQ source. */
    s->scratch = 0;
    s->status = 0;
    s->irq_mask = 0;
    s->irq_status = 0;
    s->dma_addr = 0;
    s->dma_len = 0;
    s->dma_pattern = 0;
    s->dma_result = 0;
    /* Deassert a previously active level interrupt. */
    kvm_course_update_irq(s);
}

static int kvm_course_post_load(void *opaque, int version_id)
{
    KVMCoursePCIState *s = opaque;

    /* Sanitize reserved bits before rebuilding state derived from them. */
    s->status &= KVM_COURSE_STATUS_DONE | KVM_COURSE_STATUS_ERROR;
    s->irq_mask &= KVM_COURSE_IRQ_COMPLETION;
    s->irq_status &= KVM_COURSE_IRQ_COMPLETION;
    /* Reconcile the output level with the migrated mask and pending bits. */
    kvm_course_update_irq(s);
    return 0;
}

static const VMStateDescription vmstate_kvm_course_pci = {
    /* Version 1 is the migration wire contract for this course device. */
    .name = TYPE_KVM_COURSE_PCI,
    .version_id = 1,
    .minimum_version_id = 1,
    .post_load = kvm_course_post_load,
    .fields = (const VMStateField[]) {
        /* Preserve PCI configuration first, then every mutable ABI register. */
        VMSTATE_PCI_DEVICE(parent_obj, KVMCoursePCIState),
        VMSTATE_UINT32(scratch, KVMCoursePCIState),
        VMSTATE_UINT32(status, KVMCoursePCIState),
        VMSTATE_UINT32(irq_mask, KVMCoursePCIState),
        VMSTATE_UINT32(irq_status, KVMCoursePCIState),
        VMSTATE_UINT64(dma_addr, KVMCoursePCIState),
        VMSTATE_UINT32(dma_len, KVMCoursePCIState),
        VMSTATE_UINT32(dma_pattern, KVMCoursePCIState),
        VMSTATE_UINT32(dma_result, KVMCoursePCIState),
        VMSTATE_END_OF_LIST()
    },
};

static void kvm_course_realize(PCIDevice *pdev, Error **errp)
{
    KVMCoursePCIState *s = KVM_COURSE_PCI(pdev);

    /* Advertise PCI interrupt pin A for conventional level-triggered INTx. */
    pci_config_set_interrupt_pin(pdev->config, 1);
    /* Bind BAR0 accesses to the callbacks and make the region exactly 4 KiB. */
    memory_region_init_io(&s->mmio, OBJECT(s), &kvm_course_mmio_ops, s,
                          TYPE_KVM_COURSE_PCI "-mmio",
                          KVM_COURSE_BAR_SIZE);
    /* Expose that region as non-prefetchable memory BAR0. */
    pci_register_bar(pdev, 0, PCI_BASE_ADDRESS_SPACE_MEMORY, &s->mmio);
}

static void kvm_course_class_init(ObjectClass *klass, const void *data)
{
    DeviceClass *dc = DEVICE_CLASS(klass);
    PCIDeviceClass *pc = PCI_DEVICE_CLASS(klass);

    /* These fields become the PCI identity visible to firmware and drivers. */
    pc->realize = kvm_course_realize;
    pc->vendor_id = PCI_VENDOR_ID_QEMU;
    pc->device_id = 0x11e9;
    pc->revision = KVM_COURSE_VERSION;
    pc->class_id = PCI_CLASS_OTHERS;
    /* qdev invokes these hooks for migration and reset. */
    dc->vmsd = &vmstate_kvm_course_pci;
    device_class_set_legacy_reset(dc, kvm_course_reset);
    set_bit(DEVICE_CATEGORY_MISC, dc->categories);
}

static const TypeInfo kvm_course_types[] = {
    {
        .name = TYPE_KVM_COURSE_PCI,
        /* Inherit PCI behavior and opt into conventional PCI connectivity. */
        .parent = TYPE_PCI_DEVICE,
        .instance_size = sizeof(KVMCoursePCIState),
        .class_init = kvm_course_class_init,
        .interfaces = (const InterfaceInfo[]) {
            { INTERFACE_CONVENTIONAL_PCI_DEVICE },
            { }
        },
    },
};

DEFINE_TYPES(kvm_course_types)
```

##### tests/qtest/kvm-course-pci-test.c

```c
/*
 * qtests for the KVM course PCI device.
 *
 * SPDX-License-Identifier: GPL-2.0-or-later
 */

#include "qemu/osdep.h"
#include "hw/misc/kvm-course-pci.h"
#include "hw/pci/pci.h"
#include "hw/pci/pci_regs.h"
#include "libqos/pci.h"
#include "libqos/pci-pc.h"
#include "libqtest.h"
#include "qobject/qdict.h"

/* q35 places the device at 00:04.0 and gives qtest 32 MiB of guest RAM. */
#define COURSE_DEVFN QPCI_DEVFN(4, 0)
#define COURSE_DMA_GPA 0x00100000ULL
#define COURSE_RAM_END 0x02000000ULL
#define COURSE_MACHINE \
    "-machine q35 -nodefaults -display none -m 32M " \
    "-device " TYPE_KVM_COURSE_PCI ",addr=04.0"

typedef struct CourseFixture {
    /* Keep one handle for QMP/qtest, the PCI bus, function, and mapped BAR. */
    QTestState *qts;
    QPCIBus *bus;
    QPCIDevice *dev;
    QPCIBar bar;
} CourseFixture;

static CourseFixture *course_start(const char *extra)
{
    CourseFixture *f = g_new0(CourseFixture, 1);

    /* qtest starts a user-process QEMU; no firmware or guest OS is needed. */
    f->qts = qtest_initf(COURSE_MACHINE " %s", extra ? extra : "");
    /* libqos discovers the function, enables memory and bus mastering, and maps BAR0. */
    f->bus = qpci_new_pc(f->qts, NULL);
    f->dev = qpci_device_find(f->bus, COURSE_DEVFN);
    g_assert_nonnull(f->dev);
    qpci_device_enable(f->dev);
    f->bar = qpci_iomap(f->dev, 0, NULL);
    return f;
}

static void course_stop(CourseFixture *f)
{
    qpci_iounmap(f->dev, f->bar);
    g_free(f->dev);
    qpci_free_pc(f->bus);
    qtest_quit(f->qts);
    g_free(f);
}

static uint32_t course_read(CourseFixture *f, uint64_t offset)
{
    return qpci_io_readl(f->dev, f->bar, offset);
}

static void course_write(CourseFixture *f, uint64_t offset, uint32_t value)
{
    qpci_io_writel(f->dev, f->bar, offset, value);
}

static int course_asserted_irq(QTestState *qts)
{
    int found = -1;
    int irq;

    /* q35 routes PCI INTx to one of the IOAPIC inputs 16 through 23. */
    for (irq = 16; irq < 24; irq++) {
        if (qtest_get_irq(qts, irq)) {
            g_assert_cmpint(found, ==, -1);
            found = irq;
        }
    }
    return found;
}

static void test_abi(void)
{
    CourseFixture *f = course_start(NULL);
    uint64_t bar_size;

    /* First freeze the discoverable PCI identity and BAR geometry. */
    g_assert_cmphex(qpci_config_readw(f->dev, PCI_VENDOR_ID), ==,
                    PCI_VENDOR_ID_QEMU);
    g_assert_cmphex(qpci_config_readw(f->dev, PCI_DEVICE_ID), ==, 0x11e9);
    g_assert_cmphex(qpci_config_readb(f->dev, PCI_REVISION_ID), ==,
                    KVM_COURSE_VERSION);
    g_assert_cmphex(qpci_config_readw(f->dev, PCI_CLASS_DEVICE), ==,
                    PCI_CLASS_OTHERS);

    qpci_iounmap(f->dev, f->bar);
    f->bar = qpci_iomap(f->dev, 0, &bar_size);
    g_assert_cmpuint(bar_size, ==, KVM_COURSE_BAR_SIZE);

    /* Then verify reset values and ordinary 32-bit read/write behavior. */
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_ID), ==, KVM_COURSE_ID);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_VERSION), ==,
                     KVM_COURSE_VERSION);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0);

    course_write(f, KVM_COURSE_REG_SCRATCH, 0x12345678);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0x12345678);

    /* Writes to read-only identity registers must have no effect. */
    course_write(f, KVM_COURSE_REG_ID, 0);
    course_write(f, KVM_COURSE_REG_VERSION, 0);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_ID), ==, KVM_COURSE_ID);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_VERSION), ==,
                     KVM_COURSE_VERSION);

    /* Wrong-size and unaligned accesses have deterministic rejection behavior. */
    qpci_io_writeb(f->dev, f->bar, KVM_COURSE_REG_SCRATCH, 0xaa);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0x12345678);
    qpci_io_writew(f->dev, f->bar, KVM_COURSE_REG_SCRATCH, 0xbbcc);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0x12345678);
    qpci_io_writeq(f->dev, f->bar, KVM_COURSE_REG_SCRATCH,
                   0x1122334455667788ULL);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0x12345678);
    course_write(f, KVM_COURSE_REG_SCRATCH + 1, 0xaabbccdd);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0x12345678);
    g_assert_cmphex(qpci_io_readb(f->dev, f->bar,
                                 KVM_COURSE_REG_SCRATCH), ==, 0xff);
    g_assert_cmphex(qpci_io_readw(f->dev, f->bar,
                                 KVM_COURSE_REG_SCRATCH), ==, 0xffff);
    g_assert_cmphex(qpci_io_readq(f->dev, f->bar,
                                 KVM_COURSE_REG_SCRATCH), ==, UINT64_MAX);
    g_assert_cmphex(course_read(f, KVM_COURSE_REG_SCRATCH + 1), ==,
                    UINT32_MAX);
    g_assert_cmphex(course_read(f, 0x34), ==, UINT32_MAX);

    course_stop(f);
}

static void test_irq_and_reset(void)
{
    CourseFixture *f = course_start(NULL);
    int irq;

    /* Intercept q35 IOAPIC inputs so qtest can observe the physical INTx line. */
    qtest_irq_intercept_in(f->qts, "ioapic");
    g_assert_cmpint(course_asserted_irq(f->qts), ==, -1);

    /* A masked completion latches pending status without asserting INTx. */
    course_write(f, KVM_COURSE_REG_COMMAND, KVM_COURSE_CMD_PING);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_STATUS), ==,
                     KVM_COURSE_STATUS_DONE);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_IRQ_STATUS), ==,
                     KVM_COURSE_IRQ_COMPLETION);
    g_assert_cmpint(course_asserted_irq(f->qts), ==, -1);

    /* Unmasking an already-pending event raises the level; W1C lowers it. */
    course_write(f, KVM_COURSE_REG_IRQ_MASK, KVM_COURSE_IRQ_COMPLETION);
    irq = course_asserted_irq(f->qts);
    g_assert_cmpint(irq, >=, 16);

    course_write(f, KVM_COURSE_REG_IRQ_STATUS,
                 KVM_COURSE_IRQ_COMPLETION);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_IRQ_STATUS), ==, 0);
    g_assert_cmpint(course_asserted_irq(f->qts), ==, -1);

    /* Dirty every register and assert INTx before exercising system reset. */
    course_write(f, KVM_COURSE_REG_SCRATCH, 0xcafef00d);
    course_write(f, KVM_COURSE_REG_DMA_ADDR_LO, 0x12345678);
    course_write(f, KVM_COURSE_REG_DMA_ADDR_HI, 0x9abcdef0);
    course_write(f, KVM_COURSE_REG_DMA_LEN, 64);
    course_write(f, KVM_COURSE_REG_DMA_PATTERN, 0xa5a5a5a5);
    course_write(f, KVM_COURSE_REG_COMMAND, KVM_COURSE_CMD_PING);
    g_assert_cmpint(course_asserted_irq(f->qts), ==, irq);

    /* A QMP system reset must restore every field and deassert the IRQ. */
    qtest_system_reset(f->qts);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_SCRATCH), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_STATUS), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_IRQ_MASK), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_IRQ_STATUS), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_ADDR_LO), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_ADDR_HI), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_LEN), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_PATTERN), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_RESULT), ==, 0);
    g_assert_cmpint(course_asserted_irq(f->qts), ==, -1);

    course_stop(f);
}

static void course_program_dma(CourseFixture *f, uint64_t addr,
                               uint32_t len, uint32_t pattern)
{
    course_write(f, KVM_COURSE_REG_DMA_ADDR_LO, addr);
    course_write(f, KVM_COURSE_REG_DMA_ADDR_HI, addr >> 32);
    course_write(f, KVM_COURSE_REG_DMA_LEN, len);
    course_write(f, KVM_COURSE_REG_DMA_PATTERN, pattern);
    course_write(f, KVM_COURSE_REG_COMMAND, KVM_COURSE_CMD_DMA_FILL);
}

static void course_assert_dma_error(CourseFixture *f, uint64_t addr,
                                    uint32_t len)
{
    course_program_dma(f, addr, len, 0x44332211);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_STATUS), ==,
                     KVM_COURSE_STATUS_DONE | KVM_COURSE_STATUS_ERROR);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_RESULT), ==, 0);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_IRQ_STATUS), ==,
                     KVM_COURSE_IRQ_COMPLETION);
}

static void test_dma(void)
{
    CourseFixture *f = course_start(NULL);
    uint8_t crossing[64];
    uint8_t before[64];
    uint8_t after[64];
    bool crossing_prefix_changed = false;
    uint16_t pci_command;
    size_t i;

    /* Positive control: bus-master DMA writes the repeating LE byte pattern. */
    memset(before, 0xa5, sizeof(before));
    qtest_memwrite(f->qts, COURSE_DMA_GPA, before, sizeof(before));
    course_program_dma(f, COURSE_DMA_GPA, sizeof(after), 0x44332211);
    qtest_memread(f->qts, COURSE_DMA_GPA, after, sizeof(after));

    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_STATUS), ==,
                     KVM_COURSE_STATUS_DONE);
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_DMA_RESULT), ==,
                     sizeof(after));
    g_assert_cmpuint(course_read(f, KVM_COURSE_REG_IRQ_STATUS), ==,
                     KVM_COURSE_IRQ_COMPLETION);
    for (i = 0; i < sizeof(after); i++) {
        static const uint8_t expected[] = { 0x11, 0x22, 0x33, 0x44 };
        g_assert_cmphex(after[i], ==, expected[i % sizeof(expected)]);
    }

    /* Argument guards reject these requests before calling pci_dma_write(). */
    course_assert_dma_error(f, COURSE_DMA_GPA, 0);
    course_assert_dma_error(f, COURSE_DMA_GPA, KVM_COURSE_DMA_MAX + 1);
    course_assert_dma_error(f, UINT64_MAX - 1, 4);

    /*
     * This request crosses from RAM into an unassigned address.  The command
     * must fail, but the valid prefix may already have changed: DMA writes are
     * not transactions and DMA_RESULT=0 does not promise rollback.
     */
    memset(before, 0xa5, sizeof(before));
    qtest_memwrite(f->qts, COURSE_RAM_END - 32, before, 32);
    course_assert_dma_error(f, COURSE_RAM_END - 32, sizeof(crossing));
    qtest_memread(f->qts, COURSE_RAM_END - 32, crossing, 32);
    for (i = 0; i < 32; i++) {
        static const uint8_t expected[] = { 0x11, 0x22, 0x33, 0x44 };

        g_assert_true(crossing[i] == before[i] ||
                      crossing[i] == expected[i % sizeof(expected)]);
        crossing_prefix_changed |= crossing[i] != before[i];
    }
    g_test_message("failed cross-boundary DMA left its valid prefix %s",
                   crossing_prefix_changed ? "modified" : "unchanged");

    /* A wholly unmapped target reports an address-space transaction error. */
    memset(before, 0x5a, sizeof(before));
    qtest_memwrite(f->qts, COURSE_DMA_GPA, before, sizeof(before));
    course_assert_dma_error(f, 0x40000000, sizeof(before));
    qtest_memread(f->qts, COURSE_DMA_GPA, after, sizeof(after));
    g_assert_cmpmem(after, sizeof(after), before, sizeof(before));

    /* Clearing PCI bus mastering makes the same otherwise-valid DMA fail. */
    pci_command = qpci_config_readw(f->dev, PCI_COMMAND);
    qpci_config_writew(f->dev, PCI_COMMAND,
                       pci_command & ~PCI_COMMAND_MASTER);
    course_assert_dma_error(f, COURSE_DMA_GPA, sizeof(before));
    qtest_memread(f->qts, COURSE_DMA_GPA, after, sizeof(after));
    g_assert_cmpmem(after, sizeof(after), before, sizeof(before));

    course_stop(f);
}

static void wait_for_migration(QTestState *source)
{
    unsigned int attempt;

    /* Bound polling to ten seconds so a broken migration cannot hang CI. */
    for (attempt = 0; attempt < 1000; attempt++) {
        QDict *response = qtest_qmp_assert_success_ref(
            source, "{ 'execute': 'query-migrate' }");
        const char *status = qdict_get_str(response, "status");

        if (g_str_equal(status, "completed")) {
            qobject_unref(response);
            return;
        }
        if (!g_str_equal(status, "setup") &&
            !g_str_equal(status, "active") &&
            !g_str_equal(status, "device") &&
            !g_str_equal(status, "wait-unplug")) {
            g_error("migration ended with status '%s'", status);
        }
        qobject_unref(response);
        g_usleep(10000);
    }
    g_error("migration did not complete within 10 seconds");
}

static void test_migration(void)
{
    g_autoptr(GError) error = NULL;
    g_autofree char *directory = NULL;
    g_autofree char *socket_path = NULL;
    g_autofree char *uri = NULL;
    g_autofree char *incoming = NULL;
    CourseFixture *source;
    CourseFixture *destination;
    int source_irq;

    /* Give source and destination QEMU processes a private migration socket. */
    directory = g_dir_make_tmp("kvm-course-pci-XXXXXX", &error);
    g_assert_no_error(error);
    g_assert_nonnull(directory);
    socket_path = g_build_filename(directory, "migration.sock", NULL);
    uri = g_strdup_printf("unix:%s", socket_path);
    incoming = g_strdup_printf("-incoming %s", uri);

    /* The destination must listen before the source issues migrate. */
    destination = course_start(incoming);
    qtest_irq_intercept_in(destination->qts, "ioapic");
    source = course_start(NULL);
    qtest_irq_intercept_in(source->qts, "ioapic");

    /* Seed all independent fields plus a masked, asserted completion IRQ. */
    course_write(source, KVM_COURSE_REG_SCRATCH, 0xfeedc0de);
    course_write(source, KVM_COURSE_REG_DMA_ADDR_LO, 0x55667788);
    course_write(source, KVM_COURSE_REG_DMA_ADDR_HI, 0x11223344);
    course_write(source, KVM_COURSE_REG_DMA_LEN, 128);
    course_write(source, KVM_COURSE_REG_DMA_PATTERN, 0xa1b2c3d4);
    course_write(source, KVM_COURSE_REG_IRQ_MASK,
                 KVM_COURSE_IRQ_COMPLETION);
    course_write(source, KVM_COURSE_REG_COMMAND, KVM_COURSE_CMD_PING);
    source_irq = course_asserted_irq(source->qts);
    g_assert_cmpint(source_irq, >=, 16);

    /* QMP drives an end-to-end migration over the local Unix socket. */
    qtest_qmp_assert_success(source->qts,
                            "{ 'execute': 'migrate', "
                            "  'arguments': { 'uri': %s } }", uri);
    wait_for_migration(source->qts);

    /* Read the destination device to prove the VMState wire contract. */
    g_assert_cmphex(course_read(destination, KVM_COURSE_REG_SCRATCH), ==,
                    0xfeedc0de);
    g_assert_cmphex(course_read(destination, KVM_COURSE_REG_DMA_ADDR_LO), ==,
                    0x55667788);
    g_assert_cmphex(course_read(destination, KVM_COURSE_REG_DMA_ADDR_HI), ==,
                    0x11223344);
    g_assert_cmpuint(course_read(destination, KVM_COURSE_REG_DMA_LEN), ==, 128);
    g_assert_cmphex(course_read(destination, KVM_COURSE_REG_DMA_PATTERN), ==,
                    0xa1b2c3d4);
    g_assert_cmpuint(course_read(destination, KVM_COURSE_REG_STATUS), ==,
                     KVM_COURSE_STATUS_DONE);
    g_assert_cmpuint(course_read(destination, KVM_COURSE_REG_IRQ_MASK), ==,
                     KVM_COURSE_IRQ_COMPLETION);
    g_assert_cmpuint(course_read(destination, KVM_COURSE_REG_IRQ_STATUS), ==,
                     KVM_COURSE_IRQ_COMPLETION);
    /*
     * qtest's IRQ interceptor observes edges, not the IOAPIC level restored
     * before interception state is queried.  Toggle the migrated mask to
     * force a new edge and prove the migrated pending bit drives the line.
     */
    course_write(destination, KVM_COURSE_REG_IRQ_MASK, 0);
    g_assert_cmpint(course_asserted_irq(destination->qts), ==, -1);
    course_write(destination, KVM_COURSE_REG_IRQ_MASK,
                 KVM_COURSE_IRQ_COMPLETION);
    g_assert_cmpint(course_asserted_irq(destination->qts), ==, source_irq);

    course_write(destination, KVM_COURSE_REG_IRQ_STATUS,
                 KVM_COURSE_IRQ_COMPLETION);
    g_assert_cmpint(course_asserted_irq(destination->qts), ==, -1);

    course_stop(source);
    course_stop(destination);
    if (g_unlink(socket_path) < 0) {
        g_assert_cmpint(errno, ==, ENOENT);
    }
    g_assert_cmpint(g_rmdir(directory), ==, 0);
}

int main(int argc, char **argv)
{
    /* GLib provides isolation, filtering, TAP output, and assertion failures. */
    g_test_init(&argc, &argv, NULL);
    qtest_add_func("/kvm-course-pci/abi", test_abi);
    qtest_add_func("/kvm-course-pci/irq-reset", test_irq_and_reset);
    qtest_add_func("/kvm-course-pci/dma", test_dma);
    qtest_add_func("/kvm-course-pci/migration", test_migration);
    return g_test_run();
}
```
