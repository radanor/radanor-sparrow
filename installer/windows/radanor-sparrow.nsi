; Reviewed P8 port of the historical per-user MUI installer.
; Complete immutable generations replace in-place payload overwrites.
Unicode true
!addincludedir "${__FILEDIR__}"
!include "packaging-mode.nsh"
SetCompressor /SOLID zlib
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "WordFunc.nsh"
!include "TextFunc.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!insertmacro VersionCompare
!define PRODUCT_NAME "Radanor Sparrow Community Edition"
!define EXE_NAME "RadanorSparrow.exe"
!ifndef APP_REG_KEY
 !define APP_REG_KEY "Software\Radanor\RadanorSparrow"
!endif
!ifndef UNINSTALL_REG_KEY
 !define UNINSTALL_REG_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\RadanorSparrow"
!endif
!ifndef MENU_FOLDER
 !define MENU_FOLDER "Radanor Sparrow"
!endif
!define SHORTCUT_NAME "Radanor Sparrow Community Edition.lnk"
!define PRODUCT_MARKER "RadanorSparrowCE-generation-v1"
Name "${PRODUCT_NAME}"
Caption "${PRODUCT_NAME} ${PRODUCT_VERSION} (${VARIANT} build)"
BrandingText "${PRODUCT_NAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\Radanor Sparrow"
InstallDirRegKey HKCU "${APP_REG_KEY}" "InstallLocation"
RequestExecutionLevel user
ShowInstDetails show
ShowUninstDetails show
Icon "${APP_ICON}"
UninstallIcon "${APP_ICON}"
VIProductVersion "${NUMERIC_VERSION}"
VIAddVersionKey "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey "CompanyName" "Radanor"
VIAddVersionKey "FileDescription" "${PRODUCT_NAME} (${VARIANT} build)"
VIAddVersionKey "FileVersion" "${PRODUCT_VERSION}"
VIAddVersionKey "ProductVersion" "${PRODUCT_VERSION}"
!define MUI_ABORTWARNING
!define MUI_ICON "${APP_ICON}"
!define MUI_UNICON "${APP_ICON}"
!define MUI_WELCOMEPAGE_TEXT "Install ${PRODUCT_NAME} for your Windows account.$\r$\n$\r$\nWebView2 Evergreen enables the native window. If unavailable, Sparrow opens its authenticated dashboard in your default browser. Setup does not download a runtime.$\r$\n$\r$\nUse Exit Radanor Sparrow before installing. Existing application data is preserved."
!define MUI_FINISHPAGE_TEXT "${PRODUCT_NAME} is installed. Open it from the Start Menu.$\r$\n$\r$\nRerun this setup to repair. Upgrades and uninstall preserve your application data."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
Var DesktopGuard
Var CoreGuard
Var GatewayGuard
Var SetupGuard
Var Generation
Var Root
Var OldGeneration
Var FailureCode

; Retain the same named objects production checks, closing the probe/start
; race. Existing objects and uncertain errors both fail closed; never kill.
!macro Guard NAME TARGET
 System::Call 'kernel32::CreateMutexW(p 0, i 0, w "Local\RadanorShield-${NAME}") p.r1 ? e'
 Pop $2
 ${If} $1 == 0
 ${OrIf} $2 != 0
  SetErrorLevel 2
  IfSilent +2
   MessageBox MB_OK|MB_ICONSTOP "Sparrow may be running or its locks are inaccessible. Use Exit Radanor Sparrow and retry. No process was terminated."
  Abort
 ${EndIf}
 StrCpy ${TARGET} $1
!macroend
!macro AcquireGuards
 !insertmacro Guard "installer" $SetupGuard
 !insertmacro Guard "desktop" $DesktopGuard
 !insertmacro Guard "core" $CoreGuard
 !insertmacro Guard "gateway" $GatewayGuard
!macroend
Function .onInit
 SetShellVarContext current
 ${IfNot} ${RunningX64}
  SetErrorLevel 5
  Abort
 ${EndIf}
 ${IfNot} ${AtLeastWin10}
  SetErrorLevel 5
  Abort
 ${EndIf}
 !insertmacro AcquireGuards
