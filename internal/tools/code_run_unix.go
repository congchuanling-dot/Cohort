//go:build !windows

package tools

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// prepareCodeRunCommand 为 Unix 命令创建独立进程组。
// 这样超时时可以杀掉 bash 以及它启动的 grep/find/sleep 等子进程。
func prepareCodeRunCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killCodeRunProcessGroup(cmd)
		return nil
	}
}

// killCodeRunProcessGroup 尽量杀掉 code_run 启动的整组进程。
// CommandContext 会负责杀主进程，这里额外杀进程组，避免子进程残留。
func killCodeRunProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, os.ErrProcessDone) && err != syscall.ESRCH {
		_ = cmd.Process.Kill()
	}
}
