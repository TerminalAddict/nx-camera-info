//go:build gui && guismoke

package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"nx-camera-info/internal/demodb"
	"nx-camera-info/internal/updates"
)

// Test bridge and dialog substitutes are compiled only into CI test binaries.
//
//go:embed scripts/gui-smoke.js
var smokeScript string

type GUIHarness struct {
	mu             sync.Mutex
	app            *application.App
	dir            string
	mode           string
	passed         bool
	updateRequests int
	openedLink     string
	shownFile      string
}

func (h *GUIHarness) Select(mode string) { h.mu.Lock(); defer h.mu.Unlock(); h.mode = mode }
func (h *GUIHarness) selected() string   { h.mu.Lock(); defer h.mu.Unlock(); return h.mode }
func (h *GUIHarness) UpdateEvidence() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, _ := os.ReadFile(filepath.Join(h.dir, "update-installer.test"))
	return map[string]any{"requests": h.updateRequests, "openedLink": h.openedLink, "shownFile": h.shownFile, "download": string(b)}
}
func (h *GUIHarness) ReadExport(format string) (string, error) {
	b, e := os.ReadFile(filepath.Join(h.dir, "export."+format))
	return string(b), e
}
func (h *GUIHarness) Capture(name string) error {
	if name != "inventory" && name != "export" && name != "filtered" && name != "about" && name != "update" && name != "users" && name != "servers" && name != "storage" && name != "licenses" {
		return fmt.Errorf("invalid capture")
	}
	path := filepath.Join(h.dir, name+".png")
	var command *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		command = exec.Command("import", "-window", "NX Camera Info", path)
	case "darwin":
		command = exec.Command("screencapture", "-x", path)
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $r=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds; $b=New-Object System.Drawing.Bitmap($r.Width,$r.Height); $g=[System.Drawing.Graphics]::FromImage($b); $g.CopyFromScreen($r.Location,[System.Drawing.Point]::Empty,$r.Size); $b.Save('` + strings.ReplaceAll(path, "'", "''") + `'); $g.Dispose(); $b.Dispose()`
		command = exec.Command("powershell", "-NoProfile", "-Command", script)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("capture: %v %s", err, output)
	}
	return nil
}
func (h *GUIHarness) Done(problem string) {
	h.passed = problem == ""
	data, _ := json.MarshalIndent(map[string]any{"passed": h.passed, "error": problem, "platform": runtime.GOOS, "arch": runtime.GOARCH}, "", "  ")
	os.WriteFile(filepath.Join(h.dir, "result.json"), data, 0600)
	h.app.Quit()
}

func init() {
	h := &GUIHarness{}
	guiExtraServices = append(guiExtraServices, application.NewService(h))
	guiConfigure = func(service *DesktopService, app *application.App, window *application.WebviewWindow) {
		h.app = app
		h.dir = os.Getenv("NX_GUI_TEST_DIR")
		if h.dir == "" {
			panic("NX_GUI_TEST_DIR is required for the GUI test binary")
		}
		if err := os.MkdirAll(h.dir, 0700); err != nil {
			panic(err)
		}
		path := filepath.Join(h.dir, "Demo campus.sqlite")
		if err := demodb.Create(path); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(h.dir, "invalid.sqlite"), []byte("not a database"), 0600); err != nil {
			panic(err)
		}
		service.chooseOpen = func() (string, error) {
			switch h.selected() {
			case "cancel":
				return "", nil
			case "invalid":
				return filepath.Join(h.dir, "invalid.sqlite"), nil
			}
			return path, nil
		}
		service.chooseSave = func(format string) (string, error) {
			if h.selected() == "cancel" {
				return "", nil
			}
			return filepath.Join(h.dir, "export."+format), nil
		}
		service.updater = updates.New(version, runtime.GOOS, runtime.GOARCH, preferredInstaller(), h)
		service.chooseUpdate = func(string) (string, error) {
			if h.selected() == "cancel-update" {
				return "", nil
			}
			return filepath.Join(h.dir, "update-installer.test"), nil
		}
		service.openURL = func(address string) error { h.mu.Lock(); defer h.mu.Unlock(); h.openedLink = address; return nil }
		service.showFile = func(path string) error { h.mu.Lock(); defer h.mu.Unlock(); h.shownFile = path; return nil }
		window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { window.ExecJS(smokeScript) })
		go func() { time.Sleep(120 * time.Second); fmt.Fprintln(os.Stderr, "GUI smoke test timed out"); os.Exit(1) }()
	}
	guiAfterRun = func() {
		if !h.passed {
			os.Exit(1)
		}
	}
}

// Deterministic GitHub responses exercise the real updater and download path
// in every native WebView without depending on a future public release.
func (h *GUIHarness) RoundTrip(req *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.updateRequests++
	mode := h.mode
	h.mu.Unlock()
	if mode == "update-offline" {
		return nil, fmt.Errorf("synthetic offline connection")
	}
	body := "synthetic update installer; never executed"
	if strings.HasSuffix(req.URL.Path, "/releases/latest") {
		tag := "v99.0.0"
		if mode == "update-current" {
			tag = "v" + version
		}
		platform := runtime.GOOS
		if platform == "darwin" {
			platform = "macos"
		}
		assets := []map[string]any{}
		digest := sha256.Sum256([]byte(body))
		for _, suffix := range map[string][]string{"linux": {".deb", ".rpm", ".tar.gz"}, "windows": {"-setup.exe"}, "macos": {".pkg", ".dmg"}}[platform] {
			name := "nx-camera-info-gui-" + strings.TrimPrefix(tag, "v") + "-" + platform + "-" + runtime.GOARCH + suffix
			assets = append(assets, map[string]any{"name": name, "size": len(body), "digest": "sha256:" + hex.EncodeToString(digest[:]), "browser_download_url": updates.ProjectURL + "/releases/download/" + tag + "/" + name})
		}
		data, _ := json.Marshal(map[string]any{"tag_name": tag, "assets": assets})
		body = string(data)
	} else {
		if mode == "update-corrupt" {
			body = strings.Repeat("x", len(body))
		}
		if mode == "update-slow" {
			return &http.Response{StatusCode: 200, Body: smokeSlowBody{req.Context()}, Header: make(http.Header)}, nil
		}
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

type smokeSlowBody struct{ ctx context.Context }

func (b smokeSlowBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b smokeSlowBody) Close() error             { return nil }