FunctionEnd
Function un.onInit
 SetShellVarContext current
 !insertmacro AcquireGuards
 ReadINIStr $Root "$INSTDIR\install.ini" "Sparrow" "Root"
 ReadINIStr $0 "$INSTDIR\install.ini" "Sparrow" "Product"
 ${GetParent} "$INSTDIR" $1
 ${GetParent} "$1" $2
 GetFullPathName $2 "$2"
 GetFullPathName $4 "$Root"
 StrCpy $Root $4
 ${If} $0 != "${PRODUCT_MARKER}"
 ${OrIf} $Root != $2
  SetErrorLevel 5
  Abort
 ${EndIf}
 ReadRegStr $0 HKCU "${APP_REG_KEY}" "InstallLocation"
 ${If} $0 != $Root
  SetErrorLevel 5
  Abort
 ${EndIf}
FunctionEnd
!macro DeleteGeneration PATH
 Delete "${PATH}\${EXE_NAME}"
 Delete "${PATH}\LICENSE"
 Delete "${PATH}\THIRD_PARTY_NOTICES.md"
 Delete "${PATH}\BUILD-INFO"
 Delete "${PATH}\Uninstall.exe"
 Delete "${PATH}\install.ini"
 RMDir "${PATH}"
!macroend
; A locked owned file must leave the ownership marker and registration available
; for a retry. Unknown files are intentionally retained by nonrecursive RMDir.
!macro DeleteOwnedFile PATH
 ${If} ${FileExists} "${PATH}"
  ClearErrors
  Delete "${PATH}"
  IfErrors un_failed
 ${EndIf}
