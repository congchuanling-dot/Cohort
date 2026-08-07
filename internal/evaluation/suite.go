package evaluation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func LoadSuite(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		return Suite{}, fmt.Errorf("parse eval suite %s: %w", path, err)
	}
	if err := ValidateSuite(suite); err != nil {
		return Suite{}, fmt.Errorf("invalid eval suite %s: %w", path, err)
	}
	return suite, nil
}

func SaveSuite(path string, suite Suite) error {
	if suite.SchemaVersion == 0 {
		suite.SchemaVersion = SchemaVersion
	}
	if err := ValidateSuite(suite); err != nil {
		return err
	}
	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func ValidateSuite(suite Suite) error {
	if suite.SchemaVersion != 0 && suite.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", suite.SchemaVersion)
	}
	if strings.TrimSpace(suite.ID) == "" {
		return errors.New("suite id is required")
	}
	if len(suite.Cases) == 0 {
		return errors.New("suite must contain at least one case")
	}
	if suite.DefaultRepeat < 0 || suite.DefaultRepeat > 20 {
		return errors.New("default_repeat must be between 0 and 20")
	}
	seen := map[string]bool{}
	for index, c := range suite.Cases {
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("cases[%d].id is required", index)
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Prompt) == "" {
			return fmt.Errorf("case %q prompt is required", c.ID)
		}
		if c.TimeoutSec < 0 {
			return fmt.Errorf("case %q timeout_seconds must be >= 0", c.ID)
		}
		if c.Repeat < 0 || c.Repeat > 20 {
			return fmt.Errorf("case %q repeat must be between 0 and 20", c.ID)
		}
		if c.Environment.OnMissing != "" && c.Environment.OnMissing != "skip" && c.Environment.OnMissing != "fail" {
			return fmt.Errorf("case %q environment.on_missing must be skip or fail", c.ID)
		}
		for _, value := range append(append(append([]string{}, c.Environment.OperatingSystems...), c.Environment.Commands...), c.Environment.Applications...) {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("case %q environment requirements must not contain empty values", c.ID)
			}
		}
		mode := strings.TrimSpace(c.Fixture.Mode)
		if mode != "" && mode != "project" && mode != "temp" {
			return fmt.Errorf("case %q fixture.mode must be project or temp", c.ID)
		}
		for path := range c.Fixture.Files {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q fixture file %q: %w", c.ID, path, err)
			}
		}
		browserFixture := strings.TrimSpace(c.Fixture.BrowserFixture)
		if browserFixture != "" && browserFixture != "form" && browserFixture != "ocr-canvas" {
			return fmt.Errorf("case %q fixture.browser_fixture must be form or ocr-canvas", c.ID)
		}
		if strings.ContainsRune(c.Fixture.LaunchApplication, '\x00') {
			return fmt.Errorf("case %q fixture.launch_application contains an invalid NUL byte", c.ID)
		}
		if c.Assertions.MaxTurns < 0 || c.Assertions.MaxDurationMS < 0 || c.Assertions.MaxToolFailures < 0 {
			return fmt.Errorf("case %q assertion limits must be >= 0", c.ID)
		}
		if c.Assertions.MaxToolCalls < 0 {
			return fmt.Errorf("case %q max_tool_calls must be >= 0", c.ID)
		}
		for _, path := range append(append([]string{}, c.Assertions.FilesExist...), c.Assertions.FilesNotExist...) {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q state path %q: %w", c.ID, path, err)
			}
		}
		for path := range c.Assertions.FileContains {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q file_contains path %q: %w", c.ID, path, err)
			}
		}
		for path := range c.Assertions.FileEquals {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q file_equals path %q: %w", c.ID, path, err)
			}
		}
		for path := range c.Assertions.FileNotContains {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q file_not_contains path %q: %w", c.ID, path, err)
			}
		}
		for path, expected := range c.Assertions.FileJSONEquals {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q file_json_equals path %q: %w", c.ID, path, err)
			}
			var value any
			if err := json.Unmarshal(expected, &value); err != nil {
				return fmt.Errorf("case %q file_json_equals %q is invalid json: %w", c.ID, path, err)
			}
		}
		for path := range c.Assertions.FileDiffContains {
			if err := validateRelativePath(path); err != nil {
				return fmt.Errorf("case %q file_diff_contains path %q: %w", c.ID, path, err)
			}
		}
		for _, pattern := range c.Assertions.OutputRegex {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("case %q invalid output_regex %q: %w", c.ID, pattern, err)
			}
		}
		for index, assertion := range c.Assertions.CommandAssertions {
			if strings.TrimSpace(assertion.Command) == "" {
				return fmt.Errorf("case %q command_assertions[%d].command is required", c.ID, index)
			}
			if assertion.TimeoutSec < 0 {
				return fmt.Errorf("case %q command_assertions[%d].timeout_seconds must be >= 0", c.ID, index)
			}
			for _, pattern := range assertion.OutputRegex {
				if _, err := regexp.Compile(pattern); err != nil {
					return fmt.Errorf("case %q command_assertions[%d] invalid output_regex %q: %w", c.ID, index, pattern, err)
				}
			}
		}
		if c.Assertions.Judge != nil && (c.Assertions.Judge.MinScore < 0 || c.Assertions.Judge.MinScore > 100) {
			return fmt.Errorf("case %q judge.min_score must be between 0 and 100", c.ID)
		}
	}
	return nil
}

func validateRelativePath(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return errors.New("must be a non-empty relative path inside the fixture workspace")
	}
	return nil
}

func FilterCases(suite Suite, caseIDs []string, tags []string) (Suite, error) {
	idFilter := stringSet(caseIDs)
	tagFilter := stringSet(tags)
	filtered := suite
	filtered.Cases = nil
	for _, c := range suite.Cases {
		if len(idFilter) > 0 && !idFilter[c.ID] {
			continue
		}
		if len(tagFilter) > 0 && !hasAny(c.Tags, tagFilter) {
			continue
		}
		filtered.Cases = append(filtered.Cases, c)
	}
	if len(filtered.Cases) == 0 {
		return Suite{}, errors.New("no eval cases matched the selected filters")
	}
	return filtered, nil
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func hasAny(values []string, filter map[string]bool) bool {
	for _, value := range values {
		if filter[value] {
			return true
		}
	}
	return false
}
