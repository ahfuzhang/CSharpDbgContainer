package debugadmin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const gdbLogTimeLayout = "20060102-150405"

// BuildGDBStartupCommand creates the gdb invocation after all debugger commands
// have been persisted to a script. GDB reads the script before it starts the target.
func BuildGDBStartupCommand(options *Options, scriptPath string) (*exec.Cmd, error) {
	targetOptions := *options
	targetOptions.WithGDB = false
	targetCmd, err := BuildStartupCommand(&targetOptions)
	if err != nil {
		return nil, err
	}
	args := append([]string{"-q", "-x", scriptPath, "--args"}, targetCmd.Args...)
	return exec.Command("gdb", args...), nil
}

// WriteGDBCommandScript writes the complete non-interactive debugging sequence.
// The timestamp is generated here rather than by a shell so it can be recorded in run history.
// 启动 gdb 命令行
func WriteGDBCommandScript(now time.Time) (scriptPath, logPath string, err error) {
	logPath = filepath.Join(os.TempDir(), now.Format(gdbLogTimeLayout)+".log")
	// 把调试命令写到一个 *.gdb 文件
	script, err := os.CreateTemp(os.TempDir(), "debugadmin-gdb-*.gdb")
	if err != nil {
		return "", "", fmt.Errorf("create gdb command script: %w", err)
	}
	scriptPath = script.Name()
	if err := script.Chmod(0o600); err != nil {
		_ = script.Close()
		_ = os.Remove(scriptPath)
		return "", "", fmt.Errorf("set gdb command script permissions: %w", err)
	}
	// 写入多行 gdb 调试命令到 *.gdb 文件中
	if _, err := script.WriteString(gdbCommandScript(logPath)); err != nil {
		_ = script.Close()
		_ = os.Remove(scriptPath)
		return "", "", fmt.Errorf("write gdb command script: %w", err)
	}
	if err := script.Close(); err != nil {
		_ = os.Remove(scriptPath)
		return "", "", fmt.Errorf("close gdb command script: %w", err)
	}
	return scriptPath, logPath, nil
}

// 一系列的 gdb 调试命令
func gdbCommandScript(logPath string) string {
	return fmt.Sprintf(`set pagination off
set confirm off
set print pretty on
set print thread-events off
set print frame-arguments all
set disable-randomization off

set logging overwrite on
set logging file %s
set logging enabled on

handle SIGSEGV stop print pass
handle SIGABRT stop print pass
handle SIGBUS  stop print pass
handle SIGILL  stop print pass
handle SIGFPE  stop print pass

run

echo \n===== Program status =====\n
info program
echo \n===== Crashed thread =====\n
thread
echo \n===== Registers =====\n
info registers
echo \n===== Crash backtrace =====\n
bt 10
set logging off
continue
quit 128
`, logPath)
}