!macroend
Section "Radanor Sparrow Community Edition" SecInstall
 SectionIn RO
 !ifdef ACCEPTANCE_DIAGNOSTIC
  FileOpen $9 "${ACCEPTANCE_DIAGNOSTIC}" w
  FileWrite $9 "input=$INSTDIR cmd=$CMDLINE$\r$\n"
  FileClose $9
 !endif
 System::Call 'kernel32::GetFullPathNameW(w "$INSTDIR", i ${NSIS_MAX_STRLEN}, w .r0, p 0) i.r8'
 ${If} $0 == ""
 ${OrIf} $8 == 0
 ${OrIf} $8 >= ${NSIS_MAX_STRLEN}
  SetErrorLevel 5
  Abort "Invalid installation directory."
 ${EndIf}
 StrCpy $INSTDIR $0
 ReadRegStr $0 HKCU "${APP_REG_KEY}" "InstallLocation"
 ${If} $0 != ""
 ${AndIf} $0 != $INSTDIR
  SetErrorLevel 5
  Abort "An installation is registered elsewhere. Uninstall it first; data is retained."
 ${EndIf}
 ReadRegStr $OldGeneration HKCU "${APP_REG_KEY}" "Current"
 ${If} $OldGeneration != ""
  ReadRegStr $0 HKCU "${APP_REG_KEY}" "NumericVersion"
  ReadRegStr $1 HKCU "${APP_REG_KEY}" "Sequence"
  ReadRegStr $2 HKCU "${APP_REG_KEY}" "Variant"
  ReadINIStr $3 "$OldGeneration\install.ini" "Sparrow" "Product"
  ${If} $0 == ""
  ${OrIf} $1 == ""
  ${OrIf} $3 != "${PRODUCT_MARKER}"
   SetErrorLevel 5
   Abort "Installed identity is incomplete; refusing uncertain replacement."
  ${EndIf}
  ${VersionCompare} "${NUMERIC_VERSION}" "$0" $3
  ${If} $3 == 2
   Goto downgrade
  ${EndIf}
  ${If} $2 == "release"
  ${AndIf} "${VARIANT}" != "release"
   Goto downgrade
  ${EndIf}
  ${If} $3 == 0
   IntCmp ${BUILD_SEQUENCE} $1 order_ok downgrade order_ok
  ${EndIf}
 ${Else}
  ReadRegStr $0 HKCU "${APP_REG_KEY}" "InstallLocation"
  ${If} $0 != ""
   SetErrorLevel 5
   Abort "Legacy installation requires explicit removal first."
  ${EndIf}
 ${EndIf}
 order_ok:
 ${If} $OldGeneration != ""
  ReadRegStr $4 HKCU "${APP_REG_KEY}" "Commit"
  ${If} $4 == ""
   Goto downgrade
  ${EndIf}
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=ancestry.txt "${ANCESTRY}"
  FileOpen $5 "$PLUGINSDIR\ancestry.txt" r
  ancestry_loop:
  ClearErrors
  FileRead $5 $6
  IfErrors ancestry_missing
  ${TrimNewLines} $6 $6
  StrCmp $6 $4 ancestry_ok ancestry_loop
  ancestry_missing:
  FileClose $5
  Goto downgrade
  ancestry_ok:
  FileClose $5
 ${EndIf}
 ClearErrors
 CreateDirectory "$INSTDIR\versions"
 IfErrors root_failed
 GetTempFileName $Generation "$INSTDIR\versions"
 IfErrors root_failed
 StrCmp $Generation "" root_failed
 Delete "$Generation"
 CreateDirectory "$Generation"
 ClearErrors
 StrCpy $FailureCode 43
 SetOutPath "$Generation"
 IfErrors stage_failed
 ; Mark even a partial generation; delete only allowlisted names later.
 WriteINIStr "$Generation\install.ini" "Sparrow" "Product" "${PRODUCT_MARKER}"
 WriteINIStr "$Generation\install.ini" "Sparrow" "Root" "$INSTDIR"
 StrCpy $FailureCode 44
 IfErrors stage_failed
 File "${PAYLOAD_EXE}"
 StrCpy $FailureCode 45
 IfErrors stage_failed
 File "${PAYLOAD_LICENSE}"
 StrCpy $FailureCode 46
 IfErrors stage_failed
 File "${PAYLOAD_NOTICES}"
 StrCpy $FailureCode 47
 IfErrors stage_failed
 File "${PAYLOAD_BUILD_INFO}"
 StrCpy $FailureCode 48
 IfErrors stage_failed
 WriteUninstaller "$Generation\Uninstall.exe"
 StrCpy $FailureCode 49
 IfErrors stage_failed
 StrCpy $FailureCode 41
 !ifdef ACCEPTANCE_PAUSE_AFTER_STAGE
  Sleep 30000
 !endif
 !ifdef ACCEPTANCE_FAIL_AFTER_STAGE
  Goto stage_failed
 !endif
 ; Targets always name complete generations, even if power fails between
 ; registry writes. Prior payloads are retained until explicit uninstall.
 CreateDirectory "$SMPROGRAMS\${MENU_FOLDER}"
 StrCpy $FailureCode 50
 IfErrors stage_failed
 ClearErrors
 CreateShortCut "$SMPROGRAMS\${MENU_FOLDER}\pending.lnk" "$Generation\${EXE_NAME}" "" "$Generation\${EXE_NAME}" 0
 IfErrors stage_failed
 WriteRegStr HKCU "${UNINSTALL_REG_KEY}" "DisplayName" "${PRODUCT_NAME}"
 WriteRegStr HKCU "${UNINSTALL_REG_KEY}" "DisplayVersion" "${PRODUCT_VERSION}"
 WriteRegStr HKCU "${UNINSTALL_REG_KEY}" "DisplayIcon" "$Generation\${EXE_NAME}"
 WriteRegStr HKCU "${UNINSTALL_REG_KEY}" "UninstallString" '$\"$Generation\Uninstall.exe$\"'
 WriteRegStr HKCU "${UNINSTALL_REG_KEY}" "QuietUninstallString" '$\"$Generation\Uninstall.exe$\" /S'
 WriteRegDWORD HKCU "${UNINSTALL_REG_KEY}" "NoModify" 1
 WriteRegDWORD HKCU "${UNINSTALL_REG_KEY}" "NoRepair" 1
 WriteRegStr HKCU "${APP_REG_KEY}" "InstallLocation" "$INSTDIR"
 WriteRegStr HKCU "${APP_REG_KEY}" "NumericVersion" "${NUMERIC_VERSION}"
 WriteRegStr HKCU "${APP_REG_KEY}" "Sequence" "${BUILD_SEQUENCE}"
 WriteRegStr HKCU "${APP_REG_KEY}" "Variant" "${VARIANT}"
 WriteRegStr HKCU "${APP_REG_KEY}" "Commit" "${BUILD_COMMIT}"
 WriteRegStr HKCU "${APP_REG_KEY}" "Current" "$Generation"
 IfErrors registration_failed
 System::Call 'kernel32::MoveFileExW(w "$SMPROGRAMS\${MENU_FOLDER}\pending.lnk", w "$SMPROGRAMS\${MENU_FOLDER}\${SHORTCUT_NAME}", i 9) i.r0'
 ${If} $0 == 0
  Goto registration_failed
 ${EndIf}
 System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
 SetErrorLevel 0
 Goto done
 downgrade:
 SetErrorLevel 3
 Abort "Downgrade or incompatible build refused."
 root_failed:
 SetErrorLevel 43
 Abort "Cannot create a private payload generation. No existing files were changed."
 stage_failed:
 !ifdef ACCEPTANCE_DIAGNOSTIC
  FileOpen $0 "${ACCEPTANCE_DIAGNOSTIC}" a
  FileWrite $0 "root=$INSTDIR generation=$Generation menu=$SMPROGRAMS\${MENU_FOLDER}"
  FileClose $0
 !endif
 SetOutPath "$TEMP"
 !insertmacro DeleteGeneration "$Generation"
 Delete "$SMPROGRAMS\${MENU_FOLDER}\pending.lnk"
 SetErrorLevel $FailureCode
 Abort "Staging failed. Previous complete generations were not changed."
 registration_failed:
 SetErrorLevel 42
 Abort "Activation incomplete. Rerun setup to repair; complete payloads retained."
 done:
