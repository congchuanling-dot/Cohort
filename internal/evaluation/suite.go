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
		if c.Assertions.MaxTurns < 0 || c.Assertions.MaxDurationMS < 0 || c.Assertions.MaxToolFailures < 0 {
			return fmt.Errorf("case %q assertion limits must be >= 0", c.ID)
		}
		for _, pattern := range c.Assertions.OutputRegex {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("case %q invalid output_regex %q: %w", c.ID, pattern, err)
			}
		}
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
