package delivery

import "time"

const SchemaVersion = 1

type DeliveryStatus string

const (
	StatusDraft              DeliveryStatus = "draft"
	StatusPlanned            DeliveryStatus = "planned"
	StatusRunning            DeliveryStatus = "running"
	StatusIntegrating        DeliveryStatus = "integrating"
	StatusVerifying          DeliveryStatus = "verifying"
	StatusNeedsRevision      DeliveryStatus = "needs_revision"
	StatusNeedsHumanDecision DeliveryStatus = "needs_human_decision"
	StatusReadyForReview     DeliveryStatus = "ready_for_review"
	StatusApproved           DeliveryStatus = "approved"
	StatusMerging            DeliveryStatus = "merging"
	StatusMergedUnverified   DeliveryStatus = "merged_unverified"
	StatusVerified           DeliveryStatus = "verified"
	StatusBudgetExhausted    DeliveryStatus = "budget_exhausted"
	StatusFailed             DeliveryStatus = "failed"
	StatusCancelled          DeliveryStatus = "cancelled"
)

type NodeStatus string

const (
	NodePending     NodeStatus = "pending"
	NodeReady       NodeStatus = "ready"
	NodeRunning     NodeStatus = "running"
	NodePassed      NodeStatus = "passed"
	NodeFailed      NodeStatus = "failed"
	NodeBlocked     NodeStatus = "blocked"
	NodeNeedsReview NodeStatus = "needs_review"
	NodeCancelled   NodeStatus = "cancelled"
)

type AgentRole string

