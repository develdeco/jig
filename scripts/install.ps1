<#
.SYNOPSIS
    Installs the latest (or a pinned) jig release for Windows.

.DESCRIPTION
    Downloads the jig archive matching this machine's architecture, verifies
    it against checksums.txt, and installs jig.exe to a user directory
    (no admin rights required).

.ENVIRONMENT VARIABLES
    JIG_VERSION       A release tag (e.g. v0.1.1) to install instead of the
                       latest release.
    JIG_RELEASE_URL    Overrides the base URL the archive and checksums.txt
                       are fetched from (mirrors, or a local snapshot such as
                       file:///C:/path/to/dist for testing).
    JIG_INSTALL_DIR    Overrides the install directory (default:
                       %LOCALAPPDATA%\Programs\jig\bin).
#>

function Install-Jig {
    # Scoped to this function, so running the script body via `iex` (as the
    # documented one-liner does) never changes these preferences in the
    # caller's session.
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $RepoOwner = 'develdeco'
    $RepoName = 'jig'

    function Write-Info([string]$Message) {
        Write-Host $Message
    }

    $archRaw = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) {
        # A 32-bit process (e.g. 32-bit PowerShell) on a 64-bit OS reports the
        # emulated architecture in PROCESSOR_ARCHITECTURE; the real one lives here.
        $archRaw = $env:PROCESSOR_ARCHITEW6432
    }
    switch ($archRaw) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "unsupported architecture: $archRaw" }
    }

    if ($env:JIG_VERSION) {
        $defaultBaseUrl = "https://github.com/$RepoOwner/$RepoName/releases/download/$($env:JIG_VERSION)"
    } else {
        $defaultBaseUrl = "https://github.com/$RepoOwner/$RepoName/releases/latest/download"
    }
    $baseUrl = if ($env:JIG_RELEASE_URL) { $env:JIG_RELEASE_URL } else { $defaultBaseUrl }

    $archive = "jig_windows_$arch.zip"

    $tmpDir = Join-Path $env:TEMP ("jig-install-" + [System.Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    try {
        $archivePath = Join-Path $tmpDir $archive
        $checksumsPath = Join-Path $tmpDir 'checksums.txt'

        Write-Info "downloading $archive from $baseUrl..."
        try {
            Invoke-WebRequest -Uri "$baseUrl/$archive" -OutFile $archivePath -UseBasicParsing
            Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile $checksumsPath -UseBasicParsing
        } catch {
            throw "failed to download $archive`: $($_.Exception.Message)"
        }

        Write-Info 'verifying checksum...'
        $checksumLine = Select-String -Path $checksumsPath -SimpleMatch -Pattern $archive | Select-Object -First 1
        if (-not $checksumLine) {
            throw "no checksum entry for $archive in checksums.txt"
        }
        $expectedHash = (($checksumLine.Line -split '\s+')[0]).ToUpperInvariant()
        $actualHash = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToUpperInvariant()
        if ($actualHash -ne $expectedHash) {
            throw "checksum mismatch for $archive (expected $expectedHash, got $actualHash)"
        }

        Write-Info 'extracting...'
        Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force

        $installDir = if ($env:JIG_INSTALL_DIR) { $env:JIG_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\jig\bin' }
        New-Item -ItemType Directory -Path $installDir -Force | Out-Null

        $installPath = Join-Path $installDir 'jig.exe'
        Copy-Item -Path (Join-Path $tmpDir 'jig.exe') -Destination $installPath -Force

        Write-Info "installed jig to $installPath"

        $pathEntries = $env:Path -split ';' | Where-Object { $_ }
        if ($pathEntries -notcontains $installDir) {
            $escapedInstallDir = $installDir -replace "'", "''"
            Write-Info ''
            Write-Info "$installDir is not on your PATH. Add it with:"
            Write-Info "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$escapedInstallDir', 'User')"
            Write-Info "  (and, for this session only: `$env:Path += ';$escapedInstallDir')"
        }

        Write-Info ''
        & $installPath version

        Write-Info ''
        Write-Info 'next: run "jig skills install" to install the session skills'
    } finally {
        Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

try {
    Install-Jig
} catch {
    Write-Host "install.ps1: error: $($_.Exception.Message)" -ForegroundColor Red
    if ($PSCommandPath) {
        # Run as a file (`-File script.ps1`, or `& .\install.ps1`): a real
        # process/script exit code is expected, so exit here is safe.
        exit 1
    }
    # Run via `iex` from a piped-in string: there is no script process of our
    # own to exit, only the caller's session, so surface the failure instead
    # of tearing that session down.
    throw
}
