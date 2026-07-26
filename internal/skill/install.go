package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const manifestFileName = ".cohert-skill.json"

// InstallOptions 描述一次 Skill 安装请求。
type InstallOptions struct {
	Source      string
	Scope       Scope
	Name        string
	Force       bool
	DryRun      bool
	Pin         string
	ProjectRoot string
	HomeDir     string
}

// InstallResult 描述安装后的本地 Skill。
type InstallResult struct {
	Skill        Skill
	Source       string
	SourceType   string
	SourceRef    string
	RequestedRef string
	ResolvedRef  string
	Pinned       bool
	Destination  string
	Replaced     bool
	WouldReplace bool
	DryRun       bool
	Files        int
	ContentHash  string
}

// UninstallResult 描述删除一个本地 Skill 的结果。
type UninstallResult struct {
	Skill Skill
	Path  string
}

// UpdateResult 描述更新一个本地 Skill 的结果。
type UpdateResult struct {
	InstallResult
	Previous Skill
}

// UpdateOptions 描述 Skill 更新请求。
type UpdateOptions struct {
	ID     string
	Source string
	Pin    string
}

// UpdateCheckResult 描述一次不落盘的更新检查。
type UpdateCheckResult struct {
	Skill         Skill
	Source        string
	SourceType    string
	SourceRef     string
	RequestedRef  string
	ResolvedRef   string
	Pinned        bool
	Destination   string
	CurrentHash   string
	ManifestHash  string
	CandidateHash string
	Files         int
	UpToDate      bool
}

type manifest struct {
	Source       string `json:"source"`
	SourceType   string `json:"source_type"`
	SourceRef    string `json:"source_ref"`
	RequestedRef string `json:"requested_ref"`
	ResolvedRef  string `json:"resolved_ref"`
	Pinned       bool   `json:"pinned"`
	Scope        Scope  `json:"scope"`
	Alias        string `json:"alias"`
	InstalledAt  string `json:"installed_at"`
	ContentHash  string `json:"content_hash"`
}

type installCandidate struct {
	sourceDir    string
	alias        string
	sourceType   string
	sourceRef    string
	requestedRef string
	resolvedRef  string
	pinned       bool
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

	candidate, cleanup, err := resolveInstallCandidate(ctx, source, opts.Name, opts.Pin)
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
	files, contentHash, err := hashSkillDir(candidate.sourceDir)
	if err != nil {
		return InstallResult{}, err
	}
	replaced := false
	wouldReplace := false
	if _, err := os.Stat(dest); err == nil {
		wouldReplace = opts.Force
		if !opts.Force && !opts.DryRun {
			return InstallResult{}, fmt.Errorf("skill %s/%s already exists; use --force to replace it", scope, alias)
		}
		if opts.Force && !opts.DryRun {
			replaced = true
			if err := os.RemoveAll(dest); err != nil {
				return InstallResult{}, err
			}
		}
	} else if !os.IsNotExist(err) {
		return InstallResult{}, err
	}
	data, err := os.ReadFile(filepath.Join(candidate.sourceDir, SkillFileName))
	if err != nil {
		return InstallResult{}, err
	}
	metadata := parseMetadata(data, alias)
	item := Skill{
		ID:            string(scope) + "/" + alias,
		Alias:         alias,
		Name:          metadata.Name,
		Description:   metadata.Description,
		UserInvocable: metadata.UserInvocable,
		ArgumentHint:  metadata.ArgumentHint,
		Scope:         scope,
		Path:          filepath.Join(dest, SkillFileName),
	}
	if opts.DryRun {
		return InstallResult{
			Skill:        item,
			Source:       source,
			SourceType:   candidate.sourceType,
			SourceRef:    candidate.sourceRef,
			RequestedRef: candidate.requestedRef,
			ResolvedRef:  candidate.resolvedRef,
			Pinned:       candidate.pinned,
			Destination:  dest,
			WouldReplace: wouldReplace,
			DryRun:       true,
			Files:        files,
			ContentHash:  contentHash,
		}, nil
	}
	if _, err := copySkillDir(candidate.sourceDir, dest); err != nil {
		return InstallResult{}, err
	}
	if err := writeManifest(dest, manifest{
		Source:       source,
		SourceType:   candidate.sourceType,
		SourceRef:    candidate.sourceRef,
		RequestedRef: candidate.requestedRef,
		ResolvedRef:  candidate.resolvedRef,
		Pinned:       candidate.pinned,
		Scope:        scope,
		Alias:        alias,
		InstalledAt:  timeNowRFC3339(),
		ContentHash:  contentHash,
	}); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{
		Skill:        item,
		Source:       source,
		SourceType:   candidate.sourceType,
		SourceRef:    candidate.sourceRef,
		RequestedRef: candidate.requestedRef,
		ResolvedRef:  candidate.resolvedRef,
		Pinned:       candidate.pinned,
		Destination:  dest,
		Replaced:     replaced,
		WouldReplace: wouldReplace,
		Files:        files,
		ContentHash:  contentHash,
	}, nil
}

