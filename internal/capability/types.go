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
	NPM      []string `json:"npm,omitempty"`
	Brew     []string `json:"brew,omitempty"`
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

type Suggestion struct {
	MissingCapability string    `json:"missing_capability"`
	Count             int       `json:"count"`
	Sources           []string  `json:"sources,omitempty"`
	ExampleTasks      []string  `json:"example_tasks,omitempty"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	NextCommand       string    `json:"next_command"`
	Reason            string    `json:"reason"`
}

type DoctorResult struct {
	Capability     Capability    `json:"capability"`
	Checks         []DoctorCheck `json:"checks"`
	ReadyToVerify  bool          `json:"ready_to_verify"`
	ReadyToPromote bool          `json:"ready_to_promote"`
	NextActions    []string      `json:"next_actions,omitempty"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type DependencyState struct {
	Version   int                 `json:"version"`
	UpdatedAt time.Time           `json:"updated_at"`
	Plans     []DependencyPlan    `json:"plans"`
	Installs  []DependencyInstall `json:"installs"`
}

type DependencyPlan struct {
	ID           string             `json:"id"`
	ProposalID   string             `json:"proposal_id"`
	CapabilityID string             `json:"capability_id"`
	Status       string             `json:"status"`
	Scope        string             `json:"scope"`
	Risk         string             `json:"risk"`
	Actions      []DependencyAction `json:"actions"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	ApprovedAt   time.Time          `json:"approved_at,omitempty"`
	InstalledAt  time.Time          `json:"installed_at,omitempty"`
}

type DependencyAction struct {
	ID      string   `json:"id"`
	Manager string   `json:"manager"`
	Name    string   `json:"name"`
	Scope   string   `json:"scope"`
	Command []string `json:"command"`
	Risk    string   `json:"risk"`
}

type DependencyInstall struct {
	ID          string    `json:"id"`
	PlanID      string    `json:"plan_id"`
	ActionID    string    `json:"action_id"`
	Manager     string    `json:"manager"`
	Name        string    `json:"name"`
	Scope       string    `json:"scope"`
	Command     []string  `json:"command"`
	Status      string    `json:"status"`
	ExitCode    int       `json:"exit_code"`
	Output      string    `json:"output,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

type EnabledAdapterState struct {
	Version   int              `json:"version"`
	UpdatedAt time.Time        `json:"updated_at"`
	Adapters  []EnabledAdapter `json:"adapters"`
}

type EnabledAdapter struct {
	CapabilityID string    `json:"capability_id"`
	Type         string    `json:"type"`
	Entry        string    `json:"entry"`
	EnabledAt    time.Time `json:"enabled_at"`
}

type EnableAdapterResult struct {
	Capability Capability `json:"capability"`
	StatePath  string     `json:"state_path"`
	MCPImport  string     `json:"mcp_import,omitempty"`
	Enabled    bool       `json:"enabled"`
}
