package updates

import (
	"fmt"
	"os/exec"
	"strings"
)

func LaunchInstaller(path string) error {
	var cmd *exec.Cmd
	switch {
	case strings.HasSuffix(path, ".pkg"):
		cmd = exec.Command("/usr/bin/open", "-a", "/System/Library/CoreServices/Installer.app", path)
	case strings.HasSuffix(path, ".dmg"):
		cmd = exec.Command("/usr/bin/open", path)
	default:
		return fmt.Errorf("choose a macOS PKG installer or disk image")
	}
	// open exits once Launch Services accepts the request; it does not wait for
	// installation. Failure leaves the main app open with its downloaded file.
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not open the installer: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
