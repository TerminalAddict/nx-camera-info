Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"
Name "NX Camera Info"
OutFile "..\dist\installers\nx-camera-info-gui-0.5.0-windows-${ARCH}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\NX Camera Info"
InstallDirRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "InstallLocation"
RequestExecutionLevel user
SetCompressor /SOLID lzma
VIProductVersion "0.5.0.0"
VIAddVersionKey "ProductName" "NX Camera Info"
VIAddVersionKey "FileDescription" "NX Camera Info Setup"
VIAddVersionKey "FileVersion" "0.5.0"
VIAddVersionKey "ProductVersion" "0.5.0"
BrandingText "NX Camera Info"
!define MUI_ABORTWARNING
!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "installer-header.bmp"
!define MUI_HEADERIMAGE_UNBITMAP "installer-header.bmp"
!define MUI_WELCOMEFINISHPAGE_BITMAP "installer-sidebar.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "installer-sidebar.bmp"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\nx-camera-info-gui.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open NX Camera Info"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Var MaintenanceMutex
Var UpdatePID

; Serialize setup and removal for this app. Keep the mutex handle until exit.
!macro MaintenanceInit PREFIX
Function ${PREFIX}MaintenanceInit
  SetShellVarContext current
  SetRegView 32
  System::Call 'kernel32::CreateMutexW(p 0, i 0, w "Local\NXCameraInfoMaintenance") p.r0 ?e'
  Pop $1
  StrCpy $MaintenanceMutex $0
  ${If} $0 == 0
  ${OrIf} $1 == 183
    IfSilent +2
    MessageBox MB_ICONSTOP "Another NX Camera Info setup or uninstall is running. Finish it, then try again."
    SetErrorLevel 1
    Abort
  ${EndIf}
FunctionEnd
!macroend
!insertmacro MaintenanceInit ""
!insertmacro MaintenanceInit "un."

; The helper requests a normal window close for this exact installed executable,
; waits for exit and checks locks. Failure leaves the uninstall entry intact.
!macro RunMaintenance PREFIX MODE
Function ${PREFIX}RunMaintenance
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=nx-maintenance.exe "..\dist\nx-maintenance.exe"
  maintenance_retry:
    nsExec::ExecToStack '"$PLUGINSDIR\nx-maintenance.exe" ${MODE} "$INSTDIR" "$PLUGINSDIR"'
    Pop $0
    Pop $1
    StrCmp $0 "0" maintenance_done
    IfSilent maintenance_failed
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "$1$\r$\n$\r$\nClose NX Camera Info or resolve the file access problem, then click Retry. The operation has not completed." IDRETRY maintenance_retry
  maintenance_failed:
    SetErrorLevel 1
    Abort
  maintenance_done:
FunctionEnd
!macroend
!insertmacro RunMaintenance "" "install"
!insertmacro RunMaintenance "un." "uninstall"

Function .onInit
  Call MaintenanceInit
  ${GetParameters} $0
  ${GetOptions} $0 "/WAITPID=" $UpdatePID
  ${If} $UpdatePID != ""
    InitPluginsDir
    SetOutPath "$PLUGINSDIR"
    File /oname=nx-maintenance.exe "..\dist\nx-maintenance.exe"
    nsExec::ExecToStack '"$PLUGINSDIR\nx-maintenance.exe" wait "$UpdatePID"'
    Pop $0
    Pop $1
    ${If} $0 != "0"
      IfSilent +2
      MessageBox MB_ICONSTOP "$1"
      SetErrorLevel 1
      Abort
    ${EndIf}
  ${EndIf}
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "This installer requires 64-bit Windows."
    Abort
  ${EndIf}
  !if "${ARCH}" == "arm64"
    ReadEnvStr $0 PROCESSOR_ARCHITECTURE
    ReadEnvStr $1 PROCESSOR_ARCHITEW6432
    ${If} $0 != "ARM64"
    ${AndIf} $1 != "ARM64"
      MessageBox MB_ICONSTOP "Choose the AMD64 installer for Intel/AMD computers."
      Abort
    ${EndIf}
  !endif
FunctionEnd

