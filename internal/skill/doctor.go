package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiagnosticSeverity 表示 Skill doctor 的单项检查严重程度。
type DiagnosticSeverity string

const (
	DiagnosticOK      DiagnosticSeverity = "ok"
	DiagnosticWarning DiagnosticSeverity = "warn"
	DiagnosticError   DiagnosticSeverity = "error"
)

// Diagnostic 是 Skill doctor 输出的一条检查结果。
type Diagnostic struct {
	Severity DiagnosticSeverity
	Code     string
	Message  string
	Detail   string
}

// ManifestInfo 是安装 manifest 中对用户有用的元数据。
type ManifestInfo struct {
	Source      string
	SourceType  string
	Scope       Scope
	Alias       string
	InstalledAt string
	ContentHash string
}

// DoctorResult 汇总一个 Skill 的健康检查结果。
type DoctorResult struct {
	Skill    Skill
	Path     string
	Manifest *ManifestInfo
	Checks   []Diagnostic
	Healthy  bool
}

// ErrorCount 返回 error 级别诊断数量。
func (r DoctorResult) ErrorCount() int {
	count := 0
	for _, check := range r.Checks {
		if check.Severity == DiagnosticError {
			count++
		}
	}
	return count
}

// WarningCount 返回 warn 级别诊断数量。
func (r DoctorResult) WarningCount() int {
	count := 0
	for _, check := range r.Checks {
		if check.Severity == DiagnosticWarning {
			count++
		}
	}
	return count
}

// Doctor 检查一个已发现 Skill 的路径、正文、manifest 和内容 hash。
func (s *Store) Doctor(id string) (DoctorResult, error) {
	item, err := s.Find(id)
	if err != nil {
		return DoctorResult{}, err
	}
	result := DoctorResult{Skill: item, Path: filepath.Dir(item.Path)}
	dir, err := s.skillDir(item)
	if err != nil {
		result.add(DiagnosticError, "path_scope", "Skill path is outside its configured scope", err.Error())
		return result.finish(), nil
	}
	result.Path = dir
	result.add(DiagnosticOK, "path_scope", "Skill path is inside its configured scope", dir)

	data, err := os.ReadFile(item.Path)
	if err != nil {
		result.add(DiagnosticError, "skill_file", "Cannot read SKILL.md", err.Error())
		return result.finish(), nil
	}
	if strings.TrimSpace(string(data)) == "" {
		result.add(DiagnosticError, "skill_file", "SKILL.md is empty", item.Path)
	} else {
		result.add(DiagnosticOK, "skill_file", "SKILL.md is readable", fmt.Sprintf("%d bytes", len(data)))
	}

	frontMatter := parseFrontMatter(string(data))
	metadata := parseMetadata(data, item.Alias)
	if strings.TrimSpace(frontMatter["name"]) == "" {
		result.add(DiagnosticWarning, "metadata_name", "Skill has no explicit name metadata", "frontmatter name is recommended")
	} else {
		result.add(DiagnosticOK, "metadata_name", "Skill name metadata is available", metadata.Name)
	}
	if metadata.Description == "No description provided." {
		result.add(DiagnosticWarning, "metadata_description", "Skill has no useful description", "add frontmatter description for better routing")
	} else {
		result.add(DiagnosticOK, "metadata_description", "Skill description is available", metadata.Description)
	}

	meta, err := readManifest(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.add(DiagnosticWarning, "manifest", "Install manifest is missing", filepath.Join(dir, manifestFileName))
		} else {
			result.add(DiagnosticError, "manifest", "Install manifest is not readable JSON", err.Error())
		}
		return result.finish(), nil
	}
	result.Manifest = &ManifestInfo{
		Source:      meta.Source,
		SourceType:  meta.SourceType,
		Scope:       meta.Scope,
		Alias:       meta.Alias,
		InstalledAt: meta.InstalledAt,
		ContentHash: meta.ContentHash,
	}
	result.add(DiagnosticOK, "manifest", "Install manifest is readable", filepath.Join(dir, manifestFileName))
	if meta.Scope != "" && meta.Scope != item.Scope {
		result.add(DiagnosticWarning, "manifest_scope", "Manifest scope differs from discovered scope", fmt.Sprintf("manifest=%s discovered=%s", meta.Scope, item.Scope))
	}
	if meta.Alias != "" && meta.Alias != item.Alias {
		result.add(DiagnosticWarning, "manifest_alias", "Manifest alias differs from discovered alias", fmt.Sprintf("manifest=%s discovered=%s", meta.Alias, item.Alias))
	}
	if strings.TrimSpace(meta.Source) == "" {
		result.add(DiagnosticWarning, "manifest_source", "Manifest source is empty", "skill update will require an explicit source")
	} else {
		result.add(DiagnosticOK, "manifest_source", "Manifest source is available", meta.Source)
	}
	if strings.TrimSpace(meta.SourceType) == "" {
		result.add(DiagnosticWarning, "manifest_source_type", "Manifest source type is empty", "reinstall to populate source_type")
	} else {
		result.add(DiagnosticOK, "manifest_source_type", "Manifest source type is available", meta.SourceType)
	}

	files, currentHash, err := hashSkillDir(dir)
	if err != nil {
		result.add(DiagnosticError, "content_hash", "Cannot hash installed Skill content", err.Error())
		return result.finish(), nil
	}
	if strings.TrimSpace(meta.ContentHash) == "" {
		result.add(DiagnosticWarning, "content_hash", "Manifest content hash is missing", fmt.Sprintf("current=%s files=%d", currentHash, files))
	} else if meta.ContentHash != currentHash {
		result.add(DiagnosticError, "content_hash", "Installed Skill content differs from manifest hash", fmt.Sprintf("manifest=%s current=%s files=%d", meta.ContentHash, currentHash, files))
	} else {
		result.add(DiagnosticOK, "content_hash", "Installed Skill content matches manifest hash", fmt.Sprintf("%s files=%d", currentHash, files))
	}
	return result.finish(), nil
}

func (r *DoctorResult) add(severity DiagnosticSeverity, code, message, detail string) {
	r.Checks = append(r.Checks, Diagnostic{
		Severity: severity,
		Code:     code,
		Message:  message,
		Detail:   detail,
	})
}

func (r DoctorResult) finish() DoctorResult {
	r.Healthy = r.ErrorCount() == 0
	return r
}
