package delivery

import (
	"context"
	"time"
)

type RuntimeState struct {
	SchemaVersion int                    `json:"schema_version"`
	DeliveryID    string                 `json:"delivery_id"`
	Nodes         map[string]NodeRuntime `json:"nodes"`
	StartedAt     time.Time              `json:"started_at,omitempty"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

type NodeRuntime struct {
	NodeID     string      `json:"node_id"`
	Status     NodeStatus  `json:"status"`
	Attempt    int         `json:"attempt"`
	Lease      Lease       `json:"lease,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
	SelectedID string      `json:"selected_id,omitempty"`
	LastError  string      `json:"last_error,omitempty"`
	StartedAt  time.Time   `json:"started_at,omitempty"`
	FinishedAt time.Time   `json:"finished_at,omitempty"`
}

type Lease struct {
	OwnerID   string    `json:"owner_id,omitempty"`
	OwnerPID  int       `json:"owner_pid,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Heartbeat time.Time `json:"heartbeat,omitempty"`
}

type CandidateStatus string

const (
	CandidatePending  CandidateStatus = "pending"
	CandidateRunning  CandidateStatus = "running"
	CandidatePassed   CandidateStatus = "passed"
	CandidateFailed   CandidateStatus = "failed"
	CandidateRejected CandidateStatus = "rejected"
	CandidateSelected CandidateStatus = "selected"
)

type Candidate struct {
	ID                string          `json:"id"`
	NodeID            string          `json:"node_id"`
	Status            CandidateStatus `json:"status"`
	BaseCommit        string          `json:"base_commit"`
	DependencyCommits []string        `json:"dependency_commits,omitempty"`
	Branch            string          `json:"branch"`
	WorktreePath      string          `json:"worktree_path"`
	Commit            string          `json:"commit,omitempty"`
	TreeHash          string          `json:"tree_hash,omitempty"`
	ActualWrites      []string        `json:"actual_writes,omitempty"`
	DiffArtifact      string          `json:"diff_artifact,omitempty"`
	ResultArtifact    string          `json:"result_artifact,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	Turns             int             `json:"turns,omitempty"`
	Tokens            int64           `json:"tokens,omitempty"`
	DurationMS        int64           `json:"duration_ms,omitempty"`
	Error             string          `json:"error,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type ArtifactMeta struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	DeliveryID    string    `json:"delivery_id"`
	NodeID        string    `json:"node_id,omitempty"`
	Producer      string    `json:"producer"`
	BaseCommit    string    `json:"base_commit,omitempty"`
	TreeHash      string    `json:"tree_hash,omitempty"`
	ContentHash   string    `json:"content_hash"`
	Size          int64     `json:"size"`
	MediaType     string    `json:"media_type,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type WorkerResult struct {
	Summary      string   `json:"summary"`
	Turns        int      `json:"turns,omitempty"`
	Tokens       int64    `json:"tokens,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
	Commit       string   `json:"commit"`
	TreeHash     string   `json:"tree_hash"`
	ActualWrites []string `json:"actual_writes"`
	Diff         []byte   `json:"diff"`
	Result       []byte   `json:"result,omitempty"`
}

type NodeWorker func(context.Context, Delivery, AcceptanceContract, TaskNode, Candidate) (WorkerResult, error)
