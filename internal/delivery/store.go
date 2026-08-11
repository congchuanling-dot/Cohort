package delivery

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	projectStateDir = ".cohort"
	deliveriesDir   = "deliveries"
	worktreesDir    = "delivery-worktrees"

	deliveryFile = "delivery.json"
	contractFile = "contract.json"
	graphFile    = "graph.json"
	eventsFile   = "events.jsonl"
	lockFile     = ".lock"
)

type Store struct {
	ProjectRoot string
	RootDir     string
	WorktreeDir string
	now         func() time.Time
}

type lockRecord struct {
	PID       int       `json:"pid"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

func NewStore(projectRoot string) Store {
	if strings.TrimSpace(projectRoot) == "" {
		projectRoot = "."
	}
	if absolute, err := filepath.Abs(projectRoot); err == nil {
		projectRoot = absolute
	}
	projectRoot = filepath.Clean(projectRoot)
	return Store{
		ProjectRoot: projectRoot,
		RootDir:     filepath.Join(projectRoot, projectStateDir, deliveriesDir),
		WorktreeDir: filepath.Join(projectRoot, projectStateDir, worktreesDir),
		now:         func() time.Time { return time.Now().UTC() },
	}
}

func (s Store) CreateDraft(requirement string, baseCommit string, dirty bool, budget Budget) (Delivery, error) {
	requirement = strings.TrimSpace(requirement)
	baseCommit = strings.TrimSpace(baseCommit)
	if requirement == "" {
		return Delivery{}, errors.New("delivery requirement is required")
	}
	if baseCommit == "" {
		return Delivery{}, errors.New("delivery base commit is required")
	}
	if budget.MaxAgents == 0 {
		budget = DefaultBudget()
	}
	if err := validateBudget(budget); err != nil {
		return Delivery{}, err
	}
	if err := os.MkdirAll(s.RootDir, 0755); err != nil {
		return Delivery{}, err
	}
	release, err := acquireFileLock(filepath.Join(s.RootDir, lockFile), s.now())
	if err != nil {
		return Delivery{}, err
	}
	defer release()

	now := s.now()
	id := s.uniqueDeliveryID(now, requirement)
	delivery := Delivery{
		SchemaVersion:   SchemaVersion,
		ID:              id,
		Status:          StatusDraft,
		Requirement:     requirement,
		RequirementHash: HashString(requirement),
		ProjectRoot:     s.ProjectRoot,
		BaseCommit:      baseCommit,
		DirtyAtPlan:     dirty,
		Budget:          budget,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := os.MkdirAll(s.deliveryDir(id), 0755); err != nil {
		return Delivery{}, err
	}
	if err := s.writeJSON(s.deliveryPath(id), delivery); err != nil {
		return Delivery{}, err
	}
	if err := s.appendEventUnlocked(id, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(now),
		DeliveryID:    id,
		Type:          "DeliveryCreated",
		Time:          now,
		Data: map[string]any{
			"base_commit":   baseCommit,
			"dirty_at_plan": dirty,
		},
	}); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func (s Store) SavePlan(id string, contract AcceptanceContract, graph TaskGraph) (Delivery, error) {
	release, err := s.AcquireDeliveryLock(id)
	if err != nil {
		return Delivery{}, err
	}
	defer release()

	delivery, err := s.loadDeliveryUnlocked(id)
	if err != nil {
		return Delivery{}, err
	}
	if delivery.Status != StatusDraft && delivery.Status != StatusNeedsHumanDecision {
		return Delivery{}, fmt.Errorf("delivery %q cannot save a plan from status %s", id, delivery.Status)
	}
	contract.SchemaVersion = SchemaVersion
	contract.RequirementHash = delivery.RequirementHash
	contract.BaseCommit = delivery.BaseCommit
	graph.SchemaVersion = SchemaVersion
	graph.DeliveryID = delivery.ID
	graph.BaseCommit = delivery.BaseCommit
	if graph.CreatedAt.IsZero() {
		graph.CreatedAt = s.now()
	}
	for index := range graph.Nodes {
		graph.Nodes[index] = normalizeNode(graph.Nodes[index])
	}
	if err := ValidateContract(contract); err != nil {
		return Delivery{}, err
	}
	if err := ValidateGraph(graph, contract); err != nil {
		return Delivery{}, err
	}
	contractHash, err := ContentHash(contract)
	if err != nil {
		return Delivery{}, err
	}
	graphHash, err := ContentHash(graph)
	if err != nil {
		return Delivery{}, err
	}
	nextStatus := StatusPlanned
	for _, question := range contract.Questions {
		if question.Blocking {
			nextStatus = StatusNeedsHumanDecision
			break
		}
	}
	if err := ValidateTransition(delivery.Status, nextStatus); err != nil {
		return Delivery{}, err
	}
	if err := s.writeJSON(s.contractPath(id), contract); err != nil {
		return Delivery{}, err
	}
	if err := s.writeJSON(s.graphPath(id), graph); err != nil {
		return Delivery{}, err
	}
	delivery.ContractHash = contractHash
	delivery.GraphHash = graphHash
	delivery.Status = nextStatus
	delivery.UpdatedAt = s.now()
	delivery.Error = ""
	if err := s.writeJSON(s.deliveryPath(id), delivery); err != nil {
		return Delivery{}, err
	}
	if err := s.appendEventUnlocked(id, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(delivery.UpdatedAt),
		DeliveryID:    id,
		Type:          "ContractCompiled",
		Time:          delivery.UpdatedAt,
		Data: map[string]any{
			"contract_hash": contractHash,
			"graph_hash":    graphHash,
			"status":        nextStatus,
			"criteria":      len(contract.Criteria),
			"nodes":         len(graph.Nodes),
		},
	}); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func (s Store) Transition(id string, to DeliveryStatus, eventType string, data map[string]any) (Delivery, error) {
	release, err := s.AcquireDeliveryLock(id)
	if err != nil {
		return Delivery{}, err
	}
	defer release()
	delivery, err := s.loadDeliveryUnlocked(id)
	if err != nil {
		return Delivery{}, err
	}
	if err := ValidateTransition(delivery.Status, to); err != nil {
		return Delivery{}, err
	}
	delivery.Status = to
	delivery.UpdatedAt = s.now()
	if err := s.writeJSON(s.deliveryPath(id), delivery); err != nil {
		return Delivery{}, err
	}
	if eventType == "" {
		eventType = "DeliveryStatusChanged"
	}
	if data == nil {
		data = map[string]any{}
	}
	data["status"] = to
	if err := s.appendEventUnlocked(id, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(delivery.UpdatedAt),
		DeliveryID:    id,
		Type:          eventType,
		Time:          delivery.UpdatedAt,
		Data:          data,
	}); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func (s Store) Fail(id string, failure error) (Delivery, error) {
	if failure == nil {
		failure = errors.New("delivery failed")
	}
	release, err := s.AcquireDeliveryLock(id)
	if err != nil {
		return Delivery{}, err
	}
	defer release()
	delivery, err := s.loadDeliveryUnlocked(id)
	if err != nil {
		return Delivery{}, err
	}
	if delivery.Status != StatusFailed {
		if err := ValidateTransition(delivery.Status, StatusFailed); err != nil {
			return Delivery{}, err
		}
	}
	delivery.Status = StatusFailed
	delivery.Error = failure.Error()
	delivery.UpdatedAt = s.now()
	if err := s.writeJSON(s.deliveryPath(id), delivery); err != nil {
		return Delivery{}, err
	}
	if err := s.appendEventUnlocked(id, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(delivery.UpdatedAt),
		DeliveryID:    id,
		Type:          "DeliveryFailed",
		Time:          delivery.UpdatedAt,
		Data:          map[string]any{"error": failure.Error()},
	}); err != nil {
		return Delivery{}, err
	}
	return delivery, nil
}

func (s Store) Load(id string) (Delivery, error) {
	if err := validateDeliveryID(id); err != nil {
		return Delivery{}, err
	}
	return s.loadDeliveryUnlocked(id)
}

func (s Store) LoadPlan(id string) (AcceptanceContract, TaskGraph, error) {
	if err := validateDeliveryID(id); err != nil {
		return AcceptanceContract{}, TaskGraph{}, err
	}
	var contract AcceptanceContract
	if err := readJSON(s.contractPath(id), &contract); err != nil {
		return AcceptanceContract{}, TaskGraph{}, err
	}
	var graph TaskGraph
	if err := readJSON(s.graphPath(id), &graph); err != nil {
		return AcceptanceContract{}, TaskGraph{}, err
	}
	return contract, graph, nil
}

func (s Store) List() ([]Delivery, error) {
	entries, err := os.ReadDir(s.RootDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	deliveries := make([]Delivery, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		delivery, err := s.Load(entry.Name())
		if err != nil {
			continue
		}
		deliveries = append(deliveries, delivery)
	}
	sort.Slice(deliveries, func(i, j int) bool {
		return deliveries[i].CreatedAt.After(deliveries[j].CreatedAt)
	})
	return deliveries, nil
}

func (s Store) Latest() (Delivery, error) {
	deliveries, err := s.List()
	if err != nil {
		return Delivery{}, err
	}
	if len(deliveries) == 0 {
		return Delivery{}, errors.New("no deliveries found")
	}
	return deliveries[0], nil
}

func (s Store) AcquireDeliveryLock(id string) (func(), error) {
	if err := validateDeliveryID(id); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.deliveryDir(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("delivery %q not found", id)
		}
		return nil, err
	}
	return acquireFileLock(filepath.Join(s.deliveryDir(id), lockFile), s.now())
}

func (s Store) DeliveryDir(id string) (string, error) {
	if err := validateDeliveryID(id); err != nil {
		return "", err
	}
	return s.deliveryDir(id), nil
}

func (s Store) deliveryDir(id string) string {
	return filepath.Join(s.RootDir, id)
}

func (s Store) deliveryPath(id string) string {
	return filepath.Join(s.deliveryDir(id), deliveryFile)
}

func (s Store) contractPath(id string) string {
	return filepath.Join(s.deliveryDir(id), contractFile)
}

func (s Store) graphPath(id string) string {
	return filepath.Join(s.deliveryDir(id), graphFile)
}

func (s Store) eventsPath(id string) string {
	return filepath.Join(s.deliveryDir(id), eventsFile)
}

func (s Store) loadDeliveryUnlocked(id string) (Delivery, error) {
	var delivery Delivery
	if err := readJSON(s.deliveryPath(id), &delivery); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Delivery{}, fmt.Errorf("delivery %q not found", id)
		}
		return Delivery{}, err
	}
	if delivery.SchemaVersion != SchemaVersion {
		return Delivery{}, fmt.Errorf("delivery %q uses unsupported schema version %d", id, delivery.SchemaVersion)
	}
	return delivery, nil
}

func (s Store) appendEventUnlocked(id string, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	path := s.eventsPath(id)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (s Store) writeJSON(path string, value any) error {
	return atomicWriteJSON(path, value)
}

func (s Store) uniqueDeliveryID(now time.Time, requirement string) string {
	base := "delivery_" + now.Format("20060102_150405") + "_" + slug(requirement)
	id := base
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(s.deliveryDir(id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func normalizeNode(node TaskNode) TaskNode {
	if node.Status == "" {
		node.Status = NodePending
	}
	if node.Risk == "" {
		node.Risk = RiskMedium
	}
	if node.CandidateCount == 0 {
		node.CandidateCount = 1
	}
	if node.Budget.MaxTurns == 0 {
		node.Budget = DefaultNodeBudget()
	}
	return node
}

func validateBudget(budget Budget) error {
	if budget.MaxAgents < 1 || budget.MaxAgents > 32 {
		return errors.New("delivery max_agents must be between 1 and 32")
	}
	if budget.MaxParallel < 1 || budget.MaxParallel > budget.MaxAgents {
		return errors.New("delivery max_parallel must be between 1 and max_agents")
	}
	if budget.MaxTurns < 1 || budget.MaxTokens < 1 || budget.MaxDurationSecond < 1 {
		return errors.New("delivery turn, token, and duration budgets must be positive")
	}
	if budget.MaxCandidates < 1 || budget.MaxCandidates > 2 {
		return errors.New("delivery max_candidates must be 1 or 2")
	}
	if budget.MaxRevisionRounds < 0 || budget.MaxRevisionRounds > 5 {
		return errors.New("delivery max_revision_rounds must be between 0 and 5")
	}
	return nil
}

func validateDeliveryID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("delivery id is required")
	}
	if id != filepath.Base(id) || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return errors.New("invalid delivery id")
	}
	return nil
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0644); err != nil {
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
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func acquireFileLock(path string, now time.Time) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := randomToken()
		if err != nil {
			return nil, err
		}
		record := lockRecord{PID: os.Getpid(), Token: token, CreatedAt: now}
		data, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			if _, writeErr := file.Write(data); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, writeErr
			}
			if syncErr := file.Sync(); syncErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, syncErr
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, closeErr
			}
			return func() {
				var current lockRecord
				if readJSON(path, &current) == nil && current.Token == token {
					_ = os.Remove(path)
				}
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		stale, staleErr := staleLock(path, now)
		if staleErr != nil {
			return nil, staleErr
		}
		if !stale {
			return nil, fmt.Errorf("delivery state is locked: %s", path)
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, removeErr
		}
	}
	return nil, fmt.Errorf("unable to acquire delivery lock: %s", path)
}

func staleLock(path string, now time.Time) (bool, error) {
	var record lockRecord
	if err := readJSON(path, &record); err != nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, statErr
		}
		return now.Sub(info.ModTime()) > 30*time.Second, nil
	}
	if record.PID <= 0 {
		return now.Sub(record.CreatedAt) > 30*time.Second, nil
	}
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return true, nil
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return true, nil
	}
	return false, nil
}

func randomToken() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func newEventID(now time.Time) string {
	token, err := randomToken()
	if err != nil {
		token = fmt.Sprintf("%d", os.Getpid())
	}
	return fmt.Sprintf("evt_%d_%s", now.UnixNano(), token[:min(len(token), 12)])
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			b.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		result = "task"
	}
	if len(result) > 48 {
		result = strings.TrimRight(result[:48], "-")
	}
	return result
}
