//go:build !windows

package trace

import (
	"errors"
	"syscall"
)

// processAlive 报告 pid 对应的进程是否仍存在（POSIX：signal 0 只做存在性检查）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	// EPERM 说明进程存在但没有权限发信号 —— 仍算存活。
	return err == nil || errors.Is(err, syscall.EPERM)
}
