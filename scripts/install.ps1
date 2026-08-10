#Requires -Version 5.1
<#
.SYNOPSIS
  FlowBaton Windows installer. Downloads a release archive, verifies its GitHub
  build attestation is bound to this repository, release workflow, and tag,
  then checks the published SHA-256 before installing flowbaton.exe.

  irm https://github.com/larchwave/flowbaton/releases/latest/download/install.ps1 | iex

.DESCRIPTION
  Environment overrides:
    FLOWBATON_VERSION      release version without the leading "v" (default: latest)
    FLOWBATON_INSTALL_DIR  install directory (default: %LOCALAPPDATA%\FlowBaton\bin)
    FLOWBATON_BASE_URL     release download base (default: GitHub releases)
#>
# Fail fast: every I/O cmdlet below passes -ErrorAction Stop so any failure
# terminates instead of continuing past a broken download or extraction.
Set-StrictMode -Version Latest

$repo = 'larchwave/flowbaton'
$arch = if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else { throw 'install: unsupported architecture (amd64 only)' }
$installDir = if ($env:FLOWBATON_INSTALL_DIR) { $env:FLOWBATON_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'FlowBaton\bin' }

$version = $env:FLOWBATON_VERSION
if (-not $version) {
	$latest = Invoke-RestMethod -UseBasicParsing -ErrorAction Stop "https://api.github.com/repos/$repo/releases/latest"
	$version = $latest.tag_name -replace '^v', ''
	if (-not $version) { throw 'install: could not resolve the latest release tag' }
}

$base = if ($env:FLOWBATON_BASE_URL) { $env:FLOWBATON_BASE_URL } else { "https://github.com/$repo/releases/download/v$version" }
$asset = "flowbaton_${version}_windows_${arch}.zip"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("flowbaton-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp -Force -ErrorAction Stop | Out-Null
try {
	Write-Host "install: downloading $asset (v$version)"
	Invoke-WebRequest -UseBasicParsing -ErrorAction Stop "$base/$asset" -OutFile (Join-Path $tmp $asset)
	Invoke-WebRequest -UseBasicParsing -ErrorAction Stop "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

	if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { throw 'install: required tool not found: gh' }
	& gh attestation verify (Join-Path $tmp $asset) `
		--repo $repo `
		--signer-workflow "$repo/.github/workflows/release-publish.yml" `
		--source-ref "refs/tags/v$version" `
		--deny-self-hosted-runners | Out-Null
	if ($LASTEXITCODE -ne 0) { throw "install: GitHub build attestation verification failed for $asset" }

	$expected = (Select-String -Path (Join-Path $tmp 'checksums.txt') -Pattern " $([regex]::Escape($asset))$" -ErrorAction Stop |
		ForEach-Object { ($_.Line -split '\s+')[0] } | Select-Object -First 1)
	if (-not $expected) { throw "install: no checksum listed for $asset" }
	$actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $asset) -ErrorAction Stop).Hash.ToLower()
	if ($expected.ToLower() -ne $actual) { throw "install: checksum mismatch for $asset (expected $expected, got $actual)" }

	Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force -ErrorAction Stop
	$binary = Join-Path $tmp "flowbaton_${version}_windows_${arch}\flowbaton.exe"
	if (-not (Test-Path $binary)) { throw 'install: archive did not contain flowbaton.exe' }

	New-Item -ItemType Directory -Path $installDir -Force -ErrorAction Stop | Out-Null
	Copy-Item -Path $binary -Destination (Join-Path $installDir 'flowbaton.exe') -Force -ErrorAction Stop

	$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
	if ($userPath -notlike "*$installDir*") {
		[Environment]::SetEnvironmentVariable('Path', "$userPath;$installDir", 'User')
		Write-Host "install: added $installDir to your user PATH (restart the shell to pick it up)"
	}
	Write-Host "install: flowbaton v$version installed to $installDir\flowbaton.exe"
}
finally {
	Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
