package updates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, b []byte) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}
}

var installer = []byte("synthetic installer; never executed")

func fixture(tag string) map[string]any {
	hash := sha256.Sum256(installer)
	assets := []map[string]any{}
	for _, platform := range []string{"linux", "windows", "macos"} {
		for _, arch := range []string{"amd64", "arm64"} {
			for _, ext := range map[string][]string{"linux": {".deb", ".rpm", ".tar.gz"}, "windows": {"-setup.exe"}, "macos": {".pkg", ".dmg"}}[platform] {
				name := fmt.Sprintf("nx-camera-info-gui-%s-%s-%s%s", strings.TrimPrefix(tag, "v"), platform, arch, ext)
				assets = append(assets, map[string]any{"name": name, "browser_download_url": ProjectURL + "/releases/download/" + tag + "/" + name, "digest": "sha256:" + hex.EncodeToString(hash[:]), "size": len(installer)})
			}
		}
	}
	return map[string]any{"tag_name": tag, "assets": assets}
}

func newFixtureManager(r map[string]any, platform, arch, preferred string) *Manager {
	return New("0.3.0", platform, arch, preferred, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || req.Header.Get("Authorization") != "" || req.Body != nil {
			panic("unexpected data in update request")
		}
		if req.URL.String() == latestURL {
			b, _ := json.Marshal(r)
			return response(200, b), nil
		}
		return response(200, installer), nil
	}))
}

func TestAllPlatformsSelectTheirOwnInstallers(t *testing.T) {
	for _, platform := range []string{"linux", "windows", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(platform+"/"+arch, func(t *testing.T) {
				m := newFixtureManager(fixture("v0.10.0"), platform, arch, ".rpm")
				s := m.Check()
				count := map[string]int{"linux": 3, "windows": 1, "darwin": 2}[platform]
				if s.Status != "available" || len(s.Downloads) != count {
					t.Fatalf("%+v", s)
				}
				for _, d := range s.Downloads {
					if !strings.Contains(d.Name, "-"+arch) {
						t.Fatal(d.Name)
					}
				}
				if platform == "linux" && !strings.HasSuffix(s.Downloads[0].Name, ".rpm") {
					t.Fatal("wrong preferred installer")
				}
			})
		}
	}
}

func TestVersionOrderingAndStableReleases(t *testing.T) {
	for tag, want := range map[string]string{"v0.3.0": "current", "v0.2.9": "current", "v0.10.0": "available", "v1.0.0": "available", "0.3.1": "available", "v0.03.1": "error", "v9.0.0-beta.1": "error", "dev": "error", "v1.2.3/../bad": "error"} {
		if s := newFixtureManager(fixture(tag), "linux", "amd64", "").Check(); s.Status != want {
			t.Errorf("%s: %+v", tag, s)
		}
	}
	for _, flag := range []string{"draft", "prerelease"} {
		r := fixture("v0.4.0")
		r[flag] = true
		if s := newFixtureManager(r, "linux", "amd64", "").Check(); s.Status != "error" {
			t.Fatalf("accepted %s", flag)
		}
	}
}

func TestUnsafeOrMissingAssetsNeverOffered(t *testing.T) {
	for _, change := range []string{"external", "http", "digest", "size", "missing", "wrongarch"} {
		t.Run(change, func(t *testing.T) {
			r := fixture("v0.4.0")
			for _, a := range r["assets"].([]map[string]any) {
				switch change {
				case "external":
					a["browser_download_url"] = "https://example.com/install.exe"
				case "http":
					a["browser_download_url"] = strings.Replace(a["browser_download_url"].(string), "https:", "http:", 1)
				case "digest":
					a["digest"] = "sha256:bad"
				case "size":
					a["size"] = -1
				case "missing":
					a["digest"] = nil
				case "wrongarch":
					a["name"] = "nx-camera-info-gui-0.4.0-linux-386.deb"
				}
			}
			s := newFixtureManager(r, "linux", "amd64", "").Check()
			if s.Status != "available" || len(s.Downloads) != 0 || s.ReleaseURL == "" {
				t.Fatalf("%+v", s)
			}
		})
	}
}