const (
	RoleScout                 AgentRole = "scout"
	RolePlanner               AgentRole = "planner"
	RoleBuilder               AgentRole = "builder"
	RoleTestBuilder           AgentRole = "test_builder"
	RoleIntegrator            AgentRole = "integrator"
	RoleSpecVerifier          AgentRole = "spec_verifier"
	RoleCorrectnessVerifier   AgentRole = "correctness_verifier"
	RoleSecurityVerifier      AgentRole = "security_verifier"
	RolePerformanceVerifier   AgentRole = "performance_verifier"
	RoleCompatibilityVerifier AgentRole = "compatibility_verifier"
	RoleRevisionBuilder       AgentRole = "revision_builder"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type VerificationKind string

const (
	VerifyCommand        VerificationKind = "command"
	VerifyFileAssertion  VerificationKind = "file_assertion"
	VerifyAPIContract    VerificationKind = "api_contract"
	VerifyBehavioralEval VerificationKind = "behavioral_eval"
	VerifyRubric         VerificationKind = "rubric"
	VerifyHuman          VerificationKind = "human"
)

type EvidencePolicy string

const (
	EvidenceExecution EvidencePolicy = "execution"
	EvidenceStatic    EvidencePolicy = "static"
	EvidenceSemantic  EvidencePolicy = "semantic"
	EvidenceHuman     EvidencePolicy = "human"
)

type Budget struct {
	MaxAgents         int   `json:"max_agents"`
	MaxParallel       int   `json:"max_parallel"`
	MaxTurns          int   `json:"max_turns"`
	MaxTokens         int64 `json:"max_tokens"`
	MaxDurationSecond int   `json:"max_duration_seconds"`
	MaxCandidates     int   `json:"max_candidates"`
	MaxRevisionRounds int   `json:"max_revision_rounds"`
}

type NodeBudget struct {
	MaxTurns          int   `json:"max_turns"`
	MaxTokens         int64 `json:"max_tokens"`
	MaxDurationSecond int   `json:"max_duration_seconds"`
}

type Delivery struct {
	SchemaVersion   int            `json:"schema_version"`
	ID              string         `json:"id"`
	Status          DeliveryStatus `json:"status"`
	Requirement     string         `json:"requirement"`
	RequirementHash string         `json:"requirement_hash"`
	ProjectRoot     string         `json:"project_root"`
	BaseCommit      string         `json:"base_commit"`
	DirtyAtPlan     bool           `json:"dirty_at_plan,omitempty"`
	ContractHash    string         `json:"contract_hash,omitempty"`
	GraphHash       string         `json:"graph_hash,omitempty"`
	IntegrationTree string         `json:"integration_tree,omitempty"`
	MergeCommit     string         `json:"merge_commit,omitempty"`
	Budget          Budget         `json:"budget"`
	Error           string         `json:"error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	ApprovedAt      time.Time      `json:"approved_at,omitempty"`
	VerifiedAt      time.Time      `json:"verified_at,omitempty"`
}

type AcceptanceContract struct {
	SchemaVersion   int            `json:"schema_version"`
	RequirementHash string         `json:"requirement_hash"`
	BaseCommit      string         `json:"base_commit"`
	Summary         string         `json:"summary"`
	Criteria        []Criterion    `json:"criteria"`
	Invariants      []Invariant    `json:"invariants,omitempty"`
	AllowedScope    []string       `json:"allowed_scope"`
	ForbiddenScope  []string       `json:"forbidden_scope,omitempty"`
	RiskProfile     RiskProfile    `json:"risk_profile"`
	RequiredGates   []GateSpec     `json:"required_gates"`
	Questions       []OpenQuestion `json:"questions,omitempty"`
}

type Criterion struct {
	ID             string           `json:"id"`
	Statement      string           `json:"statement"`
	Mandatory      bool             `json:"mandatory"`
	Verification   VerificationKind `json:"verification"`
	TargetPaths    []string         `json:"target_paths,omitempty"`
	EvidencePolicy EvidencePolicy   `json:"evidence_policy"`
	GateIDs        []string         `json:"gate_ids,omitempty"`
}

type Invariant struct {
	ID        string   `json:"id"`
	Statement string   `json:"statement"`
	Paths     []string `json:"paths,omitempty"`
}

type RiskProfile struct {
	Level                  RiskLevel `json:"level"`
	Reasons                []string  `json:"reasons,omitempty"`
	SecuritySensitive      bool      `json:"security_sensitive,omitempty"`
	CompatibilitySensitive bool      `json:"compatibility_sensitive,omitempty"`
	PerformanceSensitive   bool      `json:"performance_sensitive,omitempty"`
}

type GateSpec struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Command        []string `json:"command,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	Mandatory      bool     `json:"mandatory"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

type OpenQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Blocking bool   `json:"blocking"`
}

type TaskGraph struct {
	SchemaVersion int        `json:"schema_version"`
	DeliveryID    string     `json:"delivery_id"`
	BaseCommit    string     `json:"base_commit"`
	Nodes         []TaskNode `json:"nodes"`
	CreatedAt     time.Time  `json:"created_at"`
}

type TaskNode struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Objective      string     `json:"objective"`
	Role           AgentRole  `json:"role"`
	Status         NodeStatus `json:"status"`
	Dependencies   []string   `json:"dependencies,omitempty"`
	ReadSet        []string   `json:"read_set,omitempty"`
	DeclaredWrites []string   `json:"declared_writes,omitempty"`
	ActualWrites   []string   `json:"actual_writes,omitempty"`
	Criteria       []string   `json:"criteria"`
	Risk           RiskLevel  `json:"risk"`
	CandidateCount int        `json:"candidate_count"`
	Budget         NodeBudget `json:"budget"`
	Attempt        int        `json:"attempt,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
}

type PlanDraft struct {
	Contract AcceptanceContract `json:"contract"`
	Graph    TaskGraph          `json:"graph"`
}

type Event struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	DeliveryID    string         `json:"delivery_id"`
	NodeID        string         `json:"node_id,omitempty"`
	Type          string         `json:"type"`
	Time          time.Time      `json:"time"`
	Data          map[string]any `json:"data,omitempty"`
}

func DefaultBudget() Budget {
	return Budget{
		MaxAgents:         5,
		MaxParallel:       3,
		MaxTurns:          300,
		MaxTokens:         600000,
		MaxDurationSecond: 7200,
		MaxCandidates:     2,
		MaxRevisionRounds: 2,
	}
}

func DefaultNodeBudget() NodeBudget {
	return NodeBudget{
		MaxTurns:          80,
		MaxTokens:         120000,
		MaxDurationSecond: 1800,
	}
}