// Uninstall 删除一个已发现的 Skill 目录，并刷新 Store。
func (s *Store) Uninstall(id string) (UninstallResult, error) {
	item, err := s.Find(id)
	if err != nil {
		return UninstallResult{}, err
	}
	dir, err := s.skillDir(item)
	if err != nil {
		return UninstallResult{}, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return UninstallResult{}, err
	}
	if err := s.Reload(); err != nil {
		return UninstallResult{}, err
	}
	return UninstallResult{Skill: item, Path: dir}, nil
}

// Update 用新的来源替换一个已安装 Skill。source 为空时读取安装 manifest。
func (s *Store) Update(ctx context.Context, id string, source string) (UpdateResult, error) {
	return s.UpdateWithOptions(ctx, UpdateOptions{ID: id, Source: source})
}

// UpdateWithOptions 用新的来源替换一个已安装 Skill，并可锁定到指定 git ref。
func (s *Store) UpdateWithOptions(ctx context.Context, opts UpdateOptions) (UpdateResult, error) {
	item, source, pin, err := s.resolveUpdateSource(opts.ID, opts.Source, opts.Pin)
	if err != nil {
		return UpdateResult{}, err
	}
	result, err := Install(ctx, InstallOptions{
		Source:      source,
		Scope:       item.Scope,
		Name:        item.Alias,
		Force:       true,
		Pin:         pin,
		ProjectRoot: s.workspace,
		HomeDir:     s.homeDir,
	})
	if err != nil {
		return UpdateResult{}, err
	}
	if err := s.Reload(); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{InstallResult: result, Previous: item}, nil
}

// CheckUpdate 检查 Skill 来源是否有新内容，不写入目标目录。
func (s *Store) CheckUpdate(ctx context.Context, opts UpdateOptions) (UpdateCheckResult, error) {
	item, source, pin, err := s.resolveUpdateSource(opts.ID, opts.Source, opts.Pin)
	if err != nil {
		return UpdateCheckResult{}, err
	}
	dir, err := s.skillDir(item)
	if err != nil {
		return UpdateCheckResult{}, err
	}
	_, currentHash, err := hashSkillDir(dir)
	if err != nil {
		return UpdateCheckResult{}, err
	}
	manifestHash := ""
	if meta, err := readManifest(dir); err == nil {
		manifestHash = meta.ContentHash
	}
	candidate, err := Install(ctx, InstallOptions{
		Source:      source,
		Scope:       item.Scope,
		Name:        item.Alias,
		Force:       true,
		DryRun:      true,
		Pin:         pin,
		ProjectRoot: s.workspace,
		HomeDir:     s.homeDir,
	})
	if err != nil {
		return UpdateCheckResult{}, err
	}
	return UpdateCheckResult{
		Skill:         item,
		Source:        candidate.Source,
		SourceType:    candidate.SourceType,
		SourceRef:     candidate.SourceRef,
		RequestedRef:  candidate.RequestedRef,
		ResolvedRef:   candidate.ResolvedRef,
		Pinned:        candidate.Pinned,
		Destination:   candidate.Destination,
		CurrentHash:   currentHash,
		ManifestHash:  manifestHash,
		CandidateHash: candidate.ContentHash,
		Files:         candidate.Files,
		UpToDate:      currentHash == candidate.ContentHash,
	}, nil
}

func (s *Store) resolveUpdateSource(id, source, pin string) (Skill, string, string, error) {
	item, err := s.Find(id)
	if err != nil {
		return Skill{}, "", "", err
	}
	source = strings.TrimSpace(source)
	pin = strings.TrimSpace(pin)
	if strings.TrimSpace(source) == "" {
		meta, err := s.manifestForSkill(item)
		if err != nil {
			return Skill{}, "", "", err
		}
		source = strings.TrimSpace(meta.Source)
		if pin == "" && meta.Pinned {
			pin = strings.TrimSpace(meta.SourceRef)
		}
	}
	if source == "" {
		return Skill{}, "", "", fmt.Errorf("skill %s install source metadata is empty; pass a source explicitly", item.ID)
	}
	return item, source, pin, nil
}

