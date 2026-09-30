# Security boundaries and recovery access

**Goal.** Make the management, console and guest boundaries visible. Retain guest access when its network is down, without publishing a raw VNC or SPICE endpoint.

**Read first.**  [libvirt QEMU security architecture](https://libvirt.org/drvqemu.html),  [client access control](https://libvirt.org/acl.html) and  [QEMU VNC security](https://www.qemu.org/docs/master/system/vnc-security.html).

## 6.1 Map authority

1. Diagram the management connection, libvirt socket, QEMU process, guest NIC, seed image, serial console and any graphics endpoint for linux-01. Mark who can read each file and control each process. Compare qemu:///system with qemu:///session on a **different disposable guest**; do not mistake a per-user connection for automatic security of system-managed VMs.

**Check:** The diagram names who can control each socket, process, disk, seed and console.

## 6.2 Test serial recovery

1. While SSH works in a disposable clone, run sudo adduser kvmrecovery, choose a strong password interactively outside Git, then run sudo usermod -aG sudo kvmrecovery. Keep SSH password authentication disabled.

2. Confirm the clone XML has a serial device. For the chapter-1 ttyS0 console, enable sudo systemctl enable --now serial-getty@ttyS0.service inside the guest.

3. From the host, open sudo virsh -c qemu:///system console CLONE, log in as kvmrecovery, run id and sudo -v, then detach with Ctrl-\]. Repair this path before any NIC drill if login fails.

4. Disconnect only the clone NIC, recover through the tested console, restore the NIC, and prove SSH returns. Repeat the console test on linux-01 before its later network drills.

5. Keep the lab-only account through incident exercises. Remove it with sudo deluser --remove-home kvmrecovery only after another recovery route is documented and tested.

**Check:** Console login and sudo work before the NIC drill; recovery still works without guest networking, and SSH returns after NIC restoration.

## 6.3 Diagnose denial

1. Inspect libvirt and QEMU file ownership, Unix-socket permissions, AppArmor/SELinux confinement and logs. Test one deliberate image-path denial on the clone, identify whether Unix permissions or confinement caused it, then fix the narrow cause. Never globally turn off confinement to pass the lab.

**Check:** The image-path denial is attributed to permissions or confinement and fixed narrowly.

## 6.4 Test access control

1. Give a second lab user **only** the intended management access. Test allowed and denied operations against two disposable domains. Authentication to the system socket alone generally grants broad read/write or read-only access; it is **not per-VM authorization**.

2. Configure and test a real ACL driver or enforcing management layer if the pass condition is per-VM isolation. Keep direct administrative socket access restricted.

**Check:** Allowed and denied management operations prove the intended authorization boundary.

## 6.5 Audit secret paths

1. Examine how secrets, cloud-init seed, SSH keys and QEMU guest-agent commands cross boundaries. Guest-agent execution is privileged remote execution. Keep graphical endpoints bound privately and access them through a documented authenticated, encrypted path; verify no lab console port is routable from an unintended network.

**Check:** Secrets and guest-agent authority are documented; no console port is unintentionally routable.

**Pass and cleanup.** Demonstrate console recovery without guest networking and denial of another user’s VM control; name the authority for each API/console path. Remove temporary grants and restore the clone NIC. Browser console brokerage belongs to chapter 18.
