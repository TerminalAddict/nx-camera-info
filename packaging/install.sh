#!/bin/sh
set -eu
cd "$(dirname "$0")"
if [ "$(id -u)" -ne 0 ]; then exec sudo sh ./install.sh; fi
sh ./install-dependencies.sh
install -d /opt/nx-camera-info /usr/local/bin /usr/local/share/applications /usr/local/share/icons/hicolor/scalable/apps
install -m755 nx-camera-info-gui /opt/nx-camera-info/
install -m644 THIRD_PARTY_NOTICES.txt GUI_THIRD_PARTY_NOTICES.txt /opt/nx-camera-info/
ln -sfn /opt/nx-camera-info/nx-camera-info-gui /usr/local/bin/nx-camera-info-gui
install -m644 nx-camera-info.desktop /usr/local/share/applications/
for size in 16 24 32 48 64 128 256 512; do
  install -d "/usr/local/share/icons/hicolor/${size}x${size}/apps"
  install -m644 "icons/$size.png" "/usr/local/share/icons/hicolor/${size}x${size}/apps/nx-camera-info.png"
done
install -m644 icon.svg /usr/local/share/icons/hicolor/scalable/apps/nx-camera-info.svg
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/local/share/icons/hicolor || true
fi
if command -v apparmor_parser >/dev/null 2>&1 && [ -e /sys/kernel/security/apparmor/profiles ]; then
  install -m644 apparmor-profile /etc/apparmor.d/nx-camera-info-gui
  apparmor_parser -r /etc/apparmor.d/nx-camera-info-gui
fi
echo 'Installed NX Camera Info. Launch it from your desktop menu as your normal user.'
