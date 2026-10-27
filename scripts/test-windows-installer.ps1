# Runs only in disposable Windows CI runners, after packaging and icon captures.
param([Parameter(Mandatory=$true)][string]$Installer)
$ErrorActionPreference = 'Stop'
$defaultDir = Join-Path $env:LOCALAPPDATA 'Programs/NX Camera Info'
$registryPath = 'Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo'
$profile = Join-Path $env:APPDATA 'nx-camera-info-gui.exe'
$desktopShortcut = Join-Path ([Environment]::GetFolderPath('Desktop')) 'NX Camera Info.lnk'
$startShortcut = Join-Path ([Environment]::GetFolderPath('Programs')) 'NX Camera Info/NX Camera Info.lnk'
$scratch = Join-Path ([IO.Path]::GetTempPath()) ('nx-installer-tests-' + [Guid]::NewGuid())
New-Item -ItemType Directory $scratch | Out-Null
$checks = [System.Collections.Generic.List[string]]::new()
$processes = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function Record([string]$Message) { $checks.Add($Message); Write-Host "PASS: $Message" }
function Remove-TestFile([string]$Path) {
    # Windows/Defender can briefly retain a just-exited test executable.
    for ($i = 0; $i -lt 25; $i++) {
        try { Remove-Item -LiteralPath $Path -Force; return }
        catch { if ($i -eq 24) { throw }; Start-Sleep -Milliseconds 200 }
    }
}
function Run-Setup([string]$Directory, [bool]$Success = $true) {
    # NSIS /D= must be the final argument and must not be quoted, including spaces.
    $p = Start-Process -FilePath $Installer -ArgumentList "/S /D=$Directory" -PassThru
    try {
        if (-not $p.WaitForExit(180000)) { $p.Kill(); throw 'Setup timed out.' }
        Write-Host "Setup exit: $($p.ExitCode) (expected success=$Success)"
        Assert-True (($p.ExitCode -eq 0) -eq $Success) "Unexpected setup exit code $($p.ExitCode); expected success=$Success"
    } finally { $p.Dispose() }
}
function Run-Uninstall([string]$Directory, [bool]$Success = $true) {
    # Run a temporary copy directly, so the waited process reports the actual
    # uninstall result rather than the exit code of NSIS's copying parent.
    $copy = Join-Path $scratch ('uninstall-' + [Guid]::NewGuid() + '.exe')
    Copy-Item (Join-Path $Directory 'Uninstall.exe') $copy
    $p = Start-Process -FilePath $copy -ArgumentList "/S _?=$Directory" -PassThru
    try {
        if (-not $p.WaitForExit(90000)) { $p.Kill(); throw 'Uninstall timed out.' }
        Write-Host "Uninstall exit: $($p.ExitCode) (expected success=$Success)"
        Assert-True (($p.ExitCode -eq 0) -eq $Success) "Unexpected uninstall exit code $($p.ExitCode); expected success=$Success"
    } finally { $p.Dispose() }
    Remove-TestFile $copy
}
function Get-InstallLocation {
    $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::CurrentUser, [Microsoft.Win32.RegistryView]::Registry32)
    try {
        $key = $base.OpenSubKey($registryPath)
        if ($null -eq $key) { return $null }
        try { return $key.GetValue('InstallLocation') } finally { $key.Dispose() }
    } finally { $base.Dispose() }
}
function Assert-Installed([string]$Directory) {
    foreach ($name in @('nx-camera-info-gui.exe','Uninstall.exe','THIRD_PARTY_NOTICES.txt','GUI_THIRD_PARTY_NOTICES.txt')) {
        Assert-True (Test-Path (Join-Path $Directory $name)) "Missing installed file: $name"
    }
    Assert-True ((Get-InstallLocation) -eq $Directory) 'Missing or incorrect uninstall registration.'
    Assert-True (Test-Path $desktopShortcut) 'Missing desktop shortcut.'
    Assert-True (Test-Path $startShortcut) 'Missing Start-menu shortcut.'
}
function Start-App([string]$Directory) {
    $p = Start-Process -FilePath (Join-Path $Directory 'nx-camera-info-gui.exe') -PassThru
    $processes.Add($p)
    for ($i = 0; $i -lt 60; $i++) {
        Start-Sleep -Milliseconds 500
        $p.Refresh()
        if ($p.HasExited) { throw "The installed GUI exited before opening: $($p.ExitCode)" }
        if ($p.MainWindowHandle -ne 0 -and (Test-Path $profile)) { return $p }
    }
    throw 'The installed GUI did not open its native window and WebView2 profile.'
}
function Assert-Stopped([System.Diagnostics.Process]$Process) {
    Assert-True ($Process.WaitForExit(10000)) 'Installer did not close the installed app.'
    Assert-True ($Process.ExitCode -eq 0) "App did not shut down cleanly: $($Process.ExitCode)"
}

