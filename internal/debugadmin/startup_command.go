package debugadmin

import (
	"errors"
	"os/exec"
	"strings"
)

func BuildStartupCommand(startup string) (*exec.Cmd, error) {
	trimmed := strings.TrimSpace(startup)
	if trimmed == "" {
		return nil, errors.New("empty startup command")
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return nil, errors.New("invalid startup command")
	}
	if strings.HasSuffix(strings.ToLower(parts[0]), ".dll") {
		return exec.Command("dotnet", parts...), nil
	}
	return exec.Command(parts[0], parts[1:]...), nil
}
