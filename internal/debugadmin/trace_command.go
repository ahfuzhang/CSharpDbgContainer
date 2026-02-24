package debugadmin

import (
	"fmt"
	"os/exec"
	"strconv"
)

func TraceProfileCandidates() []string {
	return []string{
		"dotnet-sampled-thread-time",
		"cpu-sampling",
	}
}

func BuildTraceCommand(pid int, seconds int, outputBase string, profile string) *exec.Cmd {
	duration := fmt.Sprintf("00:00:00:%02d", seconds)
	args := []string{
		"collect",
		"--duration",
		duration,
		"--format",
		"Speedscope",
		"-p",
		strconv.Itoa(pid),
		"-o",
		outputBase,
	}
	if profile != "" {
		args = append([]string{"collect", "--profile", profile}, args[1:]...)
	}
	return exec.Command("dotnet-trace", args...)
}
