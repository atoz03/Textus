; Textus' installer hooks (`windows.installerInclude` in mygo.json): what
; keeps Textus under "Open with" for .md and .markdown, and in Settings >
; Apps > Default apps, whichever app holds the default. See
; docs/dev/windows-installer.md > File associations.
;
; MyGo's installer registers a ProgID of its own for each extension in
; OpenWithProgids (com.catvinci.textus.md). The values below are Textus'
; own, so another editor's installer overwrites none of them, and they name
; the ProgID Settings > General > Default Markdown editor writes too
; (backend/default_editor.go, windowsRegistryValues) - change both.
;
; Per-user, like the install: everything here is under HKCU.

!include "LogicLib.nsh"
!include "WordFunc.nsh"

!define TEXTUS_PROGID "Textus.Markdown"
!define TEXTUS_CAPABILITIES "Software\Textus\Capabilities"
!define TEXTUS_APPKEY "Software\Classes\Applications\Textus.exe"

!macro TEXTUS_REGISTER_EXT EXT
  WriteRegNone HKCU "Software\Classes\${EXT}\OpenWithProgids" "${TEXTUS_PROGID}"
  WriteRegStr HKCU "${TEXTUS_APPKEY}\SupportedTypes" "${EXT}" ""
  WriteRegStr HKCU "${TEXTUS_CAPABILITIES}\FileAssociations" "${EXT}" "${TEXTUS_PROGID}"
!macroend

!macro TEXTUS_UNREGISTER_EXT EXT
  DeleteRegValue HKCU "Software\Classes\${EXT}\OpenWithProgids" "${TEXTUS_PROGID}"
  DeleteRegKey /ifempty HKCU "Software\Classes\${EXT}\OpenWithProgids"
!macroend

!macro customInstall
  WriteRegStr HKCU "Software\Classes\${TEXTUS_PROGID}" "" "Markdown document"
  WriteRegStr HKCU "Software\Classes\${TEXTUS_PROGID}\shell\open\command" "" '"$INSTDIR\Textus.exe" "%1"'
  ; A document's own icon (paper and quill), not the app's: resources/windows/doc.ico.
  WriteRegStr HKCU "Software\Classes\${TEXTUS_PROGID}\DefaultIcon" "" '"$INSTDIR\doc.ico"'

  WriteRegStr HKCU "${TEXTUS_APPKEY}" "FriendlyAppName" "Textus"
  WriteRegStr HKCU "${TEXTUS_APPKEY}\shell\open\command" "" '"$INSTDIR\Textus.exe" "%1"'
  WriteRegStr HKCU "${TEXTUS_CAPABILITIES}" "ApplicationName" "Textus"
  WriteRegStr HKCU "${TEXTUS_CAPABILITIES}" "ApplicationDescription" "Integrated Writing Environment"
  WriteRegStr HKCU "Software\RegisteredApplications" "Textus" "${TEXTUS_CAPABILITIES}"
  !insertmacro TEXTUS_REGISTER_EXT ".md"
  !insertmacro TEXTUS_REGISTER_EXT ".markdown"

  ; Explorer caches file types until the next sign-in otherwise.
  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
!macroend

!macro customUnInstall
  !insertmacro TEXTUS_UNREGISTER_EXT ".md"
  !insertmacro TEXTUS_UNREGISTER_EXT ".markdown"
  DeleteRegKey HKCU "Software\Classes\${TEXTUS_PROGID}"
  DeleteRegKey HKCU "${TEXTUS_APPKEY}"
  DeleteRegValue HKCU "Software\RegisteredApplications" "Textus"
  DeleteRegKey HKCU "${TEXTUS_CAPABILITIES}"
  DeleteRegKey /ifempty HKCU "Software\Textus"

  ; The `textus` command Textus installs itself (backend/cli_install_windows.go).
  ; A PATH as long as NSIS' strings comes back cut short, so it's left alone.
  Delete "$INSTDIR\bin\textus.cmd"
  RMDir "$INSTDIR\bin"
  ReadRegStr $R0 HKCU "Environment" "Path"
  StrLen $R1 $R0
  IntOp $R1 $R1 + 1
  ${If} $R1 < ${NSIS_MAX_STRLEN}
    ${un.WordAdd} $R0 ";" "-$INSTDIR\bin" $R2
    ; Written back only when exactly our folder (and a ";") is gone:
    ; anything shorter would lose the user's own entries.
    StrLen $R3 $R2
    StrLen $R4 "$INSTDIR\bin"
    IntOp $R3 $R3 + $R4
    IntOp $R3 $R3 + 1
    StrLen $R4 $R0
    ${If} $R2 != $R0
    ${AndIf} $R3 >= $R4
      WriteRegExpandStr HKCU "Environment" "Path" $R2
      SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
    ${EndIf}
  ${EndIf}

  ; Up to 1.1.0-rc.6 Textus' ProgID was "Markdown", a name other editors
  ; share. Removed only while it still opens this copy of Textus.
  ReadRegStr $R0 HKCU "Software\Classes\Markdown\shell\open\command" ""
  ${If} $R0 == '$INSTDIR\Textus.exe "%1"'
    DeleteRegKey HKCU "Software\Classes\Markdown"
  ${EndIf}

  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'
!macroend
