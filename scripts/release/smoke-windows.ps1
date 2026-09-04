param(
	[Parameter(Mandatory = $true)][string]$Candidate,
	[Parameter(Mandatory = $true)][string]$Version
)
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("flowbaton-smoke-" + [guid]::NewGuid())
$http = Join-Path $tmp "http\v$Version"
$smokeHome = Join-Path $tmp 'home'
$bin = Join-Path $tmp 'bin'
New-Item -ItemType Directory -Force -Path $http, $smokeHome, $bin -ErrorAction Stop | Out-Null
Copy-Item -Path (Join-Path $Candidate '*') -Destination $http -Force -ErrorAction Stop
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
$listener.Stop()
$server = Start-Process python -ArgumentList '-m', 'http.server', $port, '--bind', '127.0.0.1', '--directory', (Join-Path $tmp 'http') -PassThru -WindowStyle Hidden -ErrorAction Stop
try {
	Start-Sleep -Seconds 1
	$env:USERPROFILE = $smokeHome
	$env:HOME = $smokeHome
	$env:FLOWBATON_VERSION = $Version
	$env:FLOWBATON_BASE_URL = "http://127.0.0.1:$port/v$Version"
	$env:FLOWBATON_INSTALL_DIR = $bin
	& (Join-Path $env:GITHUB_WORKSPACE 'scripts\install.ps1')
	$actual = & (Join-Path $bin 'flowbaton.exe') --version
	if ($actual -ne "flowbaton $Version") { throw "installed binary reported '$actual'" }
	$env:FLOWBATON_DRIVER_ASSET_BASE_URL = "http://127.0.0.1:$port"
	& (Join-Path $bin 'flowbaton.exe') driver-setup -p android
	if ($LASTEXITCODE -ne 0) { throw 'driver-setup failed from the installed Windows archive' }
}
finally {
	Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
	Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
