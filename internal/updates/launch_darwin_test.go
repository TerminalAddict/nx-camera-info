package updates

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestMacNativeInstallerHandoff(t *testing.T) {
	path := os.Getenv("NX_TEST_NATIVE_PKG")
	if os.Getenv("CI") != "true" || path == "" {
		t.Skip("native Installer check runs only in a disposable packaging runner")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// This runner has just installed the PKG through the command-line installer.
	// Here, open the real guided installer and close its preview without installing.
	t.Cleanup(func() { exec.Command("/usr/bin/killall", "Installer").Run() })
	if err := LaunchInstaller(path); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("/usr/bin/pgrep", "-x", "Installer").Run(); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("native Installer process did not open")
}
