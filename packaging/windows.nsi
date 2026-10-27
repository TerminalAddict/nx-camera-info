Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
Name "NX Camera Info"
OutFile "..\dist\installers\nx-camera-info-gui-0.2.1-windows-${ARCH}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\NX Camera Info"
RequestExecutionLevel user
SetCompressor /SOLID lzma
VIProductVersion "0.2.1.0"
VIAddVersionKey "ProductName" "NX Camera Info"
VIAddVersionKey "FileDescription" "NX Camera Info Setup"
VIAddVersionKey "FileVersion" "0.2.1"
VIAddVersionKey "ProductVersion" "0.2.1"
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
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
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
  SetOutPath "$INSTDIR"
  File "..\dist\nx-camera-info-gui.exe"
  File "..\THIRD_PARTY_NOTICES.txt"
  File "..\GUI_THIRD_PARTY_NOTICES.txt"
  CreateDirectory "$SMPROGRAMS\NX Camera Info"
  CreateShortcut "$SMPROGRAMS\NX Camera Info\NX Camera Info.lnk" "$INSTDIR\nx-camera-info-gui.exe"
  CreateShortcut "$DESKTOP\NX Camera Info.lnk" "$INSTDIR\nx-camera-info-gui.exe"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "DisplayName" "NX Camera Info"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "DisplayVersion" "0.2.1"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo" "DisplayIcon" "$INSTDIR\nx-camera-info-gui.exe"
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\nx-camera-info-gui.exe"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.txt"
  Delete "$INSTDIR\GUI_THIRD_PARTY_NOTICES.txt"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$DESKTOP\NX Camera Info.lnk"
  Delete "$SMPROGRAMS\NX Camera Info\NX Camera Info.lnk"
  RMDir "$SMPROGRAMS\NX Camera Info"
  SetRegView 32
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\NXCameraInfo"
  ; WebView2 is shared with other applications and is intentionally retained.
SectionEnd
