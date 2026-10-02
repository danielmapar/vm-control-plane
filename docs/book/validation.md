## Validation status and course maintenance

The reference machine runs Fedora 44 Workstation on x86-64 with an Intel CPU, kernel 7.2.7-200.fc44, QEMU 10.2.2, libvirt 12.0.0 and virt-install 5.1.0. Guests use the Fedora Cloud Base Generic 44-1.7 image: Btrfs, cloud-init and the user `fedora`. No AMD host was available, so nothing was validated on AMD. Record your own versions. A later package update does not prove that an older procedure still works.

**Passed** means three things about a chapter:
- Every printed command block ran unchanged, in one validation run, after the current text of the earlier sections it builds on (usually the chapter 1.7 helper scripts) and before the Appendix B.2 removal. The console keystrokes of chapter 6.2 were typed by a test program that sends exactly the keys shown, in a run of their own.
- The checks and the deliberate failures behaved as the text says.
- Removal returned the workstation to its recorded starting state.

It does not mean every conditional branch was tested. Anything a chapter labels **not validated here** was not run, for the reason given there.

Every unit has three independent reviewers, who check correctness, simplicity and self-study use. A unit is final only when all three score its current text 10/10 for both correctness and simplicity.

Status on **2 October 2026**:

| Part | Status and main limits |
|---|---|
| Image provenance | The signed CHECKSUM file and the image digest were verified. Fedora 44 key: `36F612DCF27F7D1A48A835E4DBFCF71C6D9F90A6`. |
| Part I, chapters 0–5 | Passed. Not validated here: the first install of the virtualization packages on a fresh host, autostart after a host reboot, and connecting guests to a physical network card. |
| Part II, chapters 6–9 | Passed. Migration uses nested hosts on one workstation. Recovery after losing a physical host is not validated here. |
| Part III, chapter 10 | Passed. |
| Part IV, chapters 11–13 | Passed. Intel VT-x was tested; AMD was not. Device assignment (VFIO) is explained with read-only checks only, because the course never takes a device from the workstation it runs on. |
| Part V, chapter 14 | Passed. Assessment by another person is not validated here. |
| Appendices A–B | Passed. Appendix B ran after every chapter's validation run. |

Nested hosts share the workstation's CPU, power and storage. Their exercises (chapter 6.4's access control, chapter 8's migration and disk locking) test mechanics; they do not prove recovery after losing a physical host. Chapter 7 restores guests on the workstation and keeps its backups on the same disk, so a copy on other media is not validated. Restore and migration times are observations, not guarantees.

**Known deviations.** The desktop's software updater sometimes starts the Passim caching service by itself; the before-and-after comparisons tolerate that service. They also ignore the workstation's own Wi-Fi and docking-station state (the dock's network card and display driver), which changed during validation and which no lab touches.

**Keeping the course correct.**
- For each chapter, keep four facts: the last execution date, the software and image versions, the observed result, and any deviation with its resolution.
- After changing the kernel, QEMU, libvirt, the firmware or the image, repeat the affected chapters.
- Run Appendix A before diagnosing a later lab.
- Have a second reader repeat the early chapters without help, and fix every step that needed help.
