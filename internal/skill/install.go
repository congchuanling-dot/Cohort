package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// InstallOptions 描述一次 Skill 安装请求。
type InstallOptions struct {
	Source      string
	Scope       Scope
	Name        string
	Force       bool
	ProjectRoot string
	HomeDir     string
}

// InstallResult 描述安装后的本地 Skill。
type InstallResult struct {
	Skill       Skill
	Source      string
	Destination string
	Replaced    bool
	Files       int
}

type installCandidate struct {
	sourceDir string
	alias     string
}

// ParseScope 解析 CLI 传入的 Skill 安装范围。
func ParseScope(value string) (Scope, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(ScopeProject):
		return ScopeProject, nil
	case string(ScopeUser), "global":
		return ScopeUser, nil
	default:
		return "", fmt.Errorf("invalid skill scope %q, want project or user", value)
	}
}

// Install 从本地目录、本地 SKILL.md 或 git URL 安装一个 Skill。
func Install(ctx context.Context, opts InstallOptions) (InstallResult, error) {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		return InstallResult{}, fmt.Errorf("skill source is required")
	}
	scope := opts.Scope
	if scope == "" {
		scope = ScopeProject
	}
	projectRoot := strings.TrimSpace(opts.ProjectRoot)
	if projectRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			projectRoot = cwd
		}
	}
	if abs, err := filepath.Abs(projectRoot); err == nil {
		projectRoot = abs
	}
	homeDir := strings.TrimSpace(opts.HomeDir)
	if homeDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			homeDir = home
		}
	}

	candidate, cleanup, err := resolveInstallCandidate(ctx, source, opts.Name)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return InstallResult{}, err
	}
	alias := sanitizeAlias(firstNonEmpty(opts.Name, candidate.alias))
	if alias == "" {
		return InstallResult{}, fmt.Errorf("skill name is required or could not be inferred")
	}
	destRoot, err := installRoot(scope, projectRoot, homeDir)
	if err != nil {
		return InstallResult{}, err
	}
	dest := filepath.Join(destRoot, alias)
	replaced := false
	if _, err := os.Stat(dest); err == nil {
		if !opts.Force {
			return InstallResult{}, fmt.Errorf("skill %s/%s already exists; use --force to replace it", scope, alias)
		}
		replaced = true
		if err := os.RemoveAll(dest); err != nil {
			return InstallResult{}, err
		}
	} else if !os.IsNotExist(err) {
		return InstallResult{}, err
	}
	files, err := copySkillDir(candidate.sourceDir, dest)
	if err != nil {
		return InstallResult{}, err
	}
	data, err := os.ReadFile(filepath.Join(dest, SkillFileName))
	if err != nil {
		return InstallResult{}, err
	}
	name, description := parseSummary(data, alias)
	item := Skill{
		ID:          string(scope) + "/" + alias,
		Alias:       alias,
		Name:        name,
		Description: description,
		Scope:       scope,
		Path:        filepath.Join(dest, SkillFileName),
	}
	return InstallResult{
		Skill:       item,
		Source:      source,
		Destination: dest,
		Replaced:    replaced,
		Files:       files,
	}, nil
}

func resolveInstallCandidate(ctx context.Context, source, requestedName string) (installCandidate, func(), error) {
	if info, err := os.Stat(source); err == nil {
		candidate, err := localInstallCandidate(source, info, requestedName)
		return candidate, nil, err
	}
	if !looksLikeGitSource(source) {
		return installCandidate{}, nil, fmt.Errorf("skill source %q is not a local path and does not look like a git URL", source)
	}
	tempDir, err := os.MkdirTemp("", "cohert-skill-install-*")
	if err != nil {
		return installCandidate{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", source, tempDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		return installCandidate{}, nil, fmt.Errorf("git clone failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}
	info, err := os.Stat(tempDir)
	if err != nil {
		cleanup()
		return installCandidate{}, nil, err
	}
	candidate, err := localInstallCandidate(tempDir, info, requestedName)
	if err != nil {
		cleanup()
		return installCandidate{}, nil, err
	}
	return candidate, cleanup, nil
}

func localInstallCandidate(source string, info os.FileInfo, requestedName string) (installCandidate, error) {
	source = filepath.Clean(source)
	if !info.IsDir() {
		if strings.EqualFold(filepath.Base(source), SkillFileName) {
			return installCandidate{sourceDir: filepath.Dir(source), alias: filepath.Base(filepath.Dir(source))}, nil
		}
		return installCandidate{}, fmt.Errorf("skill file source must be named %s", SkillFileName)
	}
	if hasSkillFile(source) {
		return installCandidate{sourceDir: source, alias: filepath.Base(source)}, nil
	}
	candidates, err := findSkillCandidates(source, requestedName)
	if err != nil {
		return installCandidate{}, err
	}
	if len(candidates) == 0 {
		return installCandidate{}, fmt.Errorf("no %s found under %s", SkillFileName, source)
	}
	if len(candidates) > 1 {
		var names []string
		for _, candidate := range candidates {
			names = append(names, candidate.alias)
		}
		sort.Strings(names)
		return installCandidate{}, fmt.Errorf("multiple skills found: %s; use --name <skill_name>", strings.Join(names, ", "))
	}
	return candidates[0], nil
}

func findSkillCandidates(root, requestedName string) ([]installCandidate, error) {
	requestedName = sanitizeAlias(requestedName)
	var candidates []installCandidate
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && shouldSkipInstallDir(entry.Name()) && path != root {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), SkillFileName) {
			return nil
		}
		dir := filepath.Dir(path)
		alias := sanitizeAlias(filepath.Base(dir))
		if requestedName != "" && alias != requestedName {
			return nil
		}
		candidates = append(candidates, installCandidate{sourceDir: dir, alias: alias})
		return nil
	})
	return candidates, err
}

func copySkillDir(src, dest string) (int, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if !srcInfo.IsDir() {
		return 0, fmt.Errorf("skill source must be a directory")
	}
	if !hasSkillFile(src) {
		return 0, fmt.Errorf("skill source missing %s", SkillFileName)
	}
	files := 0
	err = filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dest, 0755)
		}
		if entry.IsDir() && shouldSkipInstallDir(entry.Name()) {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to install symlink %s", path)
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		files++
		return nil
	})
	return files, err
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	err = os.MkdirAll(filepath.Dir(dest), 0755)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func installRoot(scope Scope, projectRoot, homeDir string) (string, error) {
	switch scope {
	case ScopeProject:
		return filepath.Join(projectRoot, filepath.FromSlash(ProjectSkillsDir)), nil
	case ScopeUser:
		if strings.TrimSpace(homeDir) == "" {
			return "", errors.New("cannot install user skill because home directory is unavailable")
		}
		return filepath.Join(homeDir, filepath.FromSlash(UserSkillsDir)), nil
	default:
		return "", fmt.Errorf("invalid skill scope %q", scope)
	}
}

func looksLikeGitSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "ssh://") ||
		strings.HasPrefix(lower, "git@") ||
		strings.HasSuffix(lower, ".git")
}

func hasSkillFile(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, SkillFileName))
	return err == nil && !info.IsDir()
}

func shouldSkipInstallDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "temp", "tmp":
		return true
	default:
		return false
	}
}

func sanitizeAlias(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".git")
	value = filepath.Base(value)
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	alias := strings.Trim(b.String(), ".-_")
	if alias == "." || alias == ".." {
		return ""
	}
	return alias
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
