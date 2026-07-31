package capability

import "time"

const (
	StatusMissing   = "missing"
	StatusProposed  = "proposed"
	StatusCandidate = "candidate"
	StatusAvailable = "available"
	StatusDisabled  = "disabled"
	StatusFailed    = "failed"

	TypeSkill = "skill"
	TypeTool  = "tool"
	TypeMCP   = "mcp"
	TypeSOP   = "sop"
)

type Registry struct {
	Version      int          `json:"version"`
	UpdatedAt    time.Time    `json:"updated_at"`
	Capabilities []Capability `json:"capabilities"`
	Gaps         []Gap        `json:"gaps"`
	Proposals    []Proposal   `json:"proposals"`
}

type Capability struct {
	ID           string       `json:"id"`
	Status       string       `json:"status"`
	Type         string       `json:"type,omitempty"`
	Entry        string       `json:"entry,omitempty"`
	Triggers     []string     `json:"triggers,omitempty"`
	Requires     Requirements `json:"requires,omitempty"`
	Risk         string       `json:"risk,omitempty"`
	Verification Verification `json:"verification,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type Requirements struct {
	Tools    []string `json:"tools,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Python   []string `json:"python,omitempty"`
	Env      []string `json:"env,omitempty"`
}

type Verification struct {
	Command      string    `json:"command,omitempty"`
	SampleTask   string    `json:"sample_task,omitempty"`
	LastPassedAt time.Time `json:"last_passed_at,omitempty"`
}

type Gap struct {
	ID                string    `json:"id"`
	Task              string    `json:"task"`
	MissingCapability string    `json:"missing_capability"`
	Source            string    `json:"source"`
	Status            string    `json:"status"`
	Evidence          []string  `json:"evidence,omitempty"`
	SuggestedActions  []string  `json:"suggested_actions,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Proposal struct {
	ID           string       `json:"id"`
	GapID        string       `json:"gap_id,omitempty"`
	Summary      string       `json:"summary"`
	InstallScope string       `json:"install_scope"`
	Dependencies Requirements `json:"dependencies,omitempty"`
	Artifacts    []string     `json:"artifacts,omitempty"`
	Risk         string       `json:"risk"`
	Verification Verification `json:"verification,omitempty"`
	Status       string       `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}
