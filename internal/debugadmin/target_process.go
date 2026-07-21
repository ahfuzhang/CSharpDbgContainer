package debugadmin

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// maxRecentLogLines 是崩溃退出时保留的最后日志行数。
const maxRecentLogLines = 50

type TargetProcess struct {
	pid           int
	cmd           *exec.Cmd
	broker        *LogBroker
	history       *RunHistory
	startTime     time.Time
	gdbLogPath    string
	gdbScriptPath string
	done          chan error
	stdoutWriter  io.Writer
	stderrWriter  io.Writer
	lineWriter    io.Writer
	lineWriterMu  sync.Mutex
	lineWriteDead bool
	recentMu      sync.Mutex
	recentLines   []string
}

// StartTarget 创建被调试的子进程
func StartTarget(options *Options, broker *LogBroker, lineWriter io.Writer, logStdoutOutput bool, history *RunHistory) (*TargetProcess, error) {
	cmd, gdbLogPath, gdbScriptPath, err := buildTargetCommand(options)
	if err != nil {
		return nil, err
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		removeGDBCommandScript(gdbScriptPath)
		return nil, err
	}

	target := &TargetProcess{
		pid:           cmd.Process.Pid,
		cmd:           cmd,
		broker:        broker,
		history:       history,
		startTime:     time.Now(),
		gdbLogPath:    gdbLogPath,
		gdbScriptPath: gdbScriptPath,
		done:          make(chan error, 1),
		lineWriter:    lineWriter,
	}
	if logStdoutOutput {
		target.stdoutWriter = os.Stdout
		target.stderrWriter = os.Stderr
	}
	go target.consumeOutput(stdoutPipe, target.stdoutWriter)
	go target.consumeOutput(stderrPipe, target.stderrWriter)
	go target.waitForExit()
	return target, nil
}

func (p *TargetProcess) PID() int {
	return p.pid
}

// GDBLogPath returns the log file created for this target when it was started with gdb.
func (p *TargetProcess) GDBLogPath() string {
	return p.gdbLogPath
}

func (p *TargetProcess) Done() <-chan error {
	return p.done
}

func (p *TargetProcess) consumeOutput(reader io.ReadCloser, localWriter io.Writer) {
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text() + "\n"
		if localWriter != nil {
			_, _ = io.WriteString(localWriter, line)
		}
		p.broker.Broadcast(line)
		p.writeLineToVector(line)
		p.recordRecentLine(line)
	}
	if err := scanner.Err(); err != nil {
		message := fmt.Sprintf("[log stream error] %v\n", err)
		_, _ = os.Stdout.WriteString(message)
		p.broker.Broadcast(message)
	}
}

func (p *TargetProcess) waitForExit() {
	err := p.cmd.Wait()
	removeGDBCommandScript(p.gdbScriptPath)
	endTime := time.Now()
	message := fmt.Sprintf("[target exited] pid=%d err=%v\n", p.pid, err)
	_, _ = os.Stdout.WriteString(message)
	p.broker.Broadcast(message)
	if p.history != nil {
		exitCode, signal, abnormal := classifyExit(err)
		record := RunRecord{
			PID:        p.pid,
			StartTime:  p.startTime,
			EndTime:    endTime,
			ExitCode:   exitCode,
			Signal:     signal,
			Abnormal:   abnormal,
			LastLogs:   p.RecentLines(),
			GDBLogPath: p.gdbLogPath,
		}
		if err != nil {
			record.Err = err.Error()
		}
		if abnormal {
			record.CoreDumpPath = detectCoreDump(p.pid)
		}
		p.history.Add(record)
	}
	p.done <- err
	close(p.done)
}

func buildTargetCommand(options *Options) (*exec.Cmd, string, string, error) {
	if !options.WithGDB {
		// 没有 gdb 的情况，走原来的逻辑
		cmd, err := BuildStartupCommand(options)
		return cmd, "", "", err
	}
	// 构造 *.gdb 命令文件
	scriptPath, logPath, err := WriteGDBCommandScript(time.Now())
	if err != nil {
		return nil, "", "", err
	}
	// 构造 gdb 命令行:  gdb -q -x xx.gdb --args ${params}
	cmd, err := BuildGDBStartupCommand(options, scriptPath)
	if err != nil {
		removeGDBCommandScript(scriptPath)
		return nil, "", "", err
	}
	return cmd, logPath, scriptPath, nil
}

func removeGDBCommandScript(scriptPath string) {
	if scriptPath != "" {
		_ = os.Remove(scriptPath)
	}
}

func (p *TargetProcess) recordRecentLine(line string) {
	trimmed := strings.TrimRight(line, "\n")
	p.recentMu.Lock()
	p.recentLines = append(p.recentLines, trimmed)
	if len(p.recentLines) > maxRecentLogLines {
		p.recentLines = p.recentLines[len(p.recentLines)-maxRecentLogLines:]
	}
	p.recentMu.Unlock()
}

func (p *TargetProcess) RecentLines() []string {
	p.recentMu.Lock()
	defer p.recentMu.Unlock()
	out := make([]string, len(p.recentLines))
	copy(out, p.recentLines)
	return out
}

func (p *TargetProcess) writeLineToVector(line string) {
	p.lineWriterMu.Lock()
	defer p.lineWriterMu.Unlock()
	if p.lineWriter == nil || p.lineWriteDead {
		return
	}
	if _, err := io.WriteString(p.lineWriter, line); err != nil {
		p.lineWriteDead = true
		message := fmt.Sprintf("[vector stdin write failed] %v\n", err)
		_, _ = os.Stdout.WriteString(message)
		p.broker.Broadcast(message)
	}
}
