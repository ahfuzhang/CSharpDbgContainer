package debugadmin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// RunRecord 记录目标子进程的一次启动到退出的完整信息。
type RunRecord struct {
	PID          int
	StartTime    time.Time
	EndTime      time.Time
	ExitCode     int
	Signal       string
	Abnormal     bool
	Err          string
	LastLogs     []string
	CoreDumpPath string
	GDBLogPath   string
}

// RunHistory 是并发安全的启动记录列表，供 AdminHandler 展示。
type RunHistory struct {
	mu      sync.RWMutex
	records []RunRecord
}

func NewRunHistory() *RunHistory {
	return &RunHistory{}
}

func (h *RunHistory) Add(record RunRecord) {
	h.mu.Lock()
	h.records = append(h.records, record)
	h.mu.Unlock()
}

func (h *RunHistory) Snapshot() []RunRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]RunRecord, len(h.records))
	copy(out, h.records)
	return out
}

// classifyExit 根据 cmd.Wait() 返回的 error 判断退出码、信号，以及是否异常退出。
// abnormal 为 true 表示进程非正常结束（非 0 退出码、被信号杀死、或 Wait 本身失败）。
func classifyExit(err error) (exitCode int, signal string, abnormal bool) {
	if err == nil {
		return 0, "", false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			signal = status.Signal().String()
		}
		return exitCode, signal, true
	}
	return -1, "", true
}

// detectCoreDump 尽力查找目标进程可能产生的 core dump 文件。
// 由于 core_pattern 各系统不同，这里只检查常见路径，找到即返回绝对路径。
func detectCoreDump(pid int) string {
	candidates := []string{
		fmt.Sprintf("core.%d", pid),
		"core",
		fmt.Sprintf("/tmp/core.%d", pid),
		"/tmp/core",
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if abs, err := filepath.Abs(candidate); err == nil {
			return abs
		}
		return candidate
	}
	return ""
}
