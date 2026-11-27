; TrunkLine's Windows setup. Built by packaging/dist.sh:
;   makensis -DVERSION=X.Y.Z -DAPPEXE=<trunkline.exe> -DAPPARCH=amd64|arm64 -DLICENSEFILE=<LICENSE> -DICONFILE=<trunkline.ico> -DOUTFILE=<setup.exe> installer.nsi
; Installs per user (no admin prompt) into %LOCALAPPDATA%\Programs\TrunkLine.
; Run with /S (as the app's updater does) it installs silently, then starts
; the new version. The setup itself is a 32-bit x86 program, so it runs on
; any Windows; APPARCH is the program it carries.

Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"

!ifndef APPARCH
  !define APPARCH amd64
!endif

; Signing: SIGNCMD (packaging/windows/sign.sh) signs the uninstaller as it's
; written into the setup, then the finished setup.
!ifdef SIGNCMD
  !uninstfinalize '"${SIGNCMD}" "%1"' = 0
  !finalize '"${SIGNCMD}" "%1"' = 0
!endif

; An ARM64 program can't run on an x64 PC: say so instead of installing it.
; (The x64 one runs on ARM64 Windows 11, emulated.)
Function .onInit
  !if "${APPARCH}" == "arm64"
    ${IfNot} ${IsNativeARM64}
      MessageBox MB_ICONSTOP "This is TrunkLine for ARM64 PCs. This PC isn't one: download trunkline-${VERSION}-setup.exe instead." /SD IDOK
      Abort
    ${EndIf}
  !endif
FunctionEnd

!define UNINST "Software\Microsoft\Windows\CurrentVersion\Uninstall\TrunkLine"
!define WEBVIEW2 "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

Name "TrunkLine"
OutFile "${OUTFILE}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\TrunkLine"
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "TrunkLine"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "TrunkLine SVN client setup"
VIAddVersionKey "LegalCopyright" "Franco Coetzer, GNU GPL v3"

!define MUI_ICON "${ICONFILE}"
!define MUI_UNICON "${ICONFILE}"
; Pictures drawn by make_icon.py, next to this script (makensis runs in its folder).
!define MUI_WELCOMEFINISHPAGE_BITMAP "installer-side.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "installer-side.bmp"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "installer-header.bmp"
!define MUI_WELCOMEPAGE_TEXT "This installs TrunkLine, a desktop client for Subversion (SVN), for you only: no administrator rights needed.$\r$\n$\r$\nTrunkLine uses the svn command-line client. If it isn't installed yet, install TortoiseSVN and tick its $\"command line client tools$\".$\r$\n$\r$\nClick Next to continue."
!define MUI_FINISHPAGE_RUN "$INSTDIR\trunkline.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Start TrunkLine"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${LICENSEFILE}"
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  ; A running copy (the updater's) would keep the .exe locked.
  nsExec::Exec 'taskkill /IM trunkline.exe /F'
  Sleep 500

  SetOutPath "$INSTDIR"
  File "/oname=trunkline.exe" "${APPEXE}"
  File "/oname=LICENSE.txt" "${LICENSEFILE}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateShortcut "$SMPROGRAMS\TrunkLine.lnk" "$INSTDIR\trunkline.exe"

  WriteRegStr HKCU "${UNINST}" "DisplayName" "TrunkLine"
  WriteRegStr HKCU "${UNINST}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST}" "Publisher" "Franco Coetzer"
  WriteRegStr HKCU "${UNINST}" "URLInfoAbout" "https://github.com/FrancoCoetzer/TrunkLine"
  WriteRegStr HKCU "${UNINST}" "DisplayIcon" "$INSTDIR\trunkline.exe"
  WriteRegStr HKCU "${UNINST}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINST}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST}" "NoRepair" 1

  ; The window needs the WebView2 runtime (built into Windows 11 and most
  ; Windows 10 machines).
  ${IfNot} ${Silent}
    ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\${WEBVIEW2}" "pv"
    ${If} $0 == ""
      ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2}" "pv"
    ${EndIf}
    ${If} $0 == ""
    ${OrIf} $0 == "0.0.0.0"
      MessageBox MB_YESNO|MB_ICONINFORMATION "TrunkLine needs the Microsoft Edge WebView2 Runtime, which is not installed.$\n$\nOpen the download page now?" IDNO +2
      ExecShell "open" "https://go.microsoft.com/fwlink/p/?LinkId=2124703"
    ${EndIf}
  ${EndIf}

  ${If} ${Silent}
    Exec '"$INSTDIR\trunkline.exe"'
  ${EndIf}
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /IM trunkline.exe /F'
  Sleep 500
  Delete "$INSTDIR\trunkline.exe"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\TrunkLine.lnk"
  DeleteRegKey HKCU "${UNINST}"
SectionEnd
