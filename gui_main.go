//go:build gui

package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"nx-camera-info/internal/updates"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed frontend/*
var guiAssets embed.FS

//go:embed packaging/icon.png
var desktopIcon []byte

var guiConfigure = func(*DesktopService, *application.App, *application.WebviewWindow) {}
var guiAfterRun = func() {}
var guiExtraServices []application.Service

type DesktopService struct {
	activity     sync.RWMutex
	closing      bool
	session      cameraSession
	chooseOpen   func() (string, error)
	chooseSave   func(string) (string, error)
	chooseUpdate func(string) (string, error)
	updater      *updates.Manager
	openURL      func(string) error
	showFile     func(string) error
	launchUpdate func(string) error
	quit         func()
}

func (d *DesktopService) beginActivity() error {
	d.activity.RLock()
	if d.closing {
		d.activity.RUnlock()
		return fmt.Errorf("NX Camera Info is closing for an update")
	}
	return nil
}

func (d *DesktopService) InstallUpdate() updates.State {
	if !d.activity.TryLock() {
		s := d.updater.State()
		s.Error = "Finish the current database operation or file dialog before installing."
		return s
	}
	defer d.activity.Unlock()
	if d.closing {
		return d.updater.State()
	}
	s := d.updater.Install(d.launchUpdate)
	if s.Status == "installing" {
		d.closing = true
		d.session.close()
		d.quit()
	}
	return s
}

func (d *DesktopService) Info() map[string]string {
	return map[string]string{"version": version, "platform": runtime.GOOS, "architecture": runtime.GOARCH, "goVersion": runtime.Version(), "wailsVersion": "3.0.0"}
}

func (d *DesktopService) CheckForUpdates() updates.State { return d.updater.Check() }
func (d *DesktopService) UpdateState() updates.State     { return d.updater.State() }
func (d *DesktopService) DownloadUpdate(name string) updates.State {
	return d.updater.Download(name, d.chooseUpdate)
}
func (d *DesktopService) CancelUpdateDownload() { d.updater.Cancel() }
func (d *DesktopService) ShowUpdateDownload() error {
	s := d.updater.State()
	if s.Status != "ready" || s.SavedPath == "" {
		return fmt.Errorf("download an installer first")
	}
	if _, err := os.Stat(s.SavedPath); err != nil {
		return fmt.Errorf("the downloaded installer has been moved or removed")
	}
	return d.showFile(s.SavedPath)
}
func (d *DesktopService) OpenLink(name string) error {
	switch name {
	case "author":
		return d.openURL("https://paulwillard.nz/")
	case "project":
		return d.openURL(updates.ProjectURL)
	case "release":
		return d.openURL(d.updater.ReleaseLink())
	}
	return fmt.Errorf("unknown link")
}

func preferredInstaller() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	if executable, err := os.Executable(); err == nil && strings.HasPrefix(executable, "/opt/nx-camera-info/") {
		return ".tar.gz"
	}
	if _, err := os.Stat("/etc/debian_version"); err == nil {
		return ".deb"
	}
	if _, err := os.Stat("/etc/redhat-release"); err == nil {
		return ".rpm"
	}
	return ".tar.gz"
}

func (d *DesktopService) OpenDatabase() (*CameraView, error) {
	if err := d.beginActivity(); err != nil {
		return nil, err
	}
	defer d.activity.RUnlock()
	path, err := d.chooseOpen()
	if err != nil || path == "" {
		return nil, err
	}
	view, err := d.session.open(path)
	return &view, err
}

func (d *DesktopService) View(options ViewOptions) (CameraView, error) {
	return d.session.view(options)
}
func (d *DesktopService) Refresh(options ViewOptions) (CameraView, error) {
	if err := d.beginActivity(); err != nil {
		return CameraView{}, err
	}
	defer d.activity.RUnlock()
	return d.session.refresh(options)
}
func (d *DesktopService) CloseDatabase() { d.session.close() }

type ExportResult struct {
	Filename string `json:"filename"`
	Count    int    `json:"count"`
}

func (d *DesktopService) Export(format string, options ViewOptions) (*ExportResult, error) {
	if err := d.beginActivity(); err != nil {
		return nil, err
	}
	defer d.activity.RUnlock()
	if format != "csv" && format != "json" {
		return nil, fmt.Errorf("choose CSV or JSON")
	}
	path, err := d.chooseSave(format)
	if err != nil || path == "" {
		return nil, err
	}
	if filepath.Ext(path) == "" {
		path += "." + format
	}
	count, err := d.session.export(path, format, options)
	if os.IsExist(err) {
		return nil, fmt.Errorf("that file already exists; choose a new filename to preserve the existing file")
	}
	if err != nil {
		return nil, err
	}
	return &ExportResult{Filename: filepath.Base(path), Count: count}, nil
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("NX Camera Info GUI " + version)
		return
	}
	assets, err := fs.Sub(guiAssets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	service := &DesktopService{updater: updates.New(version, runtime.GOOS, runtime.GOARCH, preferredInstaller(), nil)}
	services := append([]application.Service{application.NewService(service)}, guiExtraServices...)
	app := application.New(application.Options{
		Name: "NX Camera Info", Description: "Camera inventory and saved credentials • " + version,
		Icon:     desktopIcon,
		Services: services,
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Linux:    application.LinuxOptions{ApplicationID: "com.loudas.nx-camera-info", ProgramName: "nx-camera-info"},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "NX Camera Info", Width: 1240, Height: 800, MinWidth: 860, MinHeight: 580,
		URL: "/", UseApplicationMenu: true,
		Windows: application.WindowsWindow{Theme: application.SystemDefault},
		Linux:   application.LinuxWindow{MenuStyle: application.LinuxMenuStylePrimaryMenu},
	})
	// Leave native title bars, menus and taskbars visible on smaller desktops.
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		if screen := app.Screen.GetPrimary(); screen != nil {
			width := min(1240, max(860, screen.WorkArea.Width-40))
			height := min(800, max(580, screen.WorkArea.Height-80))
			if width < 1240 || height < 800 {
				window.SetSize(width, height)
				window.Center()
			}
		}
	})
	service.chooseOpen = func() (string, error) {
		return app.Dialog.OpenFile().AttachToWindow(window).SetTitle("Open Nx Witness database").
			AddFilter("SQLite databases", "*.sqlite;*.sqlite3;*.db").AddFilter("All files", "*").PromptForSingleSelection()
	}
	service.chooseSave = func(format string) (string, error) {
		return app.Dialog.SaveFile().AttachToWindow(window).
			SetFilename("camera-export-"+time.Now().Format("20060102-150405")+"."+format).
			AddFilter(strings.ToUpper(format)+" file", "*."+format).PromptForSingleSelection()
	}
	service.chooseUpdate = func(name string) (string, error) {
		return app.Dialog.SaveFile().AttachToWindow(window).SetFilename(name).PromptForSingleSelection()
	}
	service.openURL = app.Browser.OpenURL
	service.showFile = func(path string) error { return app.Env.OpenFileManager(path, true) }
	service.launchUpdate = updates.LaunchInstaller
	service.quit = app.Quit
	showAbout := func(*application.Context) { window.ExecJS("document.getElementById('about-open').click()") }
	checkUpdates := func(*application.Context) { window.ExecJS("document.getElementById('updates-open').click()") }
	menu := app.Menu.New()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
		// Clear the native About role so macOS invokes our custom page instead
		// of its standard panel; keep the other application-menu roles intact.
		menu.FindByRole(application.About).SetRole(application.NoRole).OnClick(showAbout)
	}
	fileMenu := menu.AddSubmenu("File")
	for _, item := range []struct{ title, key, id string }{
		{"Open Database…", "CmdOrCtrl+o", "open"}, {"Export As…", "CmdOrCtrl+e", "export"},
		{"Close Database", "CmdOrCtrl+Shift+w", "close"},
	} {
		id := item.id
		fileMenu.Add(item.title).SetAccelerator(item.key).OnClick(func(*application.Context) {
			window.ExecJS("document.getElementById('" + id + "').click()")
		})
	}
	fileMenu.AddSeparator()
	fileMenu.Add("Quit").SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) { app.Quit() })
	menu.AddRole(application.EditMenu)
	viewMenu := menu.AddSubmenu("View")
	viewMenu.Add("Filter Cameras").SetAccelerator("CmdOrCtrl+f").OnClick(func(*application.Context) {
		window.ExecJS("document.getElementById('nav-cameras').click(); document.getElementById('filter').focus()")
	})
	viewMenu.Add("Refresh Database").SetAccelerator("CmdOrCtrl+r").OnClick(func(*application.Context) {
		window.ExecJS("document.getElementById('refresh').click()")
	})
	menu.AddRole(application.WindowMenu)
	helpMenu := menu.AddSubmenu("Help")
	helpMenu.Add("About NX Camera Info").OnClick(showAbout)
	helpMenu.Add("Check for Updates…").OnClick(checkUpdates)
	app.Menu.Set(menu)
	guiConfigure(service, app, window)
	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
	guiAfterRun()
}
