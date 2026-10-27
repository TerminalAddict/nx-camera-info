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
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend/*
var guiAssets embed.FS

var guiConfigure = func(*DesktopService, *application.App, *application.WebviewWindow) {}
var guiAfterRun = func() {}
var guiExtraServices []application.Service

type DesktopService struct {
	session    cameraSession
	chooseOpen func() (string, error)
	chooseSave func(string) (string, error)
}

func (d *DesktopService) Info() map[string]string {
	return map[string]string{"version": version, "platform": runtime.GOOS, "architecture": runtime.GOARCH}
}

func (d *DesktopService) OpenDatabase() (*CameraView, error) {
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
	return d.session.refresh(options)
}
func (d *DesktopService) CloseDatabase() { d.session.close() }

type ExportResult struct {
	Filename string `json:"filename"`
	Count    int    `json:"count"`
}

func (d *DesktopService) Export(format string, options ViewOptions) (*ExportResult, error) {
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
	service := &DesktopService{}
	services := append([]application.Service{application.NewService(service)}, guiExtraServices...)
	app := application.New(application.Options{
		Name: "NX Camera Info", Description: "Camera inventory and saved credentials • " + version,
		Services: services,
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:      application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "NX Camera Info", Width: 1240, Height: 800, MinWidth: 860, MinHeight: 580,
		URL: "/", UseApplicationMenu: true,
		Windows: application.WindowsWindow{Theme: application.SystemDefault},
		Linux:   application.LinuxWindow{MenuStyle: application.LinuxMenuStylePrimaryMenu},
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
	menu := app.Menu.New()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
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
		window.ExecJS("document.getElementById('filter').focus()")
	})
	viewMenu.Add("Refresh Database").SetAccelerator("CmdOrCtrl+r").OnClick(func(*application.Context) {
		window.ExecJS("document.getElementById('refresh').click()")
	})
	menu.AddRole(application.WindowMenu)
	helpMenu := menu.AddSubmenu("Help")
	helpMenu.Add("About NX Camera Info").OnClick(func(*application.Context) { app.Menu.ShowAbout() })
	app.Menu.Set(menu)
	guiConfigure(service, app, window)
	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
	guiAfterRun()
}
