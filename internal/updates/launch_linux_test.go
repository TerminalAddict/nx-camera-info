package updates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLinuxInstallScriptWaitsAndPreservesArguments(t *testing.T) {
	dir := t.TempDir()
	script, ready, result := filepath.Join(dir, "update.sh"), filepath.Join(dir, "ready"), filepath.Join(dir, "result")
	manager := filepath.Join(dir, "fake manager.sh")
	if err := os.WriteFile(script, []byte(linuxInstallScript), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$NX_TEST_RESULT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command("sleep", "30")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { parent.Process.Kill(); parent.Wait() })
	filename := filepath.Join(dir, "package $(touch should-not-exist) ' with spaces.deb")
	cmd := exec.Command("/bin/sh", script, strconv.Itoa(parent.Process.Pid), ready, manager, "install", "--", filename)
	cmd.Env = append(os.Environ(), "NX_TEST_RESULT="+result)
	cmd.Stdin = strings.NewReader("\n")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal acknowledgement missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(result); !os.IsNotExist(err) {
		t.Fatal("installation started before app exited")
	}
	parent.Process.Kill()
	parent.Wait()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err, output.String())
	}
	data, err := os.ReadFile(result)
	if err != nil || string(data) != "install\n--\n"+filename+"\n" {
		t.Fatalf("argument corruption: %q %v", data, err)
	}
	if !strings.Contains(output.String(), "Installation completed") {
		t.Fatal(output.String())
	}
}

func TestLinuxPackageCommandsAndMissingTools(t *testing.T) {
	lookup := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	for _, extension := range []string{"deb", "rpm"} {
		path := "/tmp/package with spaces." + extension
		args, err := linuxInstallCommand(path, lookup)
		if err != nil || len(args) != 6 || args[0] != "/usr/bin/sudo" || args[5] != path {
			t.Fatal(args, err)
		}
	}
	missing := func(string) (string, error) { return "", fmt.Errorf("not installed") }
	if _, err := linuxInstallCommand("/tmp/file.deb", missing); err == nil {
		t.Fatal("missing package manager accepted")
	}
	if _, _, err := linuxTerminal(missing); err == nil {
		t.Fatal("missing terminal accepted")
	}
}

func TestLinuxArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, test := range []struct {
		name string
		kind byte
	}{
		{"../outside", tar.TypeReg}, {"/absolute", tar.TypeReg}, {"link", tar.TypeSymlink}, {"hardlink", tar.TypeLink},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "update.tar.gz")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: test.name, Typeflag: test.kind, Linkname: "/tmp/elsewhere", Mode: 0700}); err != nil {
				t.Fatal(err)
			}
			tw.Close()
			gz.Close()
			f.Close()
			if err := extractLinuxArchive(path, filepath.Join(t.TempDir(), "out")); err == nil {
				t.Fatal("unsafe archive extracted")
			}
		})
	}
}

func TestLinuxArchiveExtractsInstaller(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.sh", "install-dependencies.sh", "nx-camera-info-gui"} {
		body := "synthetic " + name
		if err := tw.WriteHeader(&tar.Header{Name: "./" + name, Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	dir := filepath.Join(t.TempDir(), "archive")
	if err := extractLinuxArchive(path, dir); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "install.sh")); err != nil || string(data) != "synthetic install.sh" {
		t.Fatal("wrong extracted installer", err)
	}
}

// Opt-in native terminal coverage under Xvfb. The terminal and detached launch
// are real; sudo and apt-get are synthetic executables and never install files.
func TestLinuxNativeTerminalHandoff(t *testing.T) {
	if os.Getenv("NX_TEST_TERMINAL_HANDOFF") != "1" {
		t.Skip("run under Xvfb with NX_TEST_TERMINAL_HANDOFF=1")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$NX_TERMINAL_RESULT\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "apt-get"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(dir, "synthetic update.deb")
	if err := os.WriteFile(packagePath, []byte("not a real package"), 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self, "-test.run=^TestLinuxNativeTerminalChild$")
	child.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "NX_TERMINAL_CHILD_DIR="+dir, "NX_TERMINAL_RESULT="+filepath.Join(dir, "result"))
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill() })
	waitFile := func(name string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		child.Process.Kill()
		child.Wait()
		t.Fatalf("native terminal did not create %s: %s", name, output.String())
	}
	waitFile("handoff")
	if _, err := os.Stat(filepath.Join(dir, "result")); !os.IsNotExist(err) {
		t.Fatal("terminal installed before app exit")
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err, output.String())
	}
	waitFile("result")
	data, err := os.ReadFile(filepath.Join(dir, "result"))
	if err != nil || !strings.HasSuffix(string(data), "install\n--\n"+packagePath+"\n") {
		t.Fatalf("incorrect native handoff: %q %v", data, err)
	}
}

func TestLinuxNativeTerminalChild(t *testing.T) {
	dir := os.Getenv("NX_TERMINAL_CHILD_DIR")
	if dir == "" {
		t.Skip("child process only")
	}
	if err := LaunchInstaller(filepath.Join(dir, "synthetic update.deb")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "handoff"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "exit")); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("parent did not release the updater")
}
