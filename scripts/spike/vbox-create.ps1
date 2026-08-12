# Provision the Tier-1 substrate VM under Oracle VirtualBox with nested
# VT-x — run from Windows PowerShell (NOT admin required for VBoxManage).
#
#   .\scripts\spike\vbox-create.ps1 -IsoPath C:\isos\ubuntu-24.04-live-server-amd64.iso
#
# Preconditions this script checks (both verified on the dev machine):
#   - VirtualBox installed;
#   - Hyper-V NOT running (HypervisorPresent=False) — VirtualBox needs raw
#     VT-x to offer nested virtualization to the guest.
#
# After the Ubuntu install completes inside the VM:
#   ssh -p 2222 <user>@127.0.0.1
#   git clone <repo> && cd vm-control-plane
#   ./scripts/spike/host-setup.sh     # twice: re-login applies groups
#   ./scripts/spike/spike.sh          # the go/no-go gate for real-driver PRs
param(
    [Parameter(Mandatory = $true)] [string]$IsoPath,
    [string]$Name = "vmc-substrate",
    [int]$Cpus = 4,
    [int]$MemoryMB = 8192,
    [int]$DiskGB = 40,
    [int]$SshPort = 2222
)

$ErrorActionPreference = "Stop"
$vbm = "C:\Program Files\Oracle\VirtualBox\VBoxManage.exe"
if (-not (Test-Path $vbm)) { throw "VBoxManage not found at $vbm" }
if (-not (Test-Path $IsoPath)) { throw "Ubuntu ISO not found: $IsoPath" }

$hyperv = (Get-CimInstance Win32_ComputerSystem).HypervisorPresent
if ($hyperv) {
    throw "Hyper-V is active: VirtualBox cannot pass nested VT-x through. Disable Hyper-V/VBS and retry."
}

& $vbm createvm --name $Name --ostype Ubuntu_64 --register

# --nested-hw-virt on is THE load-bearing flag: without it /dev/kvm never
# appears in the guest and the spike is an immediate NO-GO.
& $vbm modifyvm $Name `
    --cpus $Cpus --memory $MemoryMB `
    --nested-hw-virt on `
    --ioapic on --pae on `
    --graphicscontroller vmsvga --vram 16 `
    --nic1 nat --natpf1 "ssh,tcp,127.0.0.1,$SshPort,,22"

$diskDir = Join-Path $env:LOCALAPPDATA "vmc-substrate"
New-Item -ItemType Directory -Force $diskDir | Out-Null
$vdi = Join-Path $diskDir "$Name.vdi"
& $vbm createmedium disk --filename $vdi --size ($DiskGB * 1024)

& $vbm storagectl $Name --name SATA --add sata --controller IntelAhci --portcount 2
& $vbm storageattach $Name --storagectl SATA --port 0 --device 0 --type hdd --medium $vdi
& $vbm storageattach $Name --storagectl SATA --port 1 --device 0 --type dvddrive --medium $IsoPath

& $vbm startvm $Name

Write-Host ""
Write-Host "VM '$Name' started with nested VT-x. Install Ubuntu Server (enable OpenSSH),"
Write-Host "then: ssh -p $SshPort <user>@127.0.0.1 and run scripts/spike/host-setup.sh."
