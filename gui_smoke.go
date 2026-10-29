//go:build gui && guismoke

package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	"nx-camera-info/internal/nxconnect"
	"nx-camera-info/internal/nxfixture"
	"nx-camera-info/internal/updates"
)

// Test bridge and dialog substitutes are compiled only into CI test binaries.
//
//go:embed scripts/gui-smoke.js
var smokeScript string

type GUIHarness struct {
	mu              sync.Mutex
	app             *application.App
	dir             string
	mode            string
	passed          bool
	updateRequests  int
	openedLink      string
	shownFile       string
	service         *DesktopService
	installLaunches int
	stagedInstaller string
	nx              *nxfixture.Fixture
	nxServer        *httptest.Server
}

func (h *GUIHarness) HoldActivity(hold bool) {
	if hold {
		h.service.activity.RLock()
	} else {
		h.service.activity.RUnlock()
	}
}

func (h *GUIHarness) Select(mode string)         { h.mu.Lock(); defer h.mu.Unlock(); h.mode = mode }
func (h *GUIHarness) ConnectionMode(mode string) { h.nx.Mode(mode) }
func (h *GUIHarness) ConnectionEvidence() map[string]any {
	h.service.session.mu.Lock()
	defer h.service.session.mu.Unlock()
	return map[string]any{"calls": h.nx.Calls(), "backupBytes": len(h.service.session.backup)}
}
func (h *GUIHarness) selected() string { h.mu.Lock(); defer h.mu.Unlock(); return h.mode }
func (h *GUIHarness) UpdateEvidence() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, _ := os.ReadFile(filepath.Join(h.dir, "update-installer.test"))
	return map[string]any{"requests": h.updateRequests, "openedLink": h.openedLink, "shownFile": h.shownFile, "download": string(b), "installLaunches": h.installLaunches}
}
func (h *GUIHarness) ReadExport(format string) (string, error) {
	b, e := os.ReadFile(filepath.Join(h.dir, "export."+format))
	return string(b), e
}
func (h *GUIHarness) Capture(name string) error {
	if name != "inventory" && name != "export" && name != "filtered" && name != "about" && name != "update" && name != "install-update" && name != "users" && name != "servers" && name != "storage" && name != "licenses" && name != "open-database" && name != "nearby" && name != "cloud-sites" && name != "certificate" {
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
		h.service = service
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
		data, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		h.nx = nxfixture.New(data)
		h.nxServer = httptest.NewTLSServer(h.nx)
		// Synthetic cloud/server endpoints are routed only to this in-process
		// fixture. This transport is compiled exclusively into guismoke builds.
		fixtureTransport := http.DefaultTransport.(*http.Transport).Clone()
		fixtureTransport.Proxy = nil
		fixtureTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // test fixture only; local pinning still runs
		fixtureTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, h.nxServer.Listener.Addr().String())
		}
		service.connections.client = &nxconnect.Client{Transport: fixtureTransport}
		service.connections.discover = func(ctx context.Context) ([]nxconnect.Server, error) {
			return []nxconnect.Server{{Name: "Demo server", SystemName: "Demo campus", Address: "https://192.0.2.10:7001", Version: "6.1.2.42921", CloudSystemID: nxfixture.SiteID}}, nil
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
		service.launchUpdate = func(path string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.installLaunches++
			if h.mode == "update-install-fail" {
				return fmt.Errorf("synthetic installer launch failure")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "synthetic update installer; never executed" || path == filepath.Join(h.dir, "update-installer.test") {
				return fmt.Errorf("installer handoff did not stage verified bytes: %v", err)
			}
			h.stagedInstaller = path
			return nil // Never execute a synthetic installer.
		}
		service.quit = func() {
			h.mu.Lock()
			path, launches := h.stagedInstaller, h.installLaunches
			h.mu.Unlock()
			problem := ""
			if path == "" || launches != 2 {
				problem = "unexpected installer launch/quit sequence"
			}
			if path != "" {
				if _, err := os.Stat(path); err != nil {
					problem = "staged installer removed before quit"
				}
				os.RemoveAll(filepath.Dir(path)) // This fixture is never executed.
			}
			h.Done(problem)
		}
		window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { window.ExecJS(smokeScript) })
		go func() { time.Sleep(120 * time.Second); fmt.Fprintln(os.Stderr, "GUI smoke test timed out"); os.Exit(1) }()
	}
	guiAfterRun = func() {
		if h.nxServer != nil {
			h.nxServer.Close()
		}
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
