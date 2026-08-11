package app

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UpdateActiveProfile atomically changes only llm.active_profile while preserving
// the rest of the user-authored configuration file.
func UpdateActiveProfile(path string, profileID string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	profileID = strings.TrimSpace(profileID)
	if path == "." || profileID == "" {
		return fmt.Errorf("config path and profile id are required")
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	if _, exists := cfg.LLM.Profiles[profileID]; !exists {
		return fmt.Errorf("LLM profile %q not found", profileID)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	section := ""
	replaced := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 && strings.HasSuffix(trimmed, ":") {
			section = strings.TrimSuffix(trimmed, ":")
		}
		if section == "llm" && indent == 2 && strings.HasPrefix(trimmed, "active_profile:") {
			line = "  active_profile: " + quoteConfigValue(profileID)
			replaced = true
		}
		output.WriteString(line)
		output.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !replaced {
		return fmt.Errorf("config %s does not contain llm.active_profile", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(output.Bytes()); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