func resolveInstallCandidate(ctx context.Context, source, requestedName, pin string) (installCandidate, func(), error) {
	pin = strings.TrimSpace(pin)
	if info, err := os.Stat(source); err == nil {
		if pin != "" {
			return installCandidate{}, nil, fmt.Errorf("--pin is only supported for git skill sources")
		}
		candidate, err := localInstallCandidate(source, info, requestedName)
		if info.IsDir() {
			candidate.sourceType = "local-dir"
		} else {
			candidate.sourceType = "local-file"
		}
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
	requestedRef, sourceRef, resolvedRef, pinned, err := checkoutGitPin(ctx, tempDir, pin)
	if err != nil {
		cleanup()
		return installCandidate{}, nil, err
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
	candidate.sourceType = "git"
	candidate.requestedRef = requestedRef
	candidate.sourceRef = sourceRef
	candidate.resolvedRef = resolvedRef
	candidate.pinned = pinned
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
		if !entry.IsDir() && entry.Name() == manifestFileName {
			return nil
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

func checkoutGitPin(ctx context.Context, repoDir, pin string) (string, string, string, bool, error) {
	pin = strings.TrimSpace(pin)
	if pin != "" {
		fetch := exec.CommandContext(ctx, "git", "-C", repoDir, "fetch", "--depth", "1", "origin", pin)
		if output, err := fetch.CombinedOutput(); err != nil {
			checkout := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "--detach", pin)
			if checkoutOutput, checkoutErr := checkout.CombinedOutput(); checkoutErr != nil {
				return "", "", "", false, fmt.Errorf("git fetch pin %q failed: %w\n%s\ngit checkout pin failed: %v\n%s", pin, err, strings.TrimSpace(string(output)), checkoutErr, strings.TrimSpace(string(checkoutOutput)))
			}
		} else {
			checkout := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "--detach", "FETCH_HEAD")
			if checkoutOutput, err := checkout.CombinedOutput(); err != nil {
				return "", "", "", false, fmt.Errorf("git checkout pin %q failed: %w\n%s", pin, err, strings.TrimSpace(string(checkoutOutput)))
			}
		}
	}
	resolved, err := gitRevParse(ctx, repoDir, "HEAD")
	if err != nil {
		return "", "", "", false, err
	}
	if pin == "" {
		return "", "", resolved, false, nil
	}
	return pin, resolved, resolved, true, nil
}

func gitRevParse(ctx context.Context, repoDir, rev string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", rev)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s failed: %w\n%s", rev, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func hashSkillDir(src string) (int, string, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, "", err
	}
	if !srcInfo.IsDir() {
		return 0, "", fmt.Errorf("skill source must be a directory")
	}
	if !hasSkillFile(src) {
		return 0, "", fmt.Errorf("skill source missing %s", SkillFileName)
	}
	h := sha256.New()
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
			return nil
		}
		if entry.IsDir() && shouldSkipInstallDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == manifestFileName {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to install symlink %s", path)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := hashFile(h, src, path); err != nil {
			return err
		}
		files++
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(h hash.Hash, root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	fmt.Fprintf(h, "file:%s\n", rel)
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	_, err = h.Write([]byte("\n"))
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

func (s *Store) skillDir(item Skill) (string, error) {
	dir := filepath.Dir(item.Path)
	root, err := installRoot(item.Scope, s.workspace, s.homeDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("refusing to modify skill outside %s: %s", root, dir)
	}
	if filepath.Base(item.Path) != SkillFileName {
		return "", fmt.Errorf("refusing to modify invalid skill path %s", item.Path)
	}
	return dir, nil
}

func (s *Store) manifestSource(item Skill) (string, error) {
	meta, err := s.manifestForSkill(item)
	if err != nil {
		return "", err
	}
	return meta.Source, nil
}

func (s *Store) manifestForSkill(item Skill) (manifest, error) {
	dir, err := s.skillDir(item)
	if err != nil {
		return manifest{}, err
	}
	meta, err := readManifest(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return manifest{}, fmt.Errorf("skill %s has no install source metadata; pass a source explicitly", item.ID)
		}
		return manifest{}, err
	}
	if strings.TrimSpace(meta.Source) == "" {
		return manifest{}, fmt.Errorf("skill %s install source metadata is empty; pass a source explicitly", item.ID)
	}
	return meta, nil
}

func readManifest(dir string) (manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestFileName))
	if err != nil {
		return manifest{}, err
	}
	var meta manifest
	if err := json.Unmarshal(data, &meta); err != nil {
		return manifest{}, err
	}
	return meta, nil
}

func writeManifest(dir string, meta manifest) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, manifestFileName), data, 0644)
}

func timeNowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

func looksLikeGitSource(source string) bool {
	lower := strings.ToLower(source)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "file://") ||
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
