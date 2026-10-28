#!/bin/sh
set -eu
if [ "$(id -u)" -eq 0 ]; then elevate=; else elevate=sudo; fi
if command -v apt-get >/dev/null 2>&1; then
  $elevate apt-get update
  $elevate apt-get install libgtk-4-1 libwebkitgtk-6.0-4 xdg-desktop-portal xdg-desktop-portal-gtk xterm sudo
elif command -v dnf >/dev/null 2>&1; then
  $elevate dnf install gtk4 webkitgtk6.0 xdg-desktop-portal xdg-desktop-portal-gtk xterm sudo
elif command -v pacman >/dev/null 2>&1; then
  $elevate pacman -S --needed gtk4 webkitgtk-6.0 xdg-desktop-portal xdg-desktop-portal-gtk xterm sudo
else
  echo 'Install GTK 4.14+, WebKitGTK 6.0, and your desktop portal using your distribution package manager.' >&2
  exit 1
fi
