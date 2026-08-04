package debugadmin

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func BuildStartupCommand() (*exec.Cmd, error) {
	program, args, err := resolveStartupProgram()
	if err != nil {
		return nil, err
	}
	if GlobalOptions.WithGDB {
		return exec.Command("gdb", append([]string{"--args", program}, args...)...), nil
	}
	if GlobalOptions.WithCoverage {
		return exec.Command("dotnet-coverage", buildCoverageArgs(program, args)...), nil
	}
	return exec.Command(program, args...), nil
}

// buildCoverageArgs 构造 dotnet-coverage 的命令行参数：
// collect --session-id ${name} --output /tmp/${name}.coverage ${program} ${args...}
func buildCoverageArgs(program string, args []string) []string {
	name := GlobalOptions.CoverageName
	coverageArgs := []string{
		"collect",
		"--session-id", name,
		"--output", fmt.Sprintf("/tmp/%s.coverage", name),
		program,
	}
	return append(coverageArgs, args...)
}

// resolveStartupProgram 根据 GlobalOptions.StartupParams 解析出实际要执行的程序名和参数，
// 不涉及 gdb 包装，供 BuildStartupCommand 和 BuildGDBStartupCommand 共用。
func resolveStartupProgram() (string, []string, error) {
	parts := GlobalOptions.StartupParams
	if len(parts) == 0 {
		return "", nil, errors.New("invalid startup command")
	}
	program := parts[0]
	args := parts[1:]
	if strings.HasSuffix(strings.ToLower(parts[0]), ".dll") {
		program = "dotnet"
		args = parts
	}
	return program, args, nil
}
