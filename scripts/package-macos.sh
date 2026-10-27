#!/usr/bin/env bash
set -euo pipefail
arch="$(go env GOARCH)"
version=0.2.0
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
app="$stage/NX Camera Info.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$stage/icon.iconset" dist/installers
cp dist/nx-camera-info-gui "$app/Contents/MacOS/nx-camera-info-gui"
cp THIRD_PARTY_NOTICES.txt GUI_THIRD_PARTY_NOTICES.txt "$app/Contents/Resources/"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" packaging/icon.png --out "$stage/icon.iconset/icon_${size}x${size}.png" >/dev/null
done
iconutil -c icns "$stage/icon.iconset" -o "$app/Contents/Resources/AppIcon.icns"
cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.loudas.nx-camera-info</string>
<key>CFBundleName</key><string>NX Camera Info</string>
<key>CFBundleDisplayName</key><string>NX Camera Info</string>
<key>CFBundleExecutable</key><string>nx-camera-info-gui</string>
<key>CFBundleVersion</key><string>$version</string>
<key>CFBundleShortVersionString</key><string>$version</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>NSHighResolutionCapable</key><true/>
<key>LSMinimumSystemVersion</key><string>12.0</string>
</dict></plist>
EOF
codesign --force --sign - "$app"
codesign --verify --deep --strict "$app"
pkgbuild --component "$app" --install-location /Applications --identifier com.loudas.nx-camera-info --version "$version" "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.pkg"
mkdir "$stage/dmg"
cp -R "$app" "$stage/dmg/"
ln -s /Applications "$stage/dmg/Applications"
hdiutil create -volname 'NX Camera Info' -srcfolder "$stage/dmg" -ov -format UDZO "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.dmg"
# Validate that the installer really installs a runnable bundle on this runner.
sudo installer -pkg "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.pkg" -target /
'/Applications/NX Camera Info.app/Contents/MacOS/nx-camera-info-gui' --version
