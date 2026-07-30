package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	envBrowserExtensionDir = "COHORT_BROWSER_EXTENSION_DIR"
	browserExtensionName   = "cohort_browser_bridge"
)

func runExtensionCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort extension path|open")
	}
	switch args[0] {
	case "path":
		dir, err := resolveBrowserExtensionDir()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, dir)
		return nil
	case "open":
		dir, err := resolveBrowserExtensionDir()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "extension_path: %s\n", dir)
		fmt.Fprintln(out, "chrome_url: chrome://extensions")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Next:")
		fmt.Fprintln(out, "  1. Open chrome://extensions")
		fmt.Fprintln(out, "  2. Enable Developer mode")
		fmt.Fprintln(out, "  3. Click Load unpacked")
		fmt.Fprintf(out, "  4. Select: %s\n", dir)
		return openChromeExtensionsPage()
	default:
		return fmt.Errorf("unknown extension command %q", args[0])
	}
}

func resolveBrowserExtensionDir() (string, error) {
	candidates := []string{}
	if env := os.Getenv(envBrowserExtensionDir); env != "" {
		candidates = append(candidates, env)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "assert", browserExtensionName),
			filepath.Join(wd, "extension", browserExtensionName),
		)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "..", "extension", browserExtensionName),
			filepath.Join(exeDir, "..", "extensions", browserExtensionName),
			filepath.Join(exeDir, "..", "..", "extension", browserExtensionName),
		)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".cohort", "extensions", browserExtensionName))
	}

	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		if browserExtensionExists(abs) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("Cohort browser extension not found; set %s to assert/%s or reinstall the npm package", envBrowserExtensionDir, browserExtensionName)
}

func browserExtensionExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "manifest.json"))
	return err == nil && !info.IsDir()
}

func openChromeExtensionsPage() error {
	const url = "chrome://extensions"
	var cmds [][]string
	switch runtime.GOOS {
	case "darwin":
		cmds = [][]string{
			{"open", "-a", "Google Chrome", url},
			{"open", url},
		}
	case "windows":
		cmds = [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	default:
		cmds = [][]string{{"xdg-open", url}}
	}
	var lastErr error
	for _, parts := range cmds {
		cmd := exec.Command(parts[0], parts[1:]...)
		if err := cmd.Run(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no opener command configured")
	}
	return fmt.Errorf("failed to open chrome://extensions automatically: %w", lastErr)
}
