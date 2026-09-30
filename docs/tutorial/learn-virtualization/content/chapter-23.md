# Connect the QEMU device to a real Linux driver

qtest gives precise control over a device model, but it does not prove that Linux can bind a driver, receive an interrupt, and use the DMA API with that model. This lab supplies that integration check. Reuse the **repaired** Linux 6.12 image and the private QEMU build from the preceding chapters.

## 23.1 Understand the ownership before writing the driver

The PCI core discovers the function and calls our `probe` for vendor/device `1234:11e9`. The driver enables it, claims and maps BAR0, chooses a DMA mask, allocates a coherent buffer, registers a shared INTx handler, and only then enables bus mastering and commands. Managed allocations simplify cleanup, but they do not stop a device or synchronize a running interrupt handler for you. The  [Linux PCI driver guide](https://docs.kernel.org/PCI/pci.html) describes that lifecycle.

A coherent allocation returns two values: a CPU pointer for accessing the buffer and a `dma_addr_t` for programming the device. Do not cast the pointer into a DMA address, and do not call `dma_map_single` on memory already returned by the coherent allocation API. Coherence does not remove ordering requirements; the example orders memory around command/completion and uses `readl`/`writel` for MMIO. See the  [DMA API guide](https://docs.kernel.org/core-api/dma-api-howto.html).

INTx is shared and level-triggered. The handler first checks whether this device has a pending condition, returns `IRQ_NONE` for somebody else's interrupt, acknowledges its own W1C status, and wakes a completion object. The probe waits in process context with a five-second limit; the interrupt handler never sleeps.

The model completes commands synchronously. During removal, the driver masks and acknowledges the interrupt, reads back to flush posted writes, waits for an active handler, and disables bus mastering before resources are released. A production asynchronous device would additionally need a device-specific stop/cancel procedure and proof that DMA has ended. This small example does not supply that missing protocol for arbitrary hardware.

## 23.2 Lab D1 — build the guest module

Create `$KVM_DEV/driver/course_guest.c` with the complete listing below. Copy the shared register header from chapter 21; the register ABI has one shared definition for the model, test, and driver. If the kernel builder is a separate VM, copy that header to its driver directory too; the first copy command assumes the QEMU source is available in the same build environment.

```c
# Create a directory owned only by this driver exercise.
mkdir -p "$KVM_DEV/driver"
# Share the exact device register definitions, rather than retyping constants.
cp "$QEMU_LAB/qemu/include/hw/misc/kvm-course-pci.h" "$KVM_DEV/driver/"
# Tell Kbuild to produce one external module from course_guest.c.
printf 'obj-m += course_guest.o\n' > "$KVM_DEV/driver/Makefile"

// SPDX-License-Identifier: GPL-2.0-only
/* A Linux 6.12 smoke-test driver for the synchronous course PCI device. */

/* Completions let process context wait for the interrupt handler. */
#include <linux/completion.h>
/* Coherent DMA allocation returns both a CPU pointer and a DMA address. */
#include <linux/dma-mapping.h>
/* IRQ registration and the IRQ_NONE/IRQ_HANDLED return values. */
#include <linux/interrupt.h>
/* Module metadata and PCI driver registration. */
#include <linux/module.h>
#include <linux/pci.h>
/* Share the exact register definitions with the QEMU device and qtests. */
#include "kvm-course-pci.h"

/* Keep the integration transfer small enough to inspect byte by byte. */
#define TEST_BYTES 64

/* These objects belong to one bound PCI function. */
struct course_guest {
        /* Kernel mapping of BAR0; access it only with readl()/writel(). */
        void __iomem *bar;
        /* IRQ-to-process notification; never sleep in the IRQ handler. */
        struct completion done;
        /* CPU address of coherent memory; this is not the device's address. */
        u8 *buffer;
        /* DMA address returned by the DMA API, including any IOMMU mapping. */
        dma_addr_t dma;
};

/* A shared INTx line may have interrupted us for another device. */
static irqreturn_t course_irq(int irq, void *opaque)
{
        /* Recover this PCI function's state from request_irq's argument. */
        struct course_guest *s = opaque;

        /* Check ownership before acknowledging anything on a shared line. */
        if (!(readl(s->bar + KVM_COURSE_REG_IRQ_STATUS) &
              KVM_COURSE_IRQ_COMPLETION))
                return IRQ_NONE;
        /* W1C means writing one clears the latched completion condition. */
        writel(KVM_COURSE_IRQ_COMPLETION, s->bar + KVM_COURSE_REG_IRQ_STATUS);
        /* Wake the waiter only after the device's interrupt was acknowledged. */
        complete(&s->done);
        return IRQ_HANDLED;
}

/* This helper runs in process context, where waiting is allowed. */
static int course_command(struct course_guest *s, u32 command)
{
        /* Each command must receive a new completion notification. */
        reinit_completion(&s->done);
        /* Order prior coherent-memory stores before starting the device. */
        dma_wmb();
        /* An MMIO write crosses into QEMU's device command callback. */
        writel(command, s->bar + KVM_COURSE_REG_COMMAND);
        /* A missing IRQ is a bounded failure, not an infinite polling loop. */
        if (!wait_for_completion_timeout(&s->done, 5 * HZ))
                return -ETIMEDOUT;
        /* DONE without ERROR is this device's successful terminal state. */
        if (readl(s->bar + KVM_COURSE_REG_STATUS) != KVM_COURSE_STATUS_DONE)
                return -EIO;
        /* Ensure subsequent CPU loads observe the completed DMA writes. */
        dma_rmb();
        return 0;
}

/* Finish device/handler access before managed teardown releases their memory. */
static void course_quiesce(struct pci_dev *pdev, struct course_guest *s)
{
        /* Stop delivery and clear the condition that could keep INTx asserted. */
        writel(0, s->bar + KVM_COURSE_REG_IRQ_MASK);
        writel(KVM_COURSE_IRQ_COMPLETION, s->bar + KVM_COURSE_REG_IRQ_STATUS);
        /* A read from the same device flushes the posted MMIO writes. */
        readl(s->bar + KVM_COURSE_REG_IRQ_STATUS);
        /* Wait for any handler already executing on another CPU. */
        synchronize_irq(pdev->irq);
        /* This synchronous device has no outstanding DMA worker to cancel. */
        pci_clear_master(pdev);
}

/* Probe runs when Linux matches vendor 1234 and device 11e9. */
static int course_probe(struct pci_dev *pdev, const struct pci_device_id *id)
{
        /* devm allocations are released on probe failure or driver removal. */
        struct course_guest *s;
        int ret, i;

        /* Allocate one zeroed software state object for this device. */
        s = devm_kzalloc(&pdev->dev, sizeof(*s), GFP_KERNEL);
        if (!s)
                return -ENOMEM;
        /* Enable the PCI function with managed disable on teardown. */
        ret = pcim_enable_device(pdev);
        if (ret)
                return ret;
        /* Claim and map BAR0; managed cleanup releases both resources. */
        ret = pcim_iomap_regions(pdev, BIT(0), "course_guest");
        if (ret)
                return ret;
        s->bar = pcim_iomap_table(pdev)[0];
        if (!s->bar)
                return -ENODEV;
        /* The device accepts a 64-bit DMA bus address. */
        ret = dma_set_mask_and_coherent(&pdev->dev, DMA_BIT_MASK(64));
        if (ret)
                return ret;
        /* Coherent memory already has a DMA mapping: do not map it again. */
        s->buffer = dmam_alloc_coherent(&pdev->dev, TEST_BYTES, &s->dma,
                                        GFP_KERNEL);
        if (!s->buffer)
                return -ENOMEM;
        /* Initialize the wait object before an interrupt can arrive. */
        init_completion(&s->done);
        /* Allocate the managed IRQ after DMA memory: teardown frees IRQ first. */
        ret = devm_request_irq(&pdev->dev, pdev->irq, course_irq, IRQF_SHARED,
                               "course_guest", s);
        if (ret)
                return ret;
        /* Save state for remove(), then permit this function to bus-master. */
        pci_set_drvdata(pdev, s);
        pci_set_master(pdev);
        /* Reject an ABI mismatch before running the test commands. */
        if (readl(s->bar + KVM_COURSE_REG_ID) != KVM_COURSE_ID ||
            readl(s->bar + KVM_COURSE_REG_VERSION) != KVM_COURSE_VERSION) {
                ret = -ENODEV;
                goto stop;
        }
        /* Check one simple CPU-to-device-to-CPU register round trip. */
        writel(0xfeed1234, s->bar + KVM_COURSE_REG_SCRATCH);
        if (readl(s->bar + KVM_COURSE_REG_SCRATCH) != 0xfeed1234) {
                ret = -EIO;
                goto stop;
        }
        /* Enable the completion source, then prove that real INTx delivery works. */
        writel(KVM_COURSE_IRQ_COMPLETION, s->bar + KVM_COURSE_REG_IRQ_MASK);
        ret = course_command(s, KVM_COURSE_CMD_PING);
        if (ret)
                goto stop;
        /* Program the DMA address, not the kernel buffer pointer. */
        writel(lower_32_bits(s->dma), s->bar + KVM_COURSE_REG_DMA_ADDR_LO);
        writel(upper_32_bits(s->dma), s->bar + KVM_COURSE_REG_DMA_ADDR_HI);
        /* Request 64 bytes filled with the little-endian byte pattern 78 56 34 12. */
        writel(TEST_BYTES, s->bar + KVM_COURSE_REG_DMA_LEN);
        writel(0x12345678, s->bar + KVM_COURSE_REG_DMA_PATTERN);
        ret = course_command(s, KVM_COURSE_CMD_DMA_FILL);
        if (ret)
                goto stop;
        /* Success must report the complete requested transfer. */
        if (readl(s->bar + KVM_COURSE_REG_DMA_RESULT) != TEST_BYTES) {
                ret = -EIO;
                goto stop;
        }
        /* Check every byte; a successful command alone does not prove DMA data. */
        for (i = 0; i < TEST_BYTES; i++) {
                if (s->buffer[i] != ((0x12345678U >> (8 * (i % 4))) & 0xff)) {
                        ret = -EIO;
                        goto stop;
                }
        }
        /* The host runner requires this message as well as successful insmod. */
        dev_info(&pdev->dev, "COURSE_DRIVER_PASS: MMIO INTx DMA64\n");
        return 0;
stop:
        /* The same ordering is required for probe failure and normal removal. */
        course_quiesce(pdev, s);
        dev_err(&pdev->dev, "COURSE_DRIVER_FAIL: %d\n", ret);
        return ret;
}

/* Quiesce the device before managed IRQ and DMA resources are released. */
static void course_remove(struct pci_dev *pdev)
{
        struct course_guest *s = pci_get_drvdata(pdev);

        /* Flush device writes and finish handlers before releasing resources. */
        course_quiesce(pdev, s);
}

/* This lab ID pair is only for the private educational device. */
static const struct pci_device_id course_ids[] = {
        { PCI_DEVICE(0x1234, 0x11e9) },
        { }
};
MODULE_DEVICE_TABLE(pci, course_ids);

/* The PCI core owns device matching and invokes probe/remove as needed. */
static struct pci_driver course_driver = {
        .name = "course_guest",
        .id_table = course_ids,
        .probe = course_probe,
        .remove = course_remove,
};
module_pci_driver(course_driver);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("MMIO, shared INTx and coherent DMA integration lab");

# Restore Linux-style leading tabs after copying the listing from Google Docs.
unexpand --first-only -t 8 "$KVM_DEV/driver/course_guest.c" \
  > "$KVM_DEV/driver/course_guest.tabs"
mv "$KVM_DEV/driver/course_guest.tabs" "$KVM_DEV/driver/course_guest.c"
# Return the build directory to the repaired kernel configuration and source.
cd "$KVM_DEV/linux-6.12"
git switch course
# Rebuild the repaired artifacts if a bisect candidate was built most recently.
bash "$KVM_DEV/build-kernel.sh" repaired
# Build against the exact kernel that will load the module, not host headers.
make LOCALVERSION= -C "$KVM_DEV/build" M="$KVM_DEV/driver" -j8 modules
# Check the example's style before putting it into the appliance.
scripts/checkpatch.pl --no-tree --strict --file "$KVM_DEV/driver/course_guest.c"
# Repack the initramfs; the assembly script now includes course_guest.ko.
bash "$KVM_DEV/make-initramfs.sh"
```

**Check:** the module builds, checkpatch is clean, and `driver/course_guest.ko` exists. The kernel build helper generated the symbol metadata by building `modules` as well as `bzImage`. A mismatched module/kernel release can produce `invalid module format`; rebuild both from the same configuration instead of forcing the module to load.

## 23.3 Lab D2 — prove MMIO, INTx, DMA, and removal

**Split-machine handoff:** the newly packed initramfs must contain the updated `course_guest.ko`. Copy this updated `course-initramfs.cpio.gz` and the matching `images/bzImage-repaired` to the runner before D2. An older archive does not test the module you just built.

On the physical runner, use the new initramfs and the matching repaired image:

```bash
# Add the course PCI function and enable the initramfs driver-check branch.
python3 "$KVM_DEV/run-kernel.py" repaired --driver
```

**Check:** the transcript contains `COURSE_DRIVER_PASS: MMIO INTx DMA64`, `COURSE_RESULT=driver:0`, `COURSE_RESULT=driver_remove:0`, and all the kernel checkpoints still pass. The driver verifies identity/version, a scratch-register round trip, interrupt-driven PING completion, a 64-byte DMA fill, every returned byte in little-endian order, and module removal. Loading an external module can print an out-of-tree taint notice; that is expected in this isolated exercise.

Notice the host harness's extra check for `COURSE_DRIVER_PASS`. Registering a PCI driver can succeed even if its device's probe fails. Therefore `insmod` returning 0 alone is insufficient evidence. Similarly, a `DONE` register alone does not prove that the buffer contains the right data; the driver checks the entire transfer.

**Explain it back:** identify the CPU pointer, DMA address, MMIO mapping, and PCI function in the code. Trace one command from Linux `writel` into the QEMU callback, through `pci_dma_write`, back through INTx and `complete`, and finally to the CPU's byte checks. Explain why the handler must be quiesced before its state and DMA buffer are freed.

**Worked answer:** `s->buffer` is the kernel CPU pointer; `s->dma` is the address given to the device; `s->bar` is an MMIO mapping and must use I/O accessors; `pdev` represents the enumerated PCI function. The command write reaches QEMU's MMIO callback, which fills guest memory through the PCI DMA address space and latches a completion. The enabled level triggers Linux's shared handler, which acknowledges the condition and wakes the waiter. The waiter checks both completion status and actual bytes. A handler retaining a freed `s`, BAR mapping, or buffer would be a lifetime bug even if the command itself was correct.
