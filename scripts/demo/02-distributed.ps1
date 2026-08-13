# Distributed real-KVM demo — WINDOWS side (run from the repo root in a
# NON-admin PowerShell; embedded Postgres refuses to run as root/admin).
#
# Topology: the control-plane + PostgreSQL run HERE on Windows (native, no
# hypervisor). The libvirt agent runs in the nested-KVM VM and dials this
# control-plane over an SSH reverse tunnel. This is the control-plane/agent
# split working across two hosts — and it keeps the DB/control-plane workload
# off the fragile nested-virt VM (see docs/reviews/real-substrate-findings.md,
# "End-to-end capstone").
#
#   pwsh -File scripts/demo/02-distributed.ps1
#
# Prereqs: Go on PATH; the Vagrant substrate VM up (deploy/vagrant, ssh on
# 127.0.0.1:2222); the repo already copied into the VM at ~/vm-control-plane.
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
Set-Location $repo
$key = "$repo\deploy\vagrant\.vagrant\machines\default\virtualbox\private_key"
$tunnelPort = 18080

Write-Host "== starting control-plane + Postgres on Windows (no agent) =="
$cpLog = New-TemporaryFile
$cp = Start-Process -FilePath "go" -ArgumentList @("run", "./scripts/dev", "--no-agent") `
    -RedirectStandardOutput $cpLog -RedirectStandardError "$cpLog.err" -NoNewWindow -PassThru

try {
    $port = $null
    foreach ($i in 1..30) {
        Start-Sleep -Seconds 2
        $m = Select-String -Path $cpLog -Pattern 'VMCTL_SERVER=127\.0\.0\.1:(\d+)' -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($m) { $port = $m.Matches[0].Groups[1].Value; break }
    }
    if (-not $port) { throw "control-plane did not come up; see $cpLog" }
    Write-Host "control-plane listening on 127.0.0.1:$port"

    Write-Host "== running the VM-side demo over an SSH reverse tunnel ($tunnelPort -> $port) =="
    $sshArgs = @(
        "-R", "${tunnelPort}:127.0.0.1:$port",
        "-i", $key,
        "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=NUL",
        "-o", "ServerAliveInterval=20", "-o", "ServerAliveCountMax=15",
        "-o", "ExitOnForwardFailure=yes",
        "-p", "2222", "vagrant@127.0.0.1",
        "bash ~/vm-control-plane/scripts/demo/02-distributed-vm.sh $tunnelPort"
    )
    & ssh @sshArgs
    Write-Host "== done (exit $LASTEXITCODE) =="
}
finally {
    Write-Host "== stopping control-plane + Postgres =="
    if ($cp -and -not $cp.HasExited) { Stop-Process -Id $cp.Id -Force -ErrorAction SilentlyContinue }
    Get-Process -Name control-plane,postgres -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
}