SectionEnd
Section "Uninstall"
 SetShellVarContext current
 SetOutPath "$TEMP"
 FindFirst $0 $1 "$Root\versions\*.*"
 loop:
 ${If} $1 == ""
  Goto endloop
 ${EndIf}
 ${If} $1 != "."
 ${AndIf} $1 != ".."
  ReadINIStr $2 "$Root\versions\$1\install.ini" "Sparrow" "Product"
  ReadINIStr $3 "$Root\versions\$1\install.ini" "Sparrow" "Root"
  ${If} $2 == "${PRODUCT_MARKER}"
  ${AndIf} $3 == $Root
   !insertmacro DeleteOwnedFile "$Root\versions\$1\${EXE_NAME}"
   !insertmacro DeleteOwnedFile "$Root\versions\$1\LICENSE"
   !insertmacro DeleteOwnedFile "$Root\versions\$1\THIRD_PARTY_NOTICES.md"
   !insertmacro DeleteOwnedFile "$Root\versions\$1\BUILD-INFO"
   !insertmacro DeleteOwnedFile "$Root\versions\$1\Uninstall.exe"
   !insertmacro DeleteOwnedFile "$Root\versions\$1\install.ini"
   RMDir "$Root\versions\$1"
  ${EndIf}
 ${EndIf}
 FindNext $0 $1
 Goto loop
 endloop:
 FindClose $0
 RMDir "$Root\versions"
 RMDir "$Root"
 Delete "$SMPROGRAMS\${MENU_FOLDER}\${SHORTCUT_NAME}"
 Delete "$SMPROGRAMS\${MENU_FOLDER}\pending.lnk"
 RMDir "$SMPROGRAMS\${MENU_FOLDER}"
 DeleteRegKey HKCU "${UNINSTALL_REG_KEY}"
 DeleteRegKey HKCU "${APP_REG_KEY}"
 System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
 SetErrorLevel 0
 Goto un_done
 un_failed:
 FindClose $0
 SetErrorLevel 51
 Abort "An installed file is locked. Registration retained; close the locking program and retry uninstall."
 un_done:
SectionEnd