Function un.onInit
  Call un.MaintenanceInit
FunctionEnd

Function CheckWebView
  SetRegView 32
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${EndIf}
FunctionEnd

Section "Install"
  Call CheckWebView
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    InitPluginsDir
    SetOutPath "$PLUGINSDIR"
    File /oname=WebView2Setup.exe "..\dist\WebView2Setup.exe"
    DetailPrint "Installing the included Microsoft WebView2 Runtime…"
    ExecWait '"$PLUGINSDIR\WebView2Setup.exe" /silent /install' $1
    Call CheckWebView
    ${If} $0 == ""
    ${OrIf} $0 == "0.0.0.0"
      MessageBox MB_ICONSTOP "WebView2 installation did not complete (exit $1). Install the Microsoft WebView2 Runtime and run setup again."
      SetErrorLevel 1
      Abort
    ${EndIf}
  ${EndIf}
  ; Stage payloads away from the installed executable. The helper replaces files
  ; only after a successful close/lock check, using same-directory atomic moves.
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "..\dist\nx-camera-info-gui.exe"
  File "..\THIRD_PARTY_NOTICES.txt"
  File "..\GUI_THIRD_PARTY_NOTICES.txt"
  DetailPrint "Closing NX Camera Info and installing application files…"
  Call RunMaintenance
  ClearErrors
  CreateDirectory "$SMPROGRAMS\NX Camera Info"
  CreateShortcut "$SMPROGRAMS\NX Camera Info\NX Camera Info.lnk" "$INSTDIR\nx-camera-info-gui.exe"
  CreateShortcut "$DESKTOP\NX Camera Info.lnk" "$INSTDIR\nx-camera-info-gui.exe"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "DisplayName" "NX Camera Info"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "DisplayVersion" "0.5.0"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "DisplayIcon" "$INSTDIR\nx-camera-info-gui.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "QuietUninstallString" '$\"$INSTDIR\Uninstall.exe$\" /S'
  ${If} ${Errors}
    IfSilent +2
    MessageBox MB_ICONSTOP "Setup could not finish creating the shortcuts or uninstall entry. Check access to the installation folder and run setup again."
    SetErrorLevel 1
    Abort
  ${EndIf}
SectionEnd

Section "Uninstall"
  DetailPrint "Closing NX Camera Info and removing its application files and WebView2 data…"
  Call un.RunMaintenance
  ; Keep the uninstaller and registry entry until all app-owned cleanup succeeds.
  SetOutPath "$TEMP"
  ClearErrors
  Delete "$DESKTOP\NX Camera Info.lnk"
  Delete "$SMPROGRAMS\NX Camera Info\NX Camera Info.lnk"
  ${If} ${Errors}
    IfSilent +2
    MessageBox MB_ICONSTOP "Some shortcuts could not be removed. Close any programs using them and run uninstall again."
    SetErrorLevel 1
    Abort
  ${EndIf}
  RMDir "$SMPROGRAMS\NX Camera Info"
  ClearErrors
  Delete "$INSTDIR\Uninstall.exe"
  ${If} ${Errors}
    IfSilent +2
    MessageBox MB_ICONSTOP "The uninstall executable could not be removed. Close any programs using it and run uninstall again."
    SetErrorLevel 1
    Abort
  ${EndIf}
  ; Never recursively remove the installation directory: it may contain the
  ; user's database, exports, or other files, especially with a custom location.
  RMDir "$INSTDIR"
  ${If} ${Errors}
    DetailPrint "The installation folder contains other files and has been retained."
  ${EndIf}
  ClearErrors
  SetRegView 32
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo"
  ${If} ${Errors}
    ; Leave a retryable uninstaller if a registry policy prevents final cleanup.
    CreateDirectory "$INSTDIR"
    CopyFiles /SILENT "$EXEPATH" "$INSTDIR\Uninstall.exe"
    IfSilent +2
    MessageBox MB_ICONSTOP "The uninstall entry could not be removed. The uninstaller has been retained so you can retry after resolving registry access."
    SetErrorLevel 1
    Abort
  ${EndIf}
  ; WebView2 is shared with other applications and is intentionally retained.
SectionEnd