func TestDownloadVerifiedAndPreservesExistingFile(t *testing.T) {
	m := newFixtureManager(fixture("v0.4.0"), "linux", "amd64", "")
	name := m.Check().Downloads[0].Name
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	choose := func(string) (string, error) { return path, nil }
	s := m.Download(name, choose)
	if s.Status != "ready" || s.SavedPath != path || s.Received != int64(len(installer)) {
		t.Fatalf("%+v", s)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, installer) {
		t.Fatalf("bad saved file: %s %v", b, err)
	}
	s = m.Download(name, choose)
	if s.Status != "available" || !strings.Contains(s.Error, "already exists") {
		t.Fatalf("%+v", s)
	}
	b, _ = os.ReadFile(path)
	if !bytes.Equal(b, installer) {
		t.Fatal("overwrote existing file")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("partial file left behind")
	}
	called := false
	s = m.Download("https://example.com/evil.exe", func(string) (string, error) { called = true; return "", nil })
	if called || s.Error == "" {
		t.Fatal("unrecognised download reached dialog")
	}
}

func TestFailedDownloadsCleanUpAndCanRetry(t *testing.T) {
	for _, failure := range []string{"checksum", "truncated", "oversized", "http", "cancel-dialog", "dialog-error", "cancel-network", "destination-race"} {
		t.Run(failure, func(t *testing.T) {
			m := newFixtureManager(fixture("v0.4.0"), "windows", "arm64", "")
			name := m.Check().Downloads[0].Name
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			m.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch failure {
				case "checksum":
					return response(200, bytes.Repeat([]byte("x"), len(installer))), nil
				case "truncated":
					return response(200, installer[:3]), nil
				case "oversized":
					return response(200, append(append([]byte{}, installer...), 0)), nil
				case "http":
					return response(500, nil), nil
				case "cancel-network":
					m.Cancel()
					return nil, context.Canceled
				case "destination-race":
					if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return response(200, installer), nil
			})
			s := m.Download(name, func(string) (string, error) {
				if failure == "cancel-dialog" {
					return "", nil
				}
				if failure == "dialog-error" {
					return "", fmt.Errorf("dialog unavailable")
				}
				return path, nil
			})
			if s.Status != "available" || s.SavedPath != "" {
				t.Fatalf("%+v", s)
			}
			files, _ := os.ReadDir(dir)
			if failure == "destination-race" {
				b, _ := os.ReadFile(path)
				if string(b) != "existing" {
					t.Fatal("destination overwritten")
				}
			} else if len(files) != 0 {
				t.Fatal("partial file left behind")
			}
			if failure == "destination-race" {
				os.Remove(path)
			}
			m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, installer), nil })
			if s = m.Download(name, func(string) (string, error) { return path, nil }); s.Status != "ready" {
				t.Fatalf("retry: %+v", s)
			}
		})
	}
}

func TestFailuresClearStaleOffer(t *testing.T) {
	for _, code := range []int{403, 404, 429, 500, 200} {
		m := newFixtureManager(fixture("v0.4.0"), "linux", "arm64", "")
		m.Check()
		m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return response(code, []byte("invalid json")), nil })
		if s := m.Check(); s.Status != "error" || s.Error == "" || len(s.Downloads) != 0 {
			t.Fatalf("%+v", s)
		}
	}
}

func TestConcurrentActionsAndCancellation(t *testing.T) {
	m := newFixtureManager(fixture("v0.4.0"), "darwin", "arm64", "")
	name := m.Check().Downloads[0].Name
	started := make(chan struct{})
	done := make(chan State, 1)
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	go func() {
		done <- m.Download(name, func(string) (string, error) { return filepath.Join(t.TempDir(), name), nil })
	}()
	<-started
	if s := m.Check(); s.Status != "downloading" {
		t.Fatalf("check interrupted download: %+v", s)
	}
	if s := m.Download(name, func(string) (string, error) { t.Error("second save dialog opened"); return "", nil }); s.Status != "downloading" {
		t.Fatalf("%+v", s)
	}
	m.Cancel()
	select {
	case s := <-done:
		if s.Status != "available" || !strings.Contains(s.Message, "cancelled") {
			t.Fatalf("%+v", s)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation hung")
	}
}

func TestRedirectPolicy(t *testing.T) {
	for address, allowed := range map[string]bool{"https://release-assets.githubusercontent.com/file": true, "https://objects.githubusercontent.com/file": true, "https://github.com/path": true, "http://github.com/path": false, "https://github.com.evil.test/path": false, "https://user@github.com/path": false, "https://github.com:444/path": false, "https://example.com/file": false} {
		u, _ := url.Parse(address)
		err := trustedRedirect(&http.Request{URL: u}, nil)
		if (err == nil) != allowed {
			t.Errorf("%s: %v", address, err)
		}
	}
}
