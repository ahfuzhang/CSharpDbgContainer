package debugadmin

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// bindCPUsPattern 校验 -bind.cpus 的格式：逗号分隔的核心编号或者用 "-" 表示的区间，
// 例如 "2-4" 或 "0,2,4-6"，与 taskset -c 接受的 cpu-list 语法一致。
var bindCPUsPattern = regexp.MustCompile(`^\d+(-\d+)?(,\d+(-\d+)?)*$`)

// validateBindCPUs 校验 -bind.cpus 参数的格式，返回去除首尾空白后的值。
func validateBindCPUs(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !bindCPUsPattern.MatchString(raw) {
		return "", fmt.Errorf("-bind.cpus %q is not a valid cpu list, expected e.g. \"2-4\" or \"0,2,4-6\"", raw)
	}
	return raw, nil
}

const (
	bindCPUsPollInterval = 200 * time.Millisecond
	bindCPUsWaitTimeout  = 30 * time.Second
)

// bindTargetCPUs 在目标进程启动后，把它绑定到 -bind.cpus 指定的 CPU 核心上：
//
//	taskset -acp ${bind.cpus} ${pid}
//
// 以 --with.gdb 或 --with.coverage 启动时，target.PID() 返回的是 gdb / dotnet-coverage
// 这个外壳进程的 pid，真正要绑核的是它派生出来的目标进程，必须通过 waitForRealTargetPID
// 在进程树中定位，避免把 gdb 或 dotnet-coverage 自身绑核。
func bindTargetCPUs(target *TargetProcess, opts *Options) {
	pid, ok := waitForRealTargetPID(target, opts, bindCPUsWaitTimeout)
	if !ok {
		_, _ = fmt.Fprintf(os.Stderr, "bind.cpus: could not locate target process descended from pid=%d within %s, skip binding\n", target.PID(), bindCPUsWaitTimeout)
		return
	}
	cmd := exec.Command("taskset", "-acp", opts.BindCPUs, strconv.Itoa(pid))
	output, err := cmd.CombinedOutput()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bind.cpus: taskset -acp %s %d failed: %v\n%s\n", opts.BindCPUs, pid, err, output)
		return
	}
	_, _ = fmt.Fprintf(os.Stdout, "bind.cpus: bound pid=%d to cpus=%s\n", pid, opts.BindCPUs)
}

// waitForRealTargetPID 等待真正的目标进程出现并返回其 pid。
// 不使用 gdb / dotnet-coverage 时，target.PID() 本身就是目标进程，直接返回；
// 否则轮询进程树查找子孙进程，直到找到、目标进程退出，或者等待超时。
func waitForRealTargetPID(target *TargetProcess, opts *Options, timeout time.Duration) (int, bool) {
	rootPID := target.PID()
	if !opts.WithGDB && !opts.WithCoverage {
		return rootPID, true
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(bindCPUsPollInterval)
	defer ticker.Stop()
	for {
		if resolved, ok := findTargetDescendantPID(rootPID, opts.StartupParams); ok {
			return resolved, true
		}
		select {
		case <-target.Done():
			return 0, false
		case now := <-ticker.C:
			if now.After(deadline) {
				return 0, false
			}
		}
	}
}
