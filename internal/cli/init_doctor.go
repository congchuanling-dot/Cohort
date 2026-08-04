package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cohort/internal/app"
	"cohort/internal/llm"
	"cohort/internal/mcp"
	"cohort/internal/session"
)

type initOptions struct {
	ConfigPath    string
	ActiveProfile string
	Force         bool
}

type doctorOptions struct {
	Connect bool
}

type doctorSummary struct {
	failures int
	warnings int
}

func runInitCommand(global globalOptions, args []string, out io.Writer) error {
	opts, err := parseInitArgs(args)
	if err != nil {
		return err
	}
	path := opts.ConfigPath
	if path == "" {
		path = global.ConfigPath
	}
	if path == "" {
		path = strings.TrimSpace(os.Getenv(app.EnvConfigPath))
	}
	if path == "" {
		path, err = app.UserConfigPath()
		if err != nil {
			return err
		}
	}
	if err := app.WriteDefaultConfig(app.InitConfigOptions{
		Path:          path,
		ActiveProfile: opts.ActiveProfile,
		Force:         opts.Force,
	}); err != nil {
		return err
	}

	active := app.ProfileDeepSeek
	if cfg, err := app.LoadConfig(path); err == nil {
		active = cfg.LLM.Active().ID
	}
	fmt.Fprintf(out, "initialized config: %s\n", filepath.Clean(path))
	fmt.Fprintf(out, "active_profile: %s\n", active)
	fmt.Fprintln(out, "next:")
	fmt.Fprintf(out, "  export %s=\"sk-...\"\n", envForProfile(active))
	fmt.Fprintf(out, "  cohort --config %q config\n", filepath.Clean(path))
	return nil
}

func parseInitArgs(args []string) (initOptions, error) {
	var opts initOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--force":
			opts.Force = true
		case arg == "--config" || arg == "-c":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a config file path", arg)
			}
			i++
			opts.ConfigPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			opts.ConfigPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--provider" || arg == "--profile":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires one of: deepseek, local, claude", arg)
			}
			i++
			opts.ActiveProfile = args[i]
		case strings.HasPrefix(arg, "--provider="):
			opts.ActiveProfile = strings.TrimPrefix(arg, "--provider=")
		case strings.HasPrefix(arg, "--profile="):
			opts.ActiveProfile = strings.TrimPrefix(arg, "--profile=")
		default:
			return opts, fmt.Errorf("unknown init option %q", arg)
		}
	}
	return opts, nil
}

func runDoctorCommand(ctx context.Context, args []string, configPath string, cfg app.Config, loadErr error, out io.Writer) error {
	if len(args) > 0 && args[0] == "computer" {
		return runComputerDoctorCommand(ctx, args[1:], cfg, loadErr, out)
	}
	opts, err := parseDoctorArgs(args)
	if err != nil {
		return err
	}
	summary := &doctorSummary{}
	fmt.Fprintln(out, "doctor:")

	if loadErr != nil {
		summary.fail(out, "config.parse", loadErr.Error())
	} else if fileExists(configPath) {
		summary.pass(out, "config.file", filepath.Clean(configPath))
	} else {
		summary.warn(out, "config.file", fmt.Sprintf("%s not found; using built-in defaults", filepath.Clean(configPath)))
	}
	if loadErr == nil {
		active := cfg.LLM.Active()
		profileName := active.ID
		if profileName == "" {
			profileName = active.Name
		}
		summary.pass(out, "llm.profile", fmt.Sprintf("%s (%s / %s)", profileName, active.Provider, active.Model))

		if active.APIKey == "" {
			summary.fail(out, "llm.api_key", "missing; set env var or active config api_key")
		} else {
			summary.pass(out, "llm.api_key", "set")
		}
		if err := validateProvider(active); err != nil {
			summary.fail(out, "llm.provider", err.Error())
		} else {
			summary.pass(out, "llm.provider", active.Provider)
		}
		if err := validateAPIBase(active); err != nil {
			summary.fail(out, "llm.api_base", err.Error())
		} else {
			summary.pass(out, "llm.api_base", active.APIBase)
		}
		checkWritableDir(out, summary, "workspace", cfg.Workspace)
		checkWritableDir(out, summary, "log_dir", cfg.LogDir)
		checkWritableDir(out, summary, "session_dir", session.DefaultRootDir)
		projectRoot, rootErr := os.Getwd()
		if rootErr != nil {
			summary.warn(out, "project.root", rootErr.Error())
		} else {
			summary.pass(out, "project.root", filepath.Clean(projectRoot))
			checkDoctorMCP(ctx, out, summary, projectRoot, opts.Connect)
			checkDoctorSkills(out, summary, projectRoot)
		}
		checkDoctorBrowser(out, summary)
		checkDoctorRuntimeHelpers(out, summary, cfg.Workspace)
		if opts.Connect {
			if err := checkAPIBaseReachable(ctx, active); err != nil {
				summary.fail(out, "llm.connect", err.Error())
			} else {
				summary.pass(out, "llm.connect", "api_base reachable")
			}
		} else {
			summary.warn(out, "llm.connect", "skipped; pass --connect to check api_base reachability")
		}
	}

	if summary.failures > 0 {
		return fmt.Errorf("doctor found %d failure(s)", summary.failures)
	}
	fmt.Fprintf(out, "doctor: ok (%d warning(s))\n", summary.warnings)
	return nil
}

