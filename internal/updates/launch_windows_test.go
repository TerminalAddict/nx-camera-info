package updates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("NX_UPDATE_LAUNCH_TEST_OUTPUT"); path != "" {
		data, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(path, data, 0600); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestWindowsLaunchPassesParentPIDWithoutShell(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, result := filepath.Join(dir, "update with spaces-setup.exe"), filepath.Join(dir, "result.json")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Windows may briefly retain a just-exited executable's handle.
		for i := 0; i < 50; i++ {
			if err := os.Remove(path); err == nil || os.IsNotExist(err) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	t.Setenv("NX_UPDATE_LAUNCH_TEST_OUTPUT", result)
	if err := LaunchInstaller(path); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(result)
		if err == nil {
			var args []string
			if json.Unmarshal(data, &args) == nil {
				if len(args) != 1 || args[0] != "/WAITPID="+strconv.Itoa(os.Getpid()) {
					t.Fatalf("wrong updater arguments: %v", args)
				}
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("detached installer did not start")
}
