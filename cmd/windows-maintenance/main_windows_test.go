package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestUpdateWaitTimesOutWithoutTerminatingProcess(t *testing.T) {
	if err := waitProcess(uint32(os.Getpid()), 20*time.Millisecond); err == nil {
		t.Fatal("wait accepted an app that is still running")
	}
	for _, pid := range []string{"0", "-1", "invalid", "4294967296"} {
		if err := run([]string{"wait", pid}); err == nil {
			t.Fatalf("invalid PID accepted: %s", pid)
		}
	}
}

func TestReplacementPreservesLockedFile(t *testing.T) {
	dir := t.TempDir()
	destination, source := filepath.Join(dir, appExecutable), filepath.Join(dir, "new.exe")
	if err := os.WriteFile(destination, []byte("old application"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new application"), 0600); err != nil {
		t.Fatal(err)
	}
	ptr, _ := windows.UTF16PtrFromString(destination)
	h, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if h != 0 {
			windows.CloseHandle(h)
		}
	})
	if err = checkWritable(destination); err == nil {
		t.Fatal("lock not detected")
	}
	if err = replaceFile(source, destination); err == nil {
		t.Fatal("locked file replaced")
	}
	b, err := os.ReadFile(destination)
	if err != nil || string(b) != "old application" {
		t.Fatalf("old file changed: %q %v", b, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".nx-install-*"))
	if len(leftovers) != 0 {
		t.Fatal("temporary replacement file left behind")
	}
	windows.CloseHandle(h)
	h = 0
	if err = replaceFile(source, destination); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(destination)
	if err != nil || string(b) != "new application" {
		t.Fatalf("replacement failed: %q %v", b, err)
	}
}

func TestMissingAndReadOnlyFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), appExecutable)
	if err := checkWritable(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("preserve me"), 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	if err := checkWritable(path); err == nil {
		t.Fatal("read-only file accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve me" {
		t.Fatal("probe changed file")
	}
}

func TestCacheDirectoryLinkDoesNotRemoveTargetContents(t *testing.T) {
	// Windows directory symlinks/junctions must not cause profile cleanup to
	// traverse elsewhere. os.RemoveAll removes the link, preserving its target.
	dir := t.TempDir()
	target := filepath.Join(dir, "user files")
	link := filepath.Join(dir, "profile")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(target, "database.sqlite")
	if err := os.WriteFile(file, []byte("database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "database" {
		t.Fatalf("target contents changed: %v", err)
	}
}
