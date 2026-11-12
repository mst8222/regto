//go:build windows

package trace

import "syscall"

// processAlive 报告 pid 对应的进程是否仍存在（Windows：OpenProcess 试探）。
//
// 注意：探测失败时**宁可判为存活**（返回 true），因为误判「已死」会去抢别人正持有的锁，
// 造成两条记录抢同一个 seq —— 那比多等一会儿严重得多。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		// 权限不足通常意味着进程存在（只是不让我们查）；只有明确"不存在"才判死。
		if err == syscall.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	return true
}
