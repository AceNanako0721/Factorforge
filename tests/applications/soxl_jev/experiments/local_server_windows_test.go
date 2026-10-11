//go:build windows

package experiments_test

import (
	"os/exec"
	"syscall"
)

func configureLocalServerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
