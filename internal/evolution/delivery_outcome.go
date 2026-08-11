package evolution

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ReflectTaskDeliveryOutcomeReport = "delivery-outcome-report"
	DeliveryOutcomeReportPath        = "memory/reflection/delivery_outcomes.md"
	deliveryOutcomeRecordsPath       = "memory/reflection/delivery_outcomes.jsonl"
	deliveryReflectionInputFile      = "reflection_input.json"
)

type DeliveryOutcomeInput struct {
	SchemaVersion      int       `json:"schema_version"`
	DeliveryID         string    `json:"delivery_id"`
	RequirementHash    string    `json:"requirement_hash"`
	ContractHash       string    `json:"contract_hash"`
	BaseCommit         string    `json:"base_commit"`
	MergeCommit        string    `json:"merge_commit"`
	TreeHash           string    `json:"tree_hash"`
	Status             string    `json:"status"`
	Criteria           int       `json:"criteria"`
	Gates              int       `json:"gates"`
	VerifierReports    int       `json:"verifier_reports"`
	RevisionRounds     int       `json:"revision_rounds"`
	OpenFindings       int       `json:"open_findings"`
	BlockingFindings   int       `json:"blocking_findings"`
	ChangedFiles       int       `json:"changed_files"`
	Tokens             int64     `json:"tokens"`
	AgentDurationMS    int64     `json:"agent_duration_ms"`
	DeliveryDurationMS int64     `json:"delivery_duration_ms"`
	VerifiedAt         time.Time `json:"verified_at"`
}

func EnqueueDeliveryOutcome(queue ReflectionQueue, memoryWorkspace string, deliveryPath string, input DeliveryOutcomeInput) (ReflectionTrigger, bool, error) {
	if strings.TrimSpace(input.DeliveryID) == "" || strings.TrimSpace(input.TreeHash) == "" {
		return ReflectionTrigger{}, false, errors.New("delivery outcome requires delivery id and tree hash")
	}
	if input.SchemaVersion == 0 {
		input.SchemaVersion = 1
	}
	deliveryPath = filepath.Clean(deliveryPath)
	if err := os.MkdirAll(deliveryPath, 0755); err != nil {
		return ReflectionTrigger{}, false, err
	}
	inputPath := filepath.Join(deliveryPath, deliveryReflectionInputFile)
	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return ReflectionTrigger{}, false, err
	}
	if err := atomicWriteDeliveryOutcome(inputPath, data); err != nil {
		return ReflectionTrigger{}, false, err
	}
	return queue.Enqueue(ReflectionTrigger{
		Kind:            "delivery",
		ProjectRoot:     queue.ProjectRoot,
		MemoryWorkspace: memoryWorkspace,
		DeliveryPath:    inputPath,
		SessionID:       "delivery-" + input.DeliveryID,
		RunID:           input.MergeCommit,
		HistoryLen:      1,
		RunStatus:       input.Status,
	})
}

func (m Manager) writeDeliveryOutcomeReport(inputPath string) ([]string, error) {
	data, err := os.ReadFile(filepath.Clean(inputPath))
	if err != nil {
		return nil, err
	}
	var input DeliveryOutcomeInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, err
	}
	if input.DeliveryID == "" || input.TreeHash == "" || input.Status != "verified" {
		return nil, errors.New("delivery reflection input is incomplete or unverified")
	}
	recordsPath, err := m.resolveMemoryPath(deliveryOutcomeRecordsPath)
	if err != nil {
		return nil, err
	}
	reportPath, err := m.resolveMemoryPath(DeliveryOutcomeReportPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(recordsPath), defaultMemoryDirectoryPerm); err != nil {
		return nil, err
	}
	records, err := readDeliveryOutcomes(recordsPath)
	if err != nil {
		return nil, err
	}
	key := input.DeliveryID + "\x00" + input.TreeHash
	exists := false
	for _, record := range records {
		if record.DeliveryID+"\x00"+record.TreeHash == key {
			exists = true
			break
		}
	}
	if !exists {
		records = append(records, input)
		sort.Slice(records, func(left, right int) bool {
			if records[left].VerifiedAt.Equal(records[right].VerifiedAt) {
				return records[left].DeliveryID < records[right].DeliveryID
			}
			return records[left].VerifiedAt.Before(records[right].VerifiedAt)
		})
		if err := writeDeliveryOutcomeRecords(recordsPath, records); err != nil {
			return nil, err
		}
	}
	if err := writeDeliveryOutcomeMarkdown(reportPath, records); err != nil {
		return nil, err
	}
	return []string{recordsPath, reportPath}, nil
}

func readDeliveryOutcomes(path string) ([]DeliveryOutcomeInput, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []DeliveryOutcomeInput
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var record DeliveryOutcomeInput
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func writeDeliveryOutcomeRecords(path string, records []DeliveryOutcomeInput) error {
	var builder strings.Builder
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		builder.Write(data)
		builder.WriteByte('\n')
	}
	return atomicWriteDeliveryOutcome(path, []byte(builder.String()))
}

func writeDeliveryOutcomeMarkdown(path string, records []DeliveryOutcomeInput) error {
	var builder strings.Builder
	builder.WriteString("# Verified Delivery Outcomes\n\n")
	builder.WriteString("- privacy_policy: requirement and code content omitted; only hashes, counts, timings, and outcomes are retained\n")
	fmt.Fprintf(&builder, "- verified_deliveries: %d\n\n", len(records))
	for _, record := range records {
		fmt.Fprintf(&builder, "## %s\n\n", record.DeliveryID)
		fmt.Fprintf(&builder, "- verified_at: %s\n", record.VerifiedAt.UTC().Format(time.RFC3339))
		fmt.Fprintf(&builder, "- requirement_hash: `%s`\n- contract_hash: `%s`\n", record.RequirementHash, record.ContractHash)
		fmt.Fprintf(&builder, "- base_commit: `%s`\n- merge_commit: `%s`\n- tree_hash: `%s`\n", record.BaseCommit, record.MergeCommit, record.TreeHash)
		fmt.Fprintf(&builder, "- criteria: %d\n- gates: %d\n- verifier_reports: %d\n- revision_rounds: %d\n",
			record.Criteria, record.Gates, record.VerifierReports, record.RevisionRounds)
		fmt.Fprintf(&builder, "- open_findings: %d\n- blocking_findings: %d\n- changed_files: %d\n",
			record.OpenFindings, record.BlockingFindings, record.ChangedFiles)
		fmt.Fprintf(&builder, "- tokens: %d\n- agent_duration_ms: %d\n- delivery_duration_ms: %d\n\n",
			record.Tokens, record.AgentDurationMS, record.DeliveryDurationMS)
	}
	return atomicWriteDeliveryOutcome(path, []byte(builder.String()))
}

func atomicWriteDeliveryOutcome(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), defaultMemoryDirectoryPerm); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".delivery-outcome-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(defaultMemoryFilePerm); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
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
