package debugadmin

import (
	"bufio"
	"fmt"
	"html"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// clockTicksPerSecond is the kernel's USER_HZ, used to convert
// /proc/[pid]/stat's starttime field (in clock ticks) into wall-clock time.
// It is effectively always 100 on Linux.
const clockTicksPerSecond = 100

// ProcessInfo describes one process found under /proc, for display in the
// container process browser.
type ProcessInfo struct {
	PID         int
	Uptime      string
	Memory      string
	Cmdline     string
	ThreadCount int
	IsCurrent   bool
}

// listContainerProcesses 遍历 /proc 目录，收集容器内所有进程的启动时间、
// 物理内存占用（RSS）和命令行参数，按 PID 排序返回。
func listContainerProcesses(currentPID int) []ProcessInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	bootTime, err := readBootTime()
	if err != nil {
		return nil
	}
	processes := make([]ProcessInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		startTime, err := readProcessStartTime(pid, bootTime)
		if err != nil {
			continue
		}
		processes = append(processes, ProcessInfo{
			PID:         pid,
			Uptime:      formatUptime(time.Since(startTime)),
			Memory:      formatBytes(readProcessRSSBytes(pid)),
			Cmdline:     html.EscapeString(readProcessCmdline(pid)),
			ThreadCount: readProcessThreadCount(pid),
			IsCurrent:   pid == currentPID,
		})
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	return processes
}

func readBootTime() (time.Time, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if seconds, ok := strings.CutPrefix(line, "btime "); ok {
			value, err := strconv.ParseInt(strings.TrimSpace(seconds), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(value, 0), nil
		}
	}
	return time.Time{}, fmt.Errorf("btime not found in /proc/stat")
}

// readProcessStartTime 解析 /proc/[pid]/stat 的第 22 个字段（starttime，
// 单位为系统启动以来的时钟滴答数），换算成实际时间。
// comm 字段可能包含空格和括号，因此从最后一个 ')' 之后开始按空白切分。
func readProcessStartTime(pid int, bootTime time.Time) (time.Time, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, err
	}
	content := string(data)
	lastParen := strings.LastIndex(content, ")")
	if lastParen < 0 {
		return time.Time{}, fmt.Errorf("invalid stat content for pid %d", pid)
	}
	fields := strings.Fields(content[lastParen+1:])
	const starttimeFieldIndex = 19 // overall field 22, minus pid+comm already consumed
	if len(fields) <= starttimeFieldIndex {
		return time.Time{}, fmt.Errorf("stat fields too short for pid %d", pid)
	}
	ticks, err := strconv.ParseUint(fields[starttimeFieldIndex], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	offset := time.Duration(float64(ticks) / float64(clockTicksPerSecond) * float64(time.Second))
	return bootTime.Add(offset), nil
}

func readProcessRSSBytes(pid int) uint64 {
	file, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "VmRSS:" {
			if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return kb * 1024
			}
			break
		}
	}
	return 0
}

// readProcessThreadCount 统计 /proc/[pid]/task 目录下的条目数，
// 每个条目对应一个物理线程。
func readProcessThreadCount(pid int) int {
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		return 0
	}
	return len(entries)
}

func readProcessCmdline(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return ""
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return strings.Join(parts, " ")
}

// formatUptime renders a duration as "Xh Ym Zs", e.g. "2h 15m 30s".
func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int64(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

func formatBytes(size uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case size >= gb:
		return fmt.Sprintf("%.2f GB", float64(size)/gb)
	case size >= mb:
		return fmt.Sprintf("%.2f MB", float64(size)/mb)
	case size >= kb:
		return fmt.Sprintf("%.2f KB", float64(size)/kb)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
