package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	pluginpkg "cohort/internal/plugin"
)

func runPluginCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: cohort plugin list|show|doctor ...")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: cohort plugin list")
		}
		plugins, err := pluginpkg.Discover(root)
		if err != nil {
			return err
		}
		if len(plugins) == 0 {
			fmt.Fprintln(out, "no plugins found")
			fmt.Fprintln(out, "expected: .cohort/plugins/<plugin>/plugin.json")
			return nil
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tVERSION\tSKILLS\tCOMMANDS\tMCP\tPATH")
		for _, item := range plugins {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\n",
				item.Manifest.Name,
				item.Manifest.Version,
				len(item.Manifest.Skills),
				len(item.Manifest.Commands),
				len(item.Manifest.MCP.Servers)+boolToInt(item.Manifest.MCP.Config != ""),
				item.Path,
			)
		}
		return tw.Flush()
	case "show":
		if len(args) != 2 {
			return errors.New("usage: cohort plugin show <name|manifest_path>")
		}
		item, err := findPlugin(root, args[1])
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(item.Manifest, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	case "doctor":
		target := ""
		if len(args) > 2 {
			return errors.New("usage: cohort plugin doctor [name|manifest_path]")
		}
		if len(args) == 2 {
			target = args[1]
		}
		return doctorPlugins(root, target, out)
	default:
		return fmt.Errorf("unknown plugin command %q, use list, show, or doctor", args[0])
	}
}

func doctorPlugins(root string, target string, out io.Writer) error {
	plugins, err := discoverTargetPlugins(root, target)
	if err != nil {
		return err
	}
	if len(plugins) == 0 {
		fmt.Fprintln(out, "no plugins found")
		return nil
	}
	errorCount := 0
	for _, item := range plugins {
		result := pluginpkg.Doctor(item)
		fmt.Fprintf(out, "plugin: %s\n", result.Plugin.Manifest.Name)
		for _, check := range result.Checks {
			fmt.Fprintf(out, "  [%s] %s - %s\n", check.Status, check.Name, check.Message)
			if check.Status == "error" {
				errorCount++
			}
		}
	}
	if errorCount > 0 {
		return fmt.Errorf("plugin doctor found %d error(s)", errorCount)
	}
	return nil
}

func discoverTargetPlugins(root string, target string) ([]pluginpkg.Plugin, error) {
	if strings.TrimSpace(target) == "" {
		return pluginpkg.Discover(root)
	}
	item, err := findPlugin(root, target)
	if err != nil {
		return nil, err
	}
	return []pluginpkg.Plugin{item}, nil
}

func findPlugin(root string, target string) (pluginpkg.Plugin, error) {
	if strings.Contains(target, string(os.PathSeparator)) || strings.HasSuffix(target, ".json") || strings.HasSuffix(target, ".yaml") || strings.HasSuffix(target, ".yml") {
		return pluginpkg.Load(target)
	}
	plugins, err := pluginpkg.Discover(root)
	if err != nil {
		return pluginpkg.Plugin{}, err
	}
	for _, item := range plugins {
		if item.Manifest.Name == target {
			return item, nil
		}
	}
	return pluginpkg.Plugin{}, fmt.Errorf("plugin %q not found", target)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
