param([ValidateSet('amd64','arm64')][string]$Arch)
$ErrorActionPreference = 'Stop'
$link = if ($Arch -eq 'arm64') { '2099616' } else { '2124701' }
New-Item -ItemType Directory -Force dist/installers | Out-Null
Invoke-WebRequest "https://go.microsoft.com/fwlink/?linkid=$link" -OutFile dist/WebView2Setup.exe
$signature = Get-AuthenticodeSignature dist/WebView2Setup.exe
if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') {
    throw 'The downloaded WebView2 installer does not have a valid Microsoft signature.'
}
if ((Get-Item dist/WebView2Setup.exe).Length -lt 30000000) { throw 'Expected the full offline WebView2 installer.' }
$nsis = "${env:ProgramFiles(x86)}\NSIS\makensis.exe"
if (-not (Test-Path $nsis)) {
    choco install nsis --yes --no-progress
    if ($LASTEXITCODE -ne 0) { throw 'Could not install NSIS.' }
}
& $nsis "/DARCH=$Arch" packaging/windows.nsi
if ($LASTEXITCODE -ne 0) { throw 'NSIS packaging failed.' }
# Install into the disposable CI runner and verify the installed executable.
$installer = Resolve-Path "dist/installers/nx-camera-info-gui-0.2.0-windows-$Arch-setup.exe"
$process = Start-Process -FilePath $installer -ArgumentList '/S' -Wait -PassThru
if ($process.ExitCode -ne 0) { throw "Installer exited $($process.ExitCode)" }
$installed = "$env:LOCALAPPDATA\Programs\NX Camera Info\nx-camera-info-gui.exe"
if (-not (Test-Path $installed)) { throw 'Installer did not create the application.' }
$process = Start-Process -FilePath $installed -ArgumentList '--version' -Wait -PassThru
if ($process.ExitCode -ne 0) { throw 'Installed application failed its version check.' }
Get-FileHash dist/WebView2Setup.exe | Format-List
