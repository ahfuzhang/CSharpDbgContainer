package debugadmin

import (
	"errors"
	"os/exec"
	"strings"
)

func BuildStartupCommand(options *Options) (*exec.Cmd, error) {
	// trimmed := strings.TrimSpace(startup)
	// if trimmed == "" {
	// 	return nil, errors.New("empty startup command")
	// }
	parts := options.StartupParams
	if len(parts) == 0 {
		return nil, errors.New("invalid startup command")
	}
	program := parts[0]
	args := parts[1:]
	if strings.HasSuffix(strings.ToLower(parts[0]), ".dll") {
		program = "dotnet"
		args = parts
	}
	if !options.WithGDB {
		return exec.Command(program, args...), nil
	}
	return exec.Command("gdb", append([]string{"--args", program}, args...)...), nil
}
