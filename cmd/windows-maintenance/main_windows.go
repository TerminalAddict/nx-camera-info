// The Windows installer embeds this helper in its temporary plugin directory.
// It closes only the executable at the selected installation path. It never
// force-kills applications or shared WebView2 processes.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const appExecutable = "nx-camera-info-gui.exe"

var installedFiles = []string{appExecutable, "THIRD_PARTY_NOTICES.txt", "GUI_THIRD_PARTY_NOTICES.txt"}
var user32 = windows.NewLazySystemDLL("user32.dll")
var enumWindows = user32.NewProc("EnumWindows")
var postMessage = user32.NewProc("PostMessageW")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stdout, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 || (args[0] != "install" && args[0] != "uninstall") {
		return fmt.Errorf("expected install or uninstall and an installation directory")
	}
	dir, err := filepath.Abs(args[1])
	if err != nil || filepath.Dir(dir) == dir {
		return fmt.Errorf("invalid installation directory")
	}
	if err := prepare(dir); err != nil {
		return err
	}
	switch args[0] {
	case "install":
		if len(args) != 3 {
			return fmt.Errorf("missing installer payload")
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for _, name := range installedFiles {
			if err := replaceFile(filepath.Join(args[2], name), filepath.Join(dir, name)); err != nil {
				return fmt.Errorf("could not install %s: %w", name, err)
			}
		}
	case "uninstall":
		roaming, err := windows.KnownFolderPath(windows.FOLDERID_RoamingAppData, 0)
		if err != nil {
			return err
		}
		// Wails v3 defaults to this executable-specific roaming profile. The
		// adjacent SDK-default profile is also removed if an older build made it.
		profiles := []string{filepath.Join(roaming, appExecutable), filepath.Join(dir, appExecutable+".WebView2")}
		for _, path := range profiles {
			// WebView2 can take a moment to release its profile after WM_CLOSE.
			if err := retry(10*time.Second, func() error { return os.RemoveAll(path) }); err != nil {
				return fmt.Errorf("could not remove the app's WebView2 data at %s: %w", path, err)
			}
		}
		for _, name := range installedFiles {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("could not remove %s: %w", name, err)
			}
		}
	}
	return nil
}

// prepare is deliberately non-destructive: a failed close or locked executable
// leaves the old installation, uninstaller and registry entry available to retry.
func prepare(dir string) error {
	if err := closeApplication(filepath.Join(dir, appExecutable), 10*time.Second); err != nil {
		return err
	}
	for _, name := range append(append([]string{}, installedFiles...), "Uninstall.exe") {
		path := filepath.Join(dir, name)
		if err := retry(2*time.Second, func() error { return checkWritable(path) }); err != nil {
			return fmt.Errorf("%s is still in use or cannot be changed. Close NX Camera Info (including any background instance in Task Manager), then retry. Details: %w", path, err)
		}
	}
	return nil
}

func retry(timeout time.Duration, action func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := action()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func checkWritable(path string) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(ptr, windows.GENERIC_WRITE|windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	return windows.CloseHandle(h)
}

// Same-directory replacement avoids truncating the previous executable if a
// lock or antivirus scanner appears between the preflight and the actual write.
func replaceFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(destination), ".nx-install-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(out.Name())
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func closeApplication(path string, timeout time.Duration) error {
	target, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = windows.Process32First(snapshot, &entry)
	var processes []windows.Handle
	defer func() {
		for _, h := range processes {
			windows.CloseHandle(h)
		}
	}()
	pids := make(map[uint32]bool)
	for err == nil {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), appExecutable) {
			h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, entry.ProcessID)
			if openErr == nil {
				var buffer [32768]uint16
				length := uint32(len(buffer))
				if windows.QueryFullProcessImageName(h, 0, &buffer[0], &length) == nil {
					image, statErr := os.Stat(windows.UTF16ToString(buffer[:length]))
					if statErr == nil && os.SameFile(target, image) {
						pids[entry.ProcessID] = true
						processes = append(processes, h)
						h = 0
					}
				}
				if h != 0 {
					windows.CloseHandle(h)
				}
			}
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	if len(processes) == 0 {
		return nil
	}
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		_, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid)
		if err == nil && pids[pid] {
			// Check the held process handle too, preventing PID reuse from
			// targeting a different program after the original app exits.
			for _, h := range processes {
				processID, _ := windows.GetProcessId(h)
				state, waitErr := windows.WaitForSingleObject(h, 0)
				if processID == pid && waitErr == nil && state == uint32(windows.WAIT_TIMEOUT) {
					postMessage.Call(hwnd, 0x0010 /* WM_CLOSE */, 0, 0)
					break
				}
			}
		}
		return 1
	})
	if ok, _, callErr := enumWindows.Call(callback, 0); ok == 0 {
		return callErr
	}
	deadline := time.Now().Add(timeout)
	for _, h := range processes {
		remaining := max(0, time.Until(deadline).Milliseconds())
		state, err := windows.WaitForSingleObject(h, uint32(remaining))
		if err != nil {
			return err
		}
		if state != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("NX Camera Info is still running. Close it in Task Manager, then retry; no app files have been changed")
		}
	}
	return nil
}
