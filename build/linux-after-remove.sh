#!/bin/sh
# May VPN — when the package is removed (not upgraded), take the helper service out too.
case "$1" in
  remove|purge)
    if [ -x /usr/local/lib/mayvpn/mayvpnd ]; then /usr/local/lib/mayvpn/mayvpnd uninstall || true; fi ;;
esac
exit 0