func checkDoctorMCP(ctx context.Context, out io.Writer, summary *doctorSummary, projectRoot string, connect bool) {
	store := mcp.NewStore(projectRoot)
	scoped, err := store.LoadEffectiveWithScopes()
	if err != nil {
		summary.fail(out, "mcp.config", err.Error())
		return
	}
	if _, err := store.LoadPermissions(); err != nil {
		summary.fail(out, "mcp.permissions", err.Error())
		return
	}
	summary.pass(out, "mcp.permissions", "readable")
	if len(scoped) == 0 {
		summary.warn(out, "mcp.servers", "none configured")
		return
	}
	summary.pass(out, "mcp.servers", fmt.Sprintf("%d configured", len(scoped)))
	if !connect {
		summary.warn(out, "mcp.connect", "skipped; pass --connect to probe configured servers")
		return
	}
	servers := make([]mcp.ServerConfig, 0, len(scoped))
	for _, entry := range scoped {
		servers = append(servers, entry.Server)
	}
	probeCtx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	manager := mcp.NewManager()
	manager.Load(probeCtx, servers)
	defer manager.Close()
	available := 0
	unavailable := 0
	for _, status := range manager.Statuses() {
		if status.Available {
			available++
		} else {
			unavailable++
			summary.warn(out, "mcp.server."+status.Name, firstNonEmpty(status.Error, "unavailable"))
		}
	}
	summary.pass(out, "mcp.connect", fmt.Sprintf("%d available, %d unavailable", available, unavailable))
}

func checkDoctorSkills(out io.Writer, summary *doctorSummary, projectRoot string) {
	store, err := app.LoadSkillStore(projectRoot)
	if err != nil {
		summary.fail(out, "skill.index", err.Error())
		return
	}
	skills := store.Skills()
	if len(skills) == 0 {
		summary.warn(out, "skill.index", "no skills discovered")
		return
	}
	errors := 0
	warnings := 0
	for _, item := range skills {
		result, err := store.Doctor(item.ID)
		if err != nil {
			errors++
			summary.fail(out, "skill."+item.ID, err.Error())
			continue
		}
		errors += result.ErrorCount()
		warnings += result.WarningCount()
	}
	if errors > 0 {
		summary.fail(out, "skill.doctor", fmt.Sprintf("%d skill(s), %d error(s), %d warning(s)", len(skills), errors, warnings))
		return
	}
	if warnings > 0 {
		summary.warn(out, "skill.doctor", fmt.Sprintf("%d skill(s), %d warning(s)", len(skills), warnings))
		return
	}
	summary.pass(out, "skill.doctor", fmt.Sprintf("%d skill(s) healthy", len(skills)))
}

