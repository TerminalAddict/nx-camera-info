package updates

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func LaunchInstaller(path string) error {
	if !strings.HasSuffix(path, "-setup.exe") {
		return fmt.Errorf("choose the Windows setup installer")
	}
	cmd := exec.Command(path, "/WAITPID="+strconv.Itoa(os.Getpid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start setup: %w", err)
	}
	return cmd.Process.Release()
}
