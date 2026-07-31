package debugadmin

import (
	"context"
	"os/exec"
	"strconv"
)

// BuildThreadDumpCommand builds a gdb invocation that attaches to a running
// process, dumps every thread's backtrace via "thread apply all bt", and
// then exits (gdb -batch runs its -ex commands non-interactively and quits).
func BuildThreadDumpCommand(ctx context.Context, pid int) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		"gdb",
		"-p", strconv.Itoa(pid),
		"-batch",
		"-ex", "thread apply all bt",
	)
}
