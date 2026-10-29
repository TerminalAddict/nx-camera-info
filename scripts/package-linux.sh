#!/usr/bin/env bash
set -euo pipefail
arch="$(go env GOARCH)"
version=0.6.0
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/usr/bin" "$stage/usr/share/applications" "$stage/usr/share/icons/hicolor/scalable/apps" "$stage/usr/share/doc/nx-camera-info-gui" dist/installers
install -m755 dist/nx-camera-info-gui "$stage/usr/bin/"
install -m644 packaging/nx-camera-info.desktop "$stage/usr/share/applications/"
for size in 16 24 32 48 64 128 256 512; do
  mkdir -p "$stage/usr/share/icons/hicolor/${size}x${size}/apps"
  install -m644 "packaging/icons/$size.png" "$stage/usr/share/icons/hicolor/${size}x${size}/apps/nx-camera-info.png"
done
install -m644 branding/icon.svg "$stage/usr/share/icons/hicolor/scalable/apps/nx-camera-info.svg"
install -m644 THIRD_PARTY_NOTICES.txt GUI_THIRD_PARTY_NOTICES.txt "$stage/usr/share/doc/nx-camera-info-gui/"
mkdir "$stage/DEBIAN"
mkdir -p "$stage/etc/apparmor.d"
install -m644 packaging/apparmor-profile "$stage/etc/apparmor.d/nx-camera-info-gui"
install -m755 packaging/deb-postinst "$stage/DEBIAN/postinst"
install -m755 packaging/deb-postrm "$stage/DEBIAN/postrm"
echo '/etc/apparmor.d/nx-camera-info-gui' > "$stage/DEBIAN/conffiles"
cat > "$stage/DEBIAN/control" <<EOF
Package: nx-camera-info-gui
Version: $version
Architecture: $arch
Maintainer: TerminalAddict <TerminalAddict@users.noreply.github.com>
Depends: libc6 (>= 2.39), libgtk-4-1 (>= 4.14), libwebkitgtk-6.0-4, xdg-desktop-portal, xdg-desktop-portal-gtk, xterm, sudo
Section: utils
Priority: optional
Homepage: https://github.com/TerminalAddict/nx-camera-info
Description: Desktop camera inventory and saved credential viewer for Nx Witness
 Browse a local ecs.sqlite database, filter cameras, reveal saved credentials,
 and export CSV or JSON without changing the database.
EOF
dpkg-deb --root-owner-group --build "$stage" "dist/installers/nx-camera-info-gui-${version}-linux-${arch}.deb"
rpmarch=x86_64
if [ "$arch" = arm64 ]; then rpmarch=aarch64; fi
mkdir -p "$stage/rpm/BUILD" "$stage/rpm/RPMS" "$stage/rpm/SOURCES" "$stage/rpm/SPECS" "$stage/rpm/SRPMS"
cat > "$stage/rpm/SPECS/nx.spec" <<EOF
Name: nx-camera-info-gui
Version: $version
Release: 1
Summary: Nx Witness camera database explorer
License: LicenseRef-Unspecified
URL: https://github.com/TerminalAddict/nx-camera-info
BuildArch: $rpmarch
AutoReqProv: no
Requires: glibc >= 2.39, gtk4 >= 4.14, webkitgtk6.0, xdg-desktop-portal, xdg-desktop-portal-gtk, xterm, sudo
%description
Read-only desktop camera inventory and saved credential recovery.
%install
mkdir -p %{buildroot}/usr
cp -a $stage/usr/. %{buildroot}/usr/
%post
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || :
fi
%postun
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || :
fi
%files
/usr/bin/nx-camera-info-gui
/usr/share/applications/nx-camera-info.desktop
/usr/share/icons/hicolor/*/apps/nx-camera-info.*
/usr/share/doc/nx-camera-info-gui/*
EOF
rpmbuild --define "_topdir $stage/rpm" --define '_build_id_links none' --define '__os_install_post %{nil}' --target "$rpmarch" -bb "$stage/rpm/SPECS/nx.spec"
cp "$stage/rpm/RPMS/$rpmarch/"*.rpm "dist/installers/nx-camera-info-gui-${version}-linux-${arch}.rpm"
mkdir "$stage/archive"
cp -R packaging/icons "$stage/archive/"
install -m644 branding/icon.svg "$stage/archive/"
install -m755 dist/nx-camera-info-gui packaging/install-dependencies.sh packaging/install.sh "$stage/archive/"
install -m644 THIRD_PARTY_NOTICES.txt GUI_THIRD_PARTY_NOTICES.txt packaging/linux-README.txt packaging/icon.png packaging/nx-camera-info.desktop packaging/apparmor-profile "$stage/archive/"
tar -czf "dist/installers/nx-camera-info-gui-${version}-linux-${arch}.tar.gz" -C "$stage/archive" .
