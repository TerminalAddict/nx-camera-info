#!/usr/bin/env bash
set -euo pipefail
arch="$(go env GOARCH)"
version=0.2.1
stage="$(mktemp -d)"
cleanup() {
  hdiutil detach "$stage/mount" -quiet >/dev/null 2>&1 || true
  rm -rf "$stage"
}
trap cleanup EXIT
app="$stage/NX Camera Info.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$stage/icon.iconset" dist/installers
cp dist/nx-camera-info-gui "$app/Contents/MacOS/nx-camera-info-gui"
cp THIRD_PARTY_NOTICES.txt GUI_THIRD_PARTY_NOTICES.txt "$app/Contents/Resources/"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" packaging/icon.png --out "$stage/icon.iconset/icon_${size}x${size}.png" >/dev/null
  retina=$((size * 2))
  sips -z "$retina" "$retina" packaging/icon.png --out "$stage/icon.iconset/icon_${size}x${size}@2x.png" >/dev/null
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
pkgbuild --component "$app" --install-location /Applications --identifier com.loudas.nx-camera-info --version "$version" "$stage/app.pkg"
mkdir "$stage/resources"
cp packaging/macos-welcome.html "$stage/resources/welcome.html"
cp packaging/icon.png "$stage/resources/icon.png"
hostarch=x86_64
if [ "$arch" = arm64 ]; then hostarch=arm64; fi
cat > "$stage/Distribution.xml" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<installer-gui-script minSpecVersion="2">
  <title>NX Camera Info</title>
  <welcome file="welcome.html" mime-type="text/html"/>
  <options customize="never" require-scripts="false" hostArchitectures="$hostarch"/>
  <domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>
  <volume-check><allowed-os-versions><os-version min="12.0"/></allowed-os-versions></volume-check>
  <choices-outline><line choice="app"/></choices-outline>
  <choice id="app" visible="false" title="NX Camera Info" description="Camera database explorer">
    <pkg-ref id="com.loudas.nx-camera-info"/>
  </choice>
  <pkg-ref id="com.loudas.nx-camera-info" version="$version">app.pkg</pkg-ref>
</installer-gui-script>
EOF
productbuild --distribution "$stage/Distribution.xml" --resources "$stage/resources" --package-path "$stage" "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.pkg"
pkgutil --expand "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.pkg" "$stage/pkg-expanded"
test -s "$stage/pkg-expanded/Resources/icon.png"
test -s "$stage/pkg-expanded/Resources/welcome.html"
mkdir "$stage/dmg"
cp -R "$app" "$stage/dmg/"
ln -s /Applications "$stage/dmg/Applications"
hdiutil create -volname 'NX Camera Info' -srcfolder "$stage/dmg" -ov -format UDRW "$stage/install.dmg"
mkdir "$stage/mount"
hdiutil attach "$stage/install.dmg" -mountpoint "$stage/mount" -nobrowse -noverify -quiet
cp "$app/Contents/Resources/AppIcon.icns" "$stage/mount/.VolumeIcon.icns"
xcrun SetFile -a C "$stage/mount"
test "$(xcrun GetFileInfo -a C "$stage/mount")" = 1
hdiutil detach "$stage/mount" -quiet
hdiutil convert "$stage/install.dmg" -format UDZO -o "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.dmg"
# Validate that the installer really installs a runnable bundle on this runner.
sudo installer -pkg "dist/installers/nx-camera-info-gui-${version}-macos-${arch}.pkg" -target /
'/Applications/NX Camera Info.app/Contents/MacOS/nx-camera-info-gui' --version
