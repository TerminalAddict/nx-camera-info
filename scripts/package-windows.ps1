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
$installer = Resolve-Path "dist/installers/nx-camera-info-gui-0.3.0-windows-$Arch-setup.exe"
$process = Start-Process -FilePath $installer -ArgumentList '/S' -Wait -PassThru
if ($process.ExitCode -ne 0) { throw "Installer exited $($process.ExitCode)" }
$installed = "$env:LOCALAPPDATA\Programs\NX Camera Info\nx-camera-info-gui.exe"
if (-not (Test-Path $installed)) { throw 'Installer did not create the application.' }
$process = Start-Process -FilePath $installed -ArgumentList '--version' -Wait -PassThru
if ($process.ExitCode -ne 0) { throw 'Installed application failed its version check.' }
if ($env:NX_GUI_TEST_DIR) {
    Add-Type -AssemblyName System.Windows.Forms,System.Drawing
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public struct NXWindowRect { public int Left, Top, Right, Bottom; }
public static class NXInstallerWindow {
    [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out NXWindowRect rectangle);
}
'@
    [NXInstallerWindow]::SetProcessDPIAware() | Out-Null
    # Inspect the icons embedded in the actual shipped and installed executables.
    foreach ($entry in @(@('installed-icon', $installed), @('setup-icon', "$installer"), @('uninstall-icon', "$env:LOCALAPPDATA\Programs\NX Camera Info\Uninstall.exe"))) {
        $icon = [System.Drawing.Icon]::ExtractAssociatedIcon($entry[1])
        if ($null -eq $icon) { throw "Missing executable icon: $($entry[0])" }
        $bitmap = $icon.ToBitmap()
        $bitmap.Save((Join-Path $env:NX_GUI_TEST_DIR "$($entry[0]).png"))
        $bitmap.Dispose()
        $icon.Dispose()
    }
    # Capture the real branded welcome page without starting a second installation.
    $preview = Start-Process -FilePath $installer -PassThru
    try {
        for ($i = 0; $i -lt 45; $i++) {
            Start-Sleep -Seconds 1
            $preview.Refresh()
            if ($preview.HasExited) { throw 'Installer preview exited before showing its welcome page.' }
            if ($preview.MainWindowHandle -ne 0) { break }
        }
        if ($preview.MainWindowHandle -eq 0) { throw 'Installer welcome page did not appear.' }
        Start-Sleep -Seconds 2
        $rectangle = New-Object NXWindowRect
        if (-not [NXInstallerWindow]::GetWindowRect($preview.MainWindowHandle, [ref]$rectangle)) {
            throw 'Could not locate the installer window for its screenshot.'
        }
        $bounds = [System.Drawing.Rectangle]::FromLTRB($rectangle.Left, $rectangle.Top, $rectangle.Right, $rectangle.Bottom)
        $bitmap = New-Object System.Drawing.Bitmap($bounds.Width, $bounds.Height)
        $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
        $graphics.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
        $bitmap.Save((Join-Path $env:NX_GUI_TEST_DIR 'installer.png'))
        $graphics.Dispose()
        $bitmap.Dispose()
    } finally {
        if (-not $preview.HasExited) { Stop-Process -Id $preview.Id -Force }
    }
}
Get-FileHash dist/WebView2Setup.exe | Format-List
