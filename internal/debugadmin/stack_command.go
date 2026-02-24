package debugadmin

import (
	"context"
	"os/exec"
	"strconv"
)

func BuildStackCommand(ctx context.Context, pid int) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		"netcoredbg",
		"--interpreter=cli",
		"--attach",
		strconv.Itoa(pid),
	)
}
