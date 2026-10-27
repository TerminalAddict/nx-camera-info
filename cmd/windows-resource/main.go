package main

import (
	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: windows-resource amd64|arm64")
	}
	arch := winres.ArchAMD64
	if os.Args[1] == "arm64" {
		arch = winres.ArchARM64
	} else if os.Args[1] != "amd64" {
		log.Fatal("unsupported architecture")
	}
	f, err := os.Open("packaging/icon.ico")
	if err != nil {
		log.Fatal(err)
	}
	icon, err := winres.LoadICO(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	rs := winres.ResourceSet{}
	// Wails uses numeric resource ID 3 for window, taskbar, and About icons.
	if err = rs.SetIcon(winres.ID(3), icon); err != nil {
		log.Fatal(err)
	}
	rs.SetManifest(winres.AppManifest{ExecutionLevel: winres.AsInvoker, DPIAwareness: winres.DPIPerMonitorV2, UseCommonControlsV6: true, LongPathAware: true})
	vi := version.Info{FileVersion: [4]uint16{0, 4, 0, 0}, ProductVersion: [4]uint16{0, 4, 0, 0}}
	vi.Set(0, version.ProductName, "NX Camera Info")
	vi.Set(0, version.FileDescription, "NX Camera Info desktop database explorer")
	vi.Set(0, version.ProductVersion, "0.4.0")
	rs.SetVersionInfo(vi)
	out, err := os.Create("rsrc_windows_" + os.Args[1] + ".syso")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	if err = rs.WriteObject(out, arch); err != nil {
		log.Fatal(err)
	}
}
