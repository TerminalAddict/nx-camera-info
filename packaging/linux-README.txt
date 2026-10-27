NX Camera Info GUI

Requires a graphical desktop, glibc 2.39+, GTK 4.14+ and WebKitGTK 6.0.
Use Ubuntu 24.04+, Debian 13+, Fedora 40+ or a compatible recent distribution.

1. Run ./install-dependencies.sh if GTK/WebKit are not installed.
   This uses the distribution package manager and requires internet access.
2. Run ./nx-camera-info-gui (do not run the GUI as root).
3. Choose Open database, then select your ecs.sqlite backup.

DEB/RPM packages are preferable for desktop-menu integration and automatic
dependency resolution: sudo apt install ./PACKAGE.deb or sudo dnf install ./PACKAGE.rpm

The GUI reads databases without modifying their contents. Passwords are hidden
until Reveal passwords is selected. Exports follow the visible filter and
password setting. Existing output files are never overwritten.

Keep THIRD_PARTY_NOTICES.txt and GUI_THIRD_PARTY_NOTICES.txt with the application.
