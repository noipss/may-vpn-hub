; May VPN — Windows uninstaller: stop and remove the helper service and its folder too.
!macro customUnInstall
  ExecWait '"$INSTDIR\resources\daemon\mayvpnd.exe" uninstall'
!macroend