func checkDoctorBrowser(out io.Writer, summary *doctorSummary) {
	dir, err := resolveBrowserExtensionDir()
	if err != nil {
		summary.warn(out, "browser.extension", err.Error())
		return
	}
	summary.pass(out, "browser.extension", filepath.Clean(dir))
}

func checkDoctorRuntimeHelpers(out io.Writer, summary *doctorSummary, workspace string) {
	desktopPath := app.ResolveRuntimeScriptPath(workspace, app.DesktopDarwinHelperPath)
	if fileExists(desktopPath) {
		summary.pass(out, "desktop.helper", filepath.Clean(desktopPath))
	} else {
		summary.warn(out, "desktop.helper", fmt.Sprintf("%s not found; run `cohort doctor computer` for full diagnostics", filepath.Clean(desktopPath)))
	}
	ocrPath := app.ResolveRuntimeScriptPath(workspace, app.BrowserOCRHelperPath)
	if fileExists(ocrPath) {
		summary.pass(out, "ocr.helper", filepath.Clean(ocrPath))
	} else {
		summary.warn(out, "ocr.helper", fmt.Sprintf("%s not found; run `cohort doctor computer` for full diagnostics", filepath.Clean(ocrPath)))
	}
}

func parseDoctorArgs(args []string) (doctorOptions, error) {
	var opts doctorOptions
	for _, arg := range args {
		switch arg {
		case "--connect":
			opts.Connect = true
		default:
			return opts, fmt.Errorf("unknown doctor option %q", arg)
		}
	}
	return opts, nil
}

func (s *doctorSummary) pass(out io.Writer, name string, detail string) {
	fmt.Fprintf(out, "  [pass] %s: %s\n", name, detail)
}

func (s *doctorSummary) warn(out io.Writer, name string, detail string) {
	s.warnings++
	fmt.Fprintf(out, "  [warn] %s: %s\n", name, detail)
}

func (s *doctorSummary) fail(out io.Writer, name string, detail string) {
	s.failures++
	fmt.Fprintf(out, "  [fail] %s: %s\n", name, detail)
}

func validateProvider(profile app.LLMProfile) error {
	_, err := llm.NewClient(llm.ProviderConfig{
		ProfileID: profile.ID,
		Provider:  profile.Provider,
		Name:      profile.Name,
		APIKey:    firstNonEmpty(profile.APIKey, "doctor-placeholder"),
		APIBase:   profile.APIBase,
		Model:     profile.Model,
		Stream:    profile.Stream,
	})
	return err
}

func validateAPIBase(profile app.LLMProfile) error {
	base := strings.TrimSpace(profile.APIBase)
	if base == "" && strings.EqualFold(profile.Provider, "anthropic") {
		base = "https://api.anthropic.com"
	}
	if base == "" {
		return fmt.Errorf("missing api_base")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("api_base must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("api_base host is empty")
	}
	return nil
}

func checkWritableDir(out io.Writer, summary *doctorSummary, name string, dir string) {
	if strings.TrimSpace(dir) == "" {
		summary.fail(out, name, "path is empty")
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		summary.fail(out, name, err.Error())
		return
	}
	temp, err := os.CreateTemp(dir, ".cohort-doctor-*")
	if err != nil {
		summary.fail(out, name, err.Error())
		return
	}
	path := temp.Name()
	if err := temp.Close(); err != nil {
		summary.fail(out, name, err.Error())
		return
	}
	_ = os.Remove(path)
	summary.pass(out, name, filepath.Clean(dir))
}

func checkAPIBaseReachable(ctx context.Context, profile app.LLMProfile) error {
	base := strings.TrimSpace(profile.APIBase)
	if base == "" && strings.EqualFold(profile.Provider, "anthropic") {
		base = "https://api.anthropic.com"
	}
	if base == "" {
		return fmt.Errorf("missing api_base")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func envForProfile(profile string) string {
	switch profile {
	case app.ProfileLocal:
		return "LOCAL_OPENAI_API_KEY"
	case app.ProfileClaude:
		return "ANTHROPIC_API_KEY"
	default:
		return "DEEPSEEK_API_KEY"
	}
}
