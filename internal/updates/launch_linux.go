package updates

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Paths and package arguments are passed as argv, never interpolated into shell
// source. Keep progress/errors visible after the app has exited, including when
// the user declines sudo or the package manager cannot complete the upgrade.
const linuxInstallScript = `#!/bin/sh
pid=$1
ready=$2
shift 2
umask 077
: > "$ready" || exit 1
remaining=30
while kill -0 "$pid" 2>/dev/null; do
  if [ "$remaining" -eq 0 ]; then
    echo 'NX Camera Info did not close. No installation was started.'
    printf 'Press Enter to close this window. '
    read -r answer
    exit 1
  fi
  sleep 1
  remaining=$((remaining - 1))
done
echo 'Installing NX Camera Info. Follow any authentication and package manager prompts.'
"$@"
result=$?
if [ "$result" -eq 0 ]; then
  echo 'Installation completed. Reopen NX Camera Info from your application menu.'
else
  echo "Installation did not complete (exit $result). Review the messages above and retry the saved installer."
fi
printf 'Press Enter to close this window. '
read -r answer
exit "$result"
`

func LaunchInstaller(path string) error {
	command, err := linuxInstallCommand(path, exec.LookPath)
	if err != nil {
		return err
	}
	terminal, args, err := linuxTerminal(exec.LookPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	script, ready := filepath.Join(dir, "install-update.sh"), filepath.Join(dir, "terminal-ready")
	if err := os.WriteFile(script, []byte(linuxInstallScript), 0600); err != nil {
		return err
	}
	args = append(args, "/bin/sh", script, strconv.Itoa(os.Getpid()), ready)
	args = append(args, command...)
	cmd := exec.Command(terminal, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open the installation terminal: %w", err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	// Some terminals delegate to an existing desktop process. A successful
	// launcher exit alone does not establish that the install script started.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			return nil
		}
		select {
		case err := <-finished:
			if err != nil {
				return fmt.Errorf("the installation terminal could not start: %w", err)
			}
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the installation terminal did not become ready; NX Camera Info will stay open")
}

func linuxTerminal(lookPath func(string) (string, error)) (string, []string, error) {
	for _, candidate := range []struct {
		name string
		args []string
	}{
		// Our packages include xterm, avoiding desktop-specific server startup
		// behaviour when handing off work that must outlive this process.
		{"xterm", []string{"-T", "NX Camera Info update", "-e"}},
		{"x-terminal-emulator", []string{"-e"}},
		{"gnome-terminal", []string{"--"}},
		{"konsole", []string{"--separate", "-e"}},
	} {
		if path, err := lookPath(candidate.name); err == nil {
			return path, candidate.args, nil
		}
	}
	return "", nil, fmt.Errorf("no supported terminal is installed; install xterm or use Show downloaded installer to install manually")
}

func linuxInstallCommand(path string, lookPath func(string) (string, error)) ([]string, error) {
	switch {
	case strings.HasSuffix(path, ".deb"), strings.HasSuffix(path, ".rpm"):
		name := "apt-get"
		if strings.HasSuffix(path, ".rpm") {
			name = "dnf"
		}
		manager, err := lookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%s is not installed; choose the package format for your distribution", name)
		}
		sudo, err := lookPath("sudo")
		if err != nil {
			return nil, fmt.Errorf("sudo is not installed; use Show downloaded installer to install manually")
		}
		return []string{sudo, "--", manager, "install", "--", path}, nil
	case strings.HasSuffix(path, ".tar.gz"):
		if _, err := lookPath("sudo"); err != nil {
			return nil, fmt.Errorf("sudo is not installed; use Show downloaded installer to install manually")
		}
		dir := filepath.Join(filepath.Dir(path), "archive")
		if err := extractLinuxArchive(path, dir); err != nil {
			return nil, err
		}
		return []string{"/bin/sh", filepath.Join(dir, "install.sh")}, nil
	}
	return nil, fmt.Errorf("choose a Linux DEB, RPM or tar.gz package")
}

func extractLinuxArchive(path, dir string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var total int64
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if !filepath.IsLocal(name) || (h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg) {
			return fmt.Errorf("archive contains an unsafe path or unsupported entry")
		}
		if h.Size < 0 || h.Size > maxInstallerSize-total {
			return fmt.Errorf("unpacked update is too large")
		}
		total += h.Size
		destination := filepath.Join(dir, name)
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(destination, 0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if h.Mode&0111 != 0 {
			mode = 0700
		}
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, reader)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, name := range []string{"install.sh", "install-dependencies.sh", "nx-camera-info-gui"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("update archive is missing %s", name)
		}
	}
	return nil
}
