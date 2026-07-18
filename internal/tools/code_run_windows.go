//go:build windows

package tools

import "os/exec"

// prepareCodeRunCommand 是 Windows 下的占位实现。
// Windows 当前沿用 exec.CommandContext + PowerShell 的行为，后续如需杀进程树再单独增强。
func prepareCodeRunCommand(cmd *exec.Cmd) {}

// killCodeRunProcessGroup 是 Windows 下的占位实现。
// CommandContext 会在超时时终止主进程。
func killCodeRunProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
