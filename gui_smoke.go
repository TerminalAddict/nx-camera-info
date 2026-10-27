//go:build gui && guismoke

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"nx-camera-info/internal/demodb"
)

// Test bridge and dialog substitutes are compiled only into CI test binaries.
//
//go:embed scripts/gui-smoke.js
var smokeScript string

type GUIHarness struct {
	app    *application.App
	dir    string
	mode   string
	passed bool
}

func (h *GUIHarness) Select(mode string) { h.mode = mode }
func (h *GUIHarness) ReadExport(format string) (string, error) {
	b, e := os.ReadFile(filepath.Join(h.dir, "export."+format))
	return string(b), e
}
func (h *GUIHarness) Capture(name string) error {
	if name != "inventory" && name != "export" && name != "filtered" {
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
			switch h.mode {
			case "cancel":
				return "", nil
			case "invalid":
				return filepath.Join(h.dir, "invalid.sqlite"), nil
			}
			return path, nil
		}
		service.chooseSave = func(format string) (string, error) {
			if h.mode == "cancel" {
				return "", nil
			}
			return filepath.Join(h.dir, "export."+format), nil
		}
		window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { window.ExecJS(smokeScript) })
		go func() { time.Sleep(120 * time.Second); fmt.Fprintln(os.Stderr, "GUI smoke test timed out"); os.Exit(1) }()
	}
	guiAfterRun = func() {
		if !h.passed {
			os.Exit(1)
		}
	}
}