try {
    Assert-Installed $defaultDir
    Record 'Fresh installation creates the app, notices, shortcuts and uninstall registration'
    $installed = Join-Path $defaultDir 'nx-camera-info-gui.exe'
    $unrelated = Join-Path $scratch 'Other copy'
    New-Item -ItemType Directory $unrelated | Out-Null
    Copy-Item $installed (Join-Path $unrelated 'nx-camera-info-gui.exe')
    $other = Start-App $unrelated
    $running = Start-App $defaultDir
    Run-Setup $defaultDir
    Assert-Stopped $running
    Assert-Installed $defaultDir
    $other.Refresh()
    Assert-True (-not $other.HasExited) 'Setup closed an app at a different path with the same executable name.'
    $other.CloseMainWindow() | Out-Null
    Assert-Stopped $other
    Record 'Upgrade gracefully closes only the installed app; another copy stays running'

    $before = (Get-FileHash $installed).Hash
    $lock = [IO.File]::Open($installed, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        Run-Setup $defaultDir $false
        Assert-Installed $defaultDir
        Assert-True ((Get-FileHash $installed).Hash -eq $before) 'Failed setup changed the locked executable.'
        Run-Uninstall $defaultDir $false
        Assert-Installed $defaultDir
        Assert-True ((Get-FileHash $installed).Hash -eq $before) 'Failed uninstall changed the locked executable.'
    } finally { $lock.Dispose() }
    Record 'Locked executable makes setup/uninstall fail without removing the app or uninstall registration'

    $database = Join-Path $defaultDir 'user-database.sqlite'
    $export = Join-Path $scratch 'camera-export.csv'
    [IO.File]::WriteAllText($database, 'user database sentinel')
    [IO.File]::WriteAllText($export, 'user export sentinel')
    $legacyProfile = Join-Path $defaultDir 'nx-camera-info-gui.exe.WebView2'
    New-Item -ItemType Directory -Force $legacyProfile | Out-Null
    [IO.File]::WriteAllText((Join-Path $legacyProfile 'cache-test'), 'old cache')
    $running = Start-App $defaultDir
    Run-Uninstall $defaultDir
    Assert-Stopped $running
    foreach ($name in @('nx-camera-info-gui.exe','Uninstall.exe','THIRD_PARTY_NOTICES.txt','GUI_THIRD_PARTY_NOTICES.txt')) {
        Assert-True (-not (Test-Path (Join-Path $defaultDir $name))) "Uninstall left $name behind."
    }
    Assert-True ($null -eq (Get-InstallLocation)) 'Uninstall entry was left behind.'
    Assert-True (-not (Test-Path $desktopShortcut)) 'Desktop shortcut was left behind.'
    Assert-True (-not (Test-Path $startShortcut)) 'Start-menu shortcut was left behind.'
    Assert-True (-not (Test-Path $profile)) 'Roaming WebView2 data was left behind.'
    Assert-True (-not (Test-Path $legacyProfile)) 'Adjacent legacy WebView2 data was left behind.'
    Assert-True ([IO.File]::ReadAllText($database) -eq 'user database sentinel') 'User database was changed.'
    Assert-True ([IO.File]::ReadAllText($export) -eq 'user export sentinel') 'User export was changed.'
    Record 'Uninstall while running closes the GUI and removes app files, shortcuts, registration and cache; user files survive'

    # Recreate the reported old-uninstaller outcome: only the EXE is left,
    # there is no registered uninstaller, and that EXE is still running.
    Copy-Item 'dist/nx-camera-info-gui.exe' $installed
    $running = Start-App $defaultDir
    Run-Setup $defaultDir
    Assert-Stopped $running
    Assert-Installed $defaultDir
    Record 'Setup repairs an orphaned, running executable left by an older uninstaller'
    Remove-Item $database
    # Also exercise the normal entry point used by Add/Remove Programs, including
    # NSIS's temporary self-copy and self-deletion. -Wait waits for its children.
    $running = Start-App $defaultDir
    $normal = Start-Process -FilePath (Join-Path $defaultDir 'Uninstall.exe') -ArgumentList '/S' -Wait -PassThru
    Assert-True ($normal.ExitCode -eq 0) 'Normal uninstaller entry point returned an error.'
    Assert-Stopped $running
    Assert-True ($null -eq (Get-InstallLocation)) 'Normal uninstall left its registration behind.'
    Assert-True (-not (Test-Path $defaultDir)) 'Empty installation directory was not removed.'
    Record 'Normal Add/Remove Programs entry point closes the app and removes the empty installation directory'

    $custom = Join-Path $scratch 'Custom installation with spaces'
    Run-Setup $custom
    Assert-Installed $custom
    $running = Start-App $custom
    Run-Uninstall $custom
    Assert-Stopped $running
    Assert-True (-not (Test-Path $custom)) 'Custom installation directory was not removed.'
    Record 'Custom installation paths with spaces also close and uninstall cleanly'
    if ($env:NX_GUI_TEST_DIR) {
        @{ passed=$true; architecture=$env:PROCESSOR_ARCHITECTURE; checks=$checks.ToArray() } |
            ConvertTo-Json -Depth 4 | Set-Content (Join-Path $env:NX_GUI_TEST_DIR 'installer-result.json')
    }
} catch {
    Write-Host "Installer regression failure: $($_.Exception.Message)"
    Write-Host $_.ScriptStackTrace
    throw
} finally {
    foreach ($p in $processes) { if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force } }
    # Preserve the original assertion if cleanup of disposable test files fails.
    Remove-Item -Recurse -Force $scratch -ErrorAction Continue
}
