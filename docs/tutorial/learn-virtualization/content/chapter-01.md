# The mental model and the first Linux VM

**Goal.** Boot linux-01 and identify three layers: KVM runs guest CPU work, QEMU supplies the VM process and devices, and libvirt defines and manages that process. Check process launch, guest boot, cloud-init and SSH separately; success at one layer does not prove the next.

**Read first.**  [KVM userspace](https://www.redhat.com/en/blog/all-you-need-know-about-kvm-userspace),  [Ubuntu's libvirt cloud-image example](https://ubuntu.com/docs/public-images/public-images-how-to/launch-with-libvirt/),  [Ubuntu image verification](https://ubuntu.com/docs/public-images/public-images-how-to/verify-image-checksum/) and  [local cloud-init seed](https://ubuntu.com/docs/public-images/public-images-how-to/use-local-cloud-init-ds/).

## 1.1 Verify the image

1. Put the code blocks in steps 1–3 in one fail-fast Bash script and run it from a fresh state. It refuses an existing linux-01 domain or lab artifact; inspect and resolve a partial run before retrying.


2. Download the image and SHA256SUMS/signature together from the  [Ubuntu Noble release directory](https://cloud-images.ubuntu.com/releases/noble/release/), verify the Ubuntu signing key and image checksum, and record the URL, digest and key evidence. Expect the image checksum to say OK. If the moving release directory changes mid-download, choose one dated build; never accept a mismatch. Start with:


```bash
# Stop on unhandled command errors, unset variables and pipeline failures.
set -euo pipefail
# Name the libvirt-accessible storage directory.
VM_DIR=/var/lib/libvirt/images/kvm-course
# Confirm system libvirt is reachable before creating files.
sudo virsh -c qemu:///system list --all >/dev/null
# Refuse to replace a domain that already exists.
if sudo virsh -c qemu:///system dominfo linux-01 >/dev/null 2>&1; then
  # Explain the guard failure.
  echo "linux-01 already exists; inspect it instead of rerunning this recipe" >&2
  # Stop before any download or disk operation.
  exit 1
fi
# Refuse existing destination disks or seed media.
for path in "$VM_DIR/noble-base.qcow2" "$VM_DIR/linux-01.qcow2" "$VM_DIR/seed-linux-01.iso"; do
  # Abort if this destination already exists.
  if sudo test -e "$path"; then echo "Existing lab artifact: $path" >&2; exit 1; fi
done
# Require Ubuntu's cloud-image signing keyring.
test -f /usr/share/keyrings/ubuntu-cloudimage-keyring.gpg
# Keep downloads and seed files outside Git.
mkdir -p "$HOME/kvm-course-downloads"
# Run subsequent relative paths from that directory.
cd "$HOME/kvm-course-downloads"
# Refuse stale downloads or cloud-init files on a retry.
for path in ubuntu-24.04-server-cloudimg-amd64.img SHA256SUMS SHA256SUMS.gpg user-data meta-data seed-linux-01.iso; do
  # Abort rather than silently reusing a partial artifact.
  if test -e "$path"; then echo "Existing preparation artifact: $path" >&2; exit 1; fi
done
# Download the cloud image.
curl -fLO https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img
# Download the checksum manifest.
curl -fLO https://cloud-images.ubuntu.com/releases/noble/release/SHA256SUMS
# Download the manifest signature.
curl -fLO https://cloud-images.ubuntu.com/releases/noble/release/SHA256SUMS.gpg
# Authenticate the checksum manifest with Ubuntu's packaged keyring.
gpg --no-default-keyring --keyring /usr/share/keyrings/ubuntu-cloudimage-keyring.gpg --verify SHA256SUMS.gpg SHA256SUMS
# Verify the downloaded image against that manifest.
sha256sum -c --ignore-missing SHA256SUMS
```

**Check:** Signature and checksum verification pass for the recorded image build; no existing artifact is overwritten.

## 1.2 Create the seed

1. Ensure you have an SSH public key, for example $HOME/.ssh/id\_ed25519.pub; create a new key with ssh-keygen only if needed and keep the private key outside the lab repository. Make a user-data file with #cloud-config, ssh\_pwauth: false, and ssh\_authorized\_keys containing that public key. Make meta-data with instance-id: linux-01-001 and local-hostname: linux-01.


2. Run cloud-localds seed-linux-01.iso user-data meta-data. NoCloud requires a distinct instance ID for each new guest. This seed carries no passwords.


```bash
# Select the public key that the new guest will trust.
PUBKEY_PATH="$HOME/.ssh/id_ed25519.pub"
# Stop if the key file is absent or empty.
test -s "$PUBKEY_PATH" || { echo "Set PUBKEY_PATH to an existing public key"; exit 1; }
# Write NoCloud user data; the heredoc expands the selected public key.
cat > user-data <<EOF
#cloud-config
# Keep SSH password authentication disabled.
ssh_pwauth: false
# Install exactly the selected public key for SSH login.
ssh_authorized_keys:
  - $(cat "$PUBKEY_PATH")
EOF
# Write the NoCloud instance identity.
cat > meta-data <<EOF
# Give this new instance a unique cloud-init ID.
instance-id: linux-01-001
# Set its guest hostname.
local-hostname: linux-01
EOF
# Package both files into the seed ISO attached at boot.
cloud-localds seed-linux-01.iso user-data meta-data
```

**Check:** The seed contains the selected public key, a unique instance ID and linux-01 hostname.

## 1.3 Create the disk

1. Copy the verified pristine image to /var/lib/libvirt/images/kvm-course/noble-base.qcow2, preserving its qcow2 format. Keep it unchanged. Create /var/lib/libvirt/images/kvm-course/linux-01.qcow2 as an overlay with an explicit qcow2 backing format. While inactive, expand **only the overlay’s virtual size** to 20 GiB; cloud-init’s guest growpart/filesystem behavior must be checked after boot.


2. Install the seed in the same libvirt-accessible directory. Before launch, use qemu-img info on these **inactive** files and check that the overlay records its trusted qcow2 backing format; resolve file-permission or AppArmor errors through the distribution policy, not by disabling confinement.


```bash
# Use the same managed storage directory as step 1.1.
VM_DIR=/var/lib/libvirt/images/kvm-course
# Create that directory for the course images.
sudo install -d -m 0755 "$VM_DIR"
# Install the verified image as the immutable base.
sudo install -m 0644 ubuntu-24.04-server-cloudimg-amd64.img "$VM_DIR/noble-base.qcow2"
# Install the cloud-init seed where libvirt can read it.
sudo install -m 0644 seed-linux-01.iso "$VM_DIR/seed-linux-01.iso"
# Inspect the inactive base and confirm its image format.
sudo qemu-img info "$VM_DIR/noble-base.qcow2"
# Create a writable qcow2 overlay that names the qcow2 base explicitly.
sudo qemu-img create -f qcow2 -F qcow2 -b "$VM_DIR/noble-base.qcow2" "$VM_DIR/linux-01.qcow2"
# Enlarge only the overlay's virtual capacity while it is inactive.
sudo qemu-img resize "$VM_DIR/linux-01.qcow2" 20G
# Check the overlay and its complete backing chain before boot.
sudo qemu-img info --backing-chain "$VM_DIR/linux-01.qcow2"
```

**Check:** qemu-img shows an inactive qcow2 overlay backed by the verified base, with 20 GiB virtual capacity.

## 1.4 Review and launch

1. With the default NAT network active, launch through virt-install. **Recheck that linux-01 is still undefined and that its overlay and seed point to the paths just created.** Use --print-xml first; inspect the generated disk and NIC paths, then run the command without --print-xml only after that review.


2. Do not point the writable VM disk at the pristine base. Run this preview as a separate fail-fast Bash command; its XML-preview flag is the only argument to remove for creation:


```bash
# Stop on unhandled command errors, unset variables and pipeline failures.
set -euo pipefail
# Verify the system connection before trying to launch.
sudo virsh -c qemu:///system list --all >/dev/null
# Refuse to replace an existing linux-01 domain.
if sudo virsh -c qemu:///system dominfo linux-01 >/dev/null 2>&1; then
  # Explain why the preview cannot continue.
  echo "linux-01 already exists; do not create it again" >&2
  # Stop before invoking virt-install.
  exit 1
fi
# Capture public SSH host-key fingerprints from the trusted host console.
SERIAL_LOG=/var/log/libvirt/qemu/linux-01-console.log
# Refuse an old log so stale fingerprints cannot be mistaken for this boot.
sudo test ! -e "$SERIAL_LOG" || { echo "Inspect existing serial log first" >&2; exit 1; }
# Keep each argument beside its purpose; Bash preserves each array item.
args=(
  --connect qemu:///system       # Manage the host’s system libvirt instance.
  --virt-type kvm                # Require KVM acceleration.
  --name linux-01                # Use the guarded, unique domain name.
  --memory 2048                 # Allocate 2 GiB of guest RAM.
  --vcpus 2                     # Start with two virtual CPUs.
  --import                      # Boot the prepared disk, not an installer.
  --osinfo generic              # Avoid depending on a newer OS database entry.
  # Write only to the overlay, with an explicit disk format and bus.
  --disk path=/var/lib/libvirt/images/kvm-course/linux-01.qcow2,bus=virtio,format=qcow2
  # Attach the instance’s NoCloud seed as a CD-ROM.
  --disk path=/var/lib/libvirt/images/kvm-course/seed-linux-01.iso,device=cdrom
  --network network=default,model=virtio  # Attach the known NAT network.
  --graphics none               # The first guest uses a serial console.
  # Log serial output without removing interactive console access.
  --serial "pty,log.file=$SERIAL_LOG,log.append=on"
  --console pty,target_type=serial  # Expose the matching serial console.
  --noautoconsole                # Return to the shell after starting the VM.
  --print-xml                    # Preview only; remove this item to launch.
)
# Review paths, serial log, disk, seed and NIC before removing --print-xml.
sudo virt-install "${args[@]}"
```

**Check:** Reviewed XML points to the overlay, seed, default NAT network and serial console before creation.

## 1.5 Prove the boot

1. Inspect the running VM with `sudo virsh -c qemu:///system domstate linux-01`, then `dominfo`, `dumpxml`, `domblklist` and `domiflist`. The XML should say `type="kvm"`. Obtain its IPv4 address with `domifaddr linux-01 --source lease` or `net-dhcp-leases default`.

2. **Verify the SSH server before the first login.** The host-side serial log records cloud-init’s public SSH host-key fingerprints. Wait for the complete `BEGIN SSH HOST KEY FINGERPRINTS` / `END SSH HOST KEY FINGERPRINTS` block. This trusted console path avoids assuming that an IP address proves identity. See  [libvirt character-device logging](https://libvirt.org/formatdomain.html#consoles-serial-parallel-channel-devices) and  [cloud-init keys to console](https://docs.cloud-init.io/en/latest/reference/modules.html#keys-to-console).

```bash
# Read the IPv4 address you just observed; do not copy an address from an example.
read -r -p "linux-01 IPv4 address: " GUEST_IP
# Fetch the offered public key. This scan does NOT authenticate the server.
ssh-keyscan -T 5 -t ed25519 "$GUEST_IP" > linux-01-hostkey.scan
# Print the candidate key’s SHA256 fingerprint.
ssh-keygen -lf linux-01-hostkey.scan -E sha256
# Read the independent, host-side boot record for this newly created VM.
sudo sed -n '/BEGIN SSH HOST KEY FINGERPRINTS/,/END SSH HOST KEY FINGERPRINTS/p' \
  /var/log/libvirt/qemu/linux-01-console.log
```

**Check before continuing:** the ED25519 SHA256 fingerprint must match exactly. A scan alone is not verification. If the log is incomplete, wait for cloud-init to finish and inspect it again. If a fingerprint is absent or different, stop and diagnose the seed, log and guest identity; do not disable SSH checking. After a match, run the next block in the same shell:

```bash
# Create the SSH configuration directory if needed, with private permissions.
mkdir -p "$HOME/.ssh"
# Keep other local users from reading or changing this directory.
chmod 700 "$HOME/.ssh"
# Trust only the public key whose fingerprint you just compared.
cat linux-01-hostkey.scan >> "$HOME/.ssh/known_hosts"
# Keep the trust file private to this user.
chmod 600 "$HOME/.ssh/known_hosts"
# Require a known key, then wait for cloud-init and check the guest identity.
ssh -o StrictHostKeyChecking=yes "ubuntu@$GUEST_IP" \
  'cloud-init status --wait && hostname && ip -brief address'
```

If this private IP was reused, SSH may report an older key already in known\_hosts. Confirm the lease and fingerprint first, then remove only that obsolete address entry with `ssh-keygen -R "$GUEST_IP"` and add the verified scan again. Preserve the serial log as lab evidence; it can contain operational output, so do not publish it blindly.

3. The key-only guest has **no tested password login on its serial console** yet. If SSH fails, inspect serial boot output and the host-side XML, seed, DHCP lease and permissions. Chapter 6 creates and tests a temporary console recovery account before disconnecting a NIC.

4. If the seed/key is wrong, shut down the disposable guest, save evidence, deliberately remove its named domain/overlay/seed/log, and rebuild with a new instance ID. Do not assume a passwordless image offers console login.

**Check:** Appendix B’s C1 check passes: KVM enabled, matching lease, trusted key-based SSH, cloud-init success and expected hostname.

**Observed in the nested reference run:** `query-kvm` returned `{"enabled":true,"present":true}`, the guest received a 192.168.122.0/24 lease, cloud-init reported `done`, and `/dev/vda1` was an ext4 root of about 19 GiB. Your address and exact sizes may differ; compare the properties, not those incidental values.

## 1.6 Break one login

1. Reproduce the “wrong SSH key” drill with a disposable client key only. Show that the VM and cloud-init remain healthy while authentication fails. Later, after the managed VM is stable, launch a **separate throwaway image** using a minimal raw-QEMU command from Ubuntu’s QEMU guide.

2. Do not run raw QEMU against linux-01’s active disk.

**Check:** The wrong client key fails while the VM remains healthy; the raw-QEMU experiment uses a separate disk.

**Pass and cleanup.** Trace virsh/virt-install → libvirt → QEMU → /dev/kvm → guest kernel, and prove KVM is active. Save sanitized XML, image digest, seed template and results; remove only the throwaway raw-QEMU disk. Keep linux-01 and its base/overlay. If image login or serial behavior differs, use its documented behavior and record the change.

**Checkpoint C1:** verified base image and digest, saved domain XML, linux-01 running with KVM acceleration, a matching DHCP lease, working key-based SSH and successful cloud-init. Run Appendix B’s post-VM check and save its output in chapter-01/evidence/. Repeat it before networking/storage changes in chapters 3–8. After moving the VM in chapter 9, use the recorded current owner and network; do not apply the original host-a/default-NAT assumptions blindly.
