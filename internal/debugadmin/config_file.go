package debugadmin

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

type FileConfig struct {
	AdminPort int
	Startup   string
}

func LoadConfigFile(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileConfig{}, nil
		}
		return FileConfig{}, err
	}
	return parseConfigFile(string(data)), nil
}

func parseConfigFile(content string) FileConfig {
	cfg := FileConfig{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := stripComment(scanner.Text())
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, value, ok := splitYAMLKeyValue(trimmed)
		if !ok {
			continue
		}
		switch key {
		case "port":
			port, err := strconv.Atoi(value)
			if err == nil && port >= 1 && port <= 65535 {
				cfg.AdminPort = port
			}
		case "startup":
			cfg.Startup = strings.Trim(value, `"'`)
		}
	}
	return cfg
}

func splitYAMLKeyValue(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	return key, value, true
}

func stripComment(line string) string {
	idx := strings.Index(line, "#")
	if idx < 0 {
		return line
	}
	return line[:idx]
}
