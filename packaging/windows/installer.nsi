; TrunkLine's Windows setup. Built by make dist:
;   makensis -DVERSION=X.Y.Z -DAPPEXE=<trunkline.exe> -DLICENSEFILE=<LICENSE> -DOUTFILE=<setup.exe> installer.nsi
; Installs per user (no admin prompt) into %LOCALAPPDATA%\Programs\TrunkLine.
; Run with /S (as the app's updater does) it installs silently, then starts
; the new version.

Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"

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

!define MUI_FINISHPAGE_RUN "$INSTDIR\trunkline.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Start TrunkLine"
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
