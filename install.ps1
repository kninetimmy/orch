<#
.SYNOPSIS
Download the released orch binary for Windows, verify its SHA-256
against the release's published SHA256SUMS, and install it. Fails
closed: nothing is installed unless the checksum matches.

.PARAMETER Version
Release tag to install (default: latest), e.g. v0.1.0.

.PARAMETER InstallDir
Install directory (default: %LOCALAPPDATA%\Programs\orch).

.PARAMETER NoPathUpdate
By default the install directory is appended to the user PATH (never
the machine PATH) when missing. This switch prints the command to do it
yourself instead.

.NOTES
Exit codes mirror install.sh: 0 ok, 2 unsupported platform or bad
-Version, 3 download failure, 4 checksum failure, 5 install failure.
#>
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '', Justification = 'user-facing installer output')]
[CmdletBinding()]
param(
    [string]$Version = 'latest',
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\orch",
    [switch]$NoPathUpdate
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repo = 'kninetimmy/orch'
$exitClass = 2
$tmp = $null

try {
    try {
        # Validate the version before it reaches a URL; fail closed on
        # anything outside the tag charset.
        if ($Version -ne 'latest' -and $Version -notmatch '^v[0-9][0-9A-Za-z.+-]*$') {
            throw "Version must be a release tag like v0.1.0 (got: $Version)"
        }

        switch ($env:PROCESSOR_ARCHITECTURE) {
            'AMD64' { $arch = 'amd64' }
            'ARM64' { $arch = 'arm64' }
            default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE (releases cover amd64 and arm64)" }
        }
        $asset = "orch_windows_$arch.exe"

        if ($Version -eq 'latest') {
            $base = "https://github.com/$repo/releases/latest/download"
        }
        else {
            $base = "https://github.com/$repo/releases/download/$Version"
        }

        $exitClass = 3
        # TLS 1.2 for Windows PowerShell 5.1 (harmless on PowerShell 7+).
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

        $tmp = Join-Path $env:TEMP ("orch-install-" + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $tmp | Out-Null
        $binaryPath = Join-Path $tmp $asset
        $sumsPath = Join-Path $tmp 'SHA256SUMS'
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $binaryPath
        Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sumsPath

        $exitClass = 4
        # Verify before anything touches the install dir: exact-filename
        # match against the checksum list; a missing line is a failure,
        # never a skip.
        $expected = $null
        foreach ($line in Get-Content $sumsPath) {
            if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+)$' -and $Matches[2] -eq $asset) {
                $expected = $Matches[1]
                break
            }
        }
        if (-not $expected) {
            throw "SHA256SUMS has no entry for $asset"
        }
        $actual = (Get-FileHash -Path $binaryPath -Algorithm SHA256).Hash
        if ($actual -ne $expected) {
            throw "checksum mismatch for ${asset}: expected $expected, got $actual - refusing to install"
        }

        $exitClass = 5
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
        $dest = Join-Path $InstallDir 'orch.exe'
        Copy-Item -Path $binaryPath -Destination $dest -Force

        Write-Host "installed: $dest"
        # Best-effort version echo; status prints it on its first stdout
        # line. Stderr is dropped (a non-orch cwd is normal here), and
        # under Windows PowerShell 5.1 that redirect needs a non-Stop
        # preference or the stderr write itself becomes terminating.
        $statusLine = $null
        $prevPreference = $ErrorActionPreference
        $ErrorActionPreference = 'SilentlyContinue'
        try {
            $statusLine = & $dest status 2>$null | Select-Object -First 1
        }
        finally {
            $ErrorActionPreference = $prevPreference
        }
        if ($statusLine) { Write-Host $statusLine }

        # User PATH only, never machine PATH, never silent.
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($null -eq $userPath) { $userPath = '' }
        $onPath = $false
        foreach ($segment in ($userPath -split ';')) {
            if ($segment -and $segment.TrimEnd('\') -ieq $InstallDir.TrimEnd('\')) {
                $onPath = $true
                break
            }
        }
        if (-not $onPath) {
            if ($NoPathUpdate) {
                Write-Host "$InstallDir is not on your user PATH; add it with:"
                Write-Host "  [Environment]::SetEnvironmentVariable('Path', '$InstallDir;' + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')"
            }
            else {
                $newPath = ($userPath.TrimEnd(';') + ';' + $InstallDir).TrimStart(';')
                [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
                Write-Host "added $InstallDir to your user PATH - restart your terminal to pick it up"
            }
        }

        Write-Host "next: run 'orch doctor'"
        $exitClass = 0
    }
    finally {
        if ($tmp -and (Test-Path $tmp)) {
            Remove-Item -Recurse -Force -Path $tmp
        }
    }
}
catch {
    [Console]::Error.WriteLine("install.ps1: $($_.Exception.Message)")
    exit $exitClass
}
exit 0
