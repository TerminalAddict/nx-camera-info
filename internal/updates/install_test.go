package updates

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readyInstaller(t *testing.T, platform string) (*Manager, string) {
	t.Helper()
	m := newFixtureManager(fixture("v0.5.0"), platform, "amd64", "")
	m.cacheDir = t.TempDir()
	name := m.Check().Downloads[0].Name
	path := filepath.Join(t.TempDir(), "chosen filename without an extension")
	s := m.Download(name, func(string) (string, error) { return path, nil })
	if s.Status != "ready" || s.InstallLabel != "Install and close" {
		t.Fatalf("download: %+v", s)
	}
	return m, path
}

func TestInstallStagesVerifiedBytesAndKeepsRunningInstaller(t *testing.T) {
	for _, platform := range []string{"windows", "linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			m, original := readyInstaller(t, platform)
			var launched string
			s := m.Install(func(path string) error {
				launched = path
				if path == original || filepath.Base(path) != m.ready.Name || !strings.HasPrefix(path, m.cacheDir) {
					t.Fatal("installer did not use a private, canonical staging path")
				}
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, installer) {
					t.Fatal("staged bytes differ", err)
				}
				// Changes to the user's download after verification cannot replace
				// the staged installer that is actually handed to the OS.
				if err := os.WriteFile(original, []byte("changed original"), 0600); err != nil {
					t.Fatal(err)
				}
				if m.Check().Status != "installing" {
					t.Fatal("check interrupted install")
				}
				return nil
			})
			if s.Status != "installing" || s.Error != "" {
				t.Fatalf("handoff: %+v", s)
			}
			data, err := os.ReadFile(launched)
			if err != nil || !bytes.Equal(data, installer) {
				t.Fatal("running installer was changed or deleted", err)
			}
			m.Install(func(string) error { t.Fatal("duplicate install started"); return nil })
		})
	}
}

func TestInstallRejectsChangedDownloads(t *testing.T) {
	for _, change := range []string{"content", "size", "missing", "directory", "symlink"} {
		t.Run(change, func(t *testing.T) {
			m, path := readyInstaller(t, "windows")
			switch change {
			case "content":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(installer)), 0600); err != nil {
					t.Fatal(err)
				}
			case "size":
				if err := os.WriteFile(path, []byte("short"), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if change == "symlink" {
					target := filepath.Join(t.TempDir(), "target")
					if err := os.WriteFile(target, installer, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, path); err != nil {
						t.Skip(err)
					}
				}
			}
			s := m.Install(func(string) error { t.Fatal("unverified installer launched"); return nil })
			if s.Status != "ready" || s.Error == "" {
				t.Fatalf("failure must keep the app open: %+v", s)
			}
			entries, err := os.ReadDir(m.cacheDir)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed staging left files", err)
			}
		})
	}
}

func TestInstallLaunchFailureCanRetry(t *testing.T) {
	m, path := readyInstaller(t, "windows")
	s := m.Install(func(string) error { return fmt.Errorf("synthetic launch failure") })
	if s.Status != "ready" || !strings.Contains(s.Error, "synthetic launch failure") {
		t.Fatalf("launch failure: %+v", s)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, installer) {
		t.Fatal("user download was removed or changed", err)
	}
	entries, _ := os.ReadDir(m.cacheDir)
	if len(entries) != 0 {
		t.Fatal("failed launch left staged files")
	}
	if s = m.Install(func(string) error { return nil }); s.Status != "installing" || s.Error != "" {
		t.Fatalf("retry: %+v", s)
	}
}

func TestInstallRequiresCurrentVerifiedOffer(t *testing.T) {
	m := newFixtureManager(fixture("v0.5.0"), "windows", "amd64", "")
	never := func(string) error { t.Fatal("install without a ready download"); return nil }
	if m.Install(never).Error == "" {
		t.Fatal("idle install accepted")
	}
	m, _ = readyInstaller(t, "windows")
	m.Check()
	if m.Install(never).Error == "" {
		t.Fatal("old download accepted after new check")
	}
}
