package traceview

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cohort/internal/observability"
)

type Graph struct {
	SessionID      string         `json:"session_id"`
	RunID          string         `json:"run_id"`
	Status         string         `json:"status"`
	DurationMS     int64          `json:"duration_ms"`
	Nodes          []GraphNode    `json:"nodes"`
	Edges          []GraphEdge    `json:"edges"`
	CriticalPath   []string       `json:"critical_path"`
	CriticalPathMS int64          `json:"critical_path_ms"`
	Bottlenecks    []GraphFinding `json:"bottlenecks,omitempty"`
	Anomalies      []GraphFinding `json:"anomalies,omitempty"`
	Summary        GraphSummary   `json:"summary"`
}

type GraphNode struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Label      string    `json:"label"`
	Detail     string    `json:"detail,omitempty"`
	Turn       int       `json:"turn,omitempty"`
	Status     string    `json:"status,omitempty"`
	Severity   string    `json:"severity,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Order      int       `json:"order"`
	Critical   bool      `json:"critical,omitempty"`
}

type GraphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

type GraphFinding struct {
	NodeID     string `json:"node_id"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Reason     string `json:"reason"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type GraphSummary struct {
	NodeCount        int `json:"node_count"`
	EdgeCount        int `json:"edge_count"`
	LLMNodes         int `json:"llm_nodes"`
	ToolNodes        int `json:"tool_nodes"`
	FailedTools      int `json:"failed_tools"`
	FileChanges      int `json:"file_changes"`
	RouteEscalations int `json:"route_escalations"`
}

type graphBuilder struct {
	graph        Graph
	nodeIndex    map[string]int
	edgeSet      map[string]bool
	lastSequence string
	llmNodes     map[int]string
	toolNodes    map[string]string
	kindCounts   map[string]int
}

// CausalGraph 从脱敏后的运行事件重建因果 DAG，不读取 prompt 或工具结果正文。
func (v RunView) CausalGraph() Graph {
	events := append([]observability.Event(nil), v.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time.Before(events[j].Time)
	})
	builder := &graphBuilder{
		graph: Graph{
			SessionID: v.SessionID,
			RunID:     v.RunID,
			Status:    "incomplete",
		},
		nodeIndex:  map[string]int{},
		edgeSet:    map[string]bool{},
		llmNodes:   map[int]string{},
		toolNodes:  map[string]string{},
		kindCounts: map[string]int{},
	}
	builder.addNode(GraphNode{
		ID:        "run",
		Kind:      "run",
		Label:     "Agent Run",
		Status:    "running",
		StartedAt: firstEventTime(events),
	}, false)
	builder.lastSequence = "run"
	for _, event := range events {
		builder.consume(event)
	}
	builder.finish()
	return builder.graph
}

func (b *graphBuilder) consume(event observability.Event) {
	switch event.EventType {
	case observability.EventRunStarted:
		b.updateNode("run", func(node *GraphNode) {
			node.StartedAt = event.Time
			node.Status = "running"
		})
	case observability.EventUserPromptSubmitted:
		b.addSequential(GraphNode{
			ID:        b.nextID("prompt"),
			Kind:      "prompt",
			Label:     "User Task",
			Detail:    fmt.Sprintf("%d chars", graphInt(event.Data, "chars")),
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}, "triggers")
	case observability.EventTurnStarted:
		id := fmt.Sprintf("turn-%d", event.Turn)
		if _, exists := b.nodeIndex[id]; !exists {
			b.addSequential(GraphNode{
				ID:        id,
				Kind:      "turn",
				Label:     fmt.Sprintf("Turn %d", event.Turn),
				Turn:      event.Turn,
				StartedAt: event.Time,
			}, "next")
		}
	case observability.EventContextBuilt:
		b.addSequential(GraphNode{
			ID:    b.nextID(fmt.Sprintf("context-%d", event.Turn)),
			Kind:  "context",
			Label: "Context Build",
			Detail: fmt.Sprintf(
				"%d messages · %d tokens · %d chars",
				graphInt(event.Data, "final_messages"),
				graphInt(event.Data, "final_tokens"),
				graphInt(event.Data, "final_chars"),
			),
			Turn:      event.Turn,
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}, "builds")
	case observability.EventToolRouteSelected:
		mode := graphString(event.Data, "mode")
		detail := fmt.Sprintf(
			"%s · %d/%d tools · saved %dB",
			graphStringDefault(event.Data, "reason", "route"),
			graphInt(event.Data, "selected_count"),
			graphInt(event.Data, "full_schema_count"),
			graphInt(event.Data, "saved_schema_bytes"),
		)
		node := GraphNode{
			ID:        b.nextID(fmt.Sprintf("route-%d", event.Turn)),
			Kind:      "route",
			Label:     "Tool Route: " + graphStringDefault(event.Data, "mode", "unknown"),
			Detail:    detail,
			Turn:      event.Turn,
			Status:    mode,
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}
		b.addSequential(node, "routes")
		if mode == "escalating" || graphBool(event.Data, "escalated") {
			b.graph.Summary.RouteEscalations++
			b.addAnomaly(node, "tool surface escalated")
		}
	case observability.EventLLMRequestStarted:
		id := fmt.Sprintf("llm-%d", event.Turn)
		node := GraphNode{
			ID:        id,
			Kind:      "llm",
			Label:     fmt.Sprintf("LLM Turn %d", event.Turn),
			Detail:    fmt.Sprintf("%d messages · %d tools", graphInt(event.Data, "message_count"), graphInt(event.Data, "tool_schema_count")),
			Turn:      event.Turn,
			Status:    "running",
			StartedAt: event.Time,
		}
		b.addSequential(node, "requests")
		b.llmNodes[event.Turn] = id
	case observability.EventLLMResponseFinished:
		id := b.llmNodes[event.Turn]
		if id == "" {
			id = fmt.Sprintf("llm-%d", event.Turn)
			b.addSequential(GraphNode{ID: id, Kind: "llm", Label: fmt.Sprintf("LLM Turn %d", event.Turn), Turn: event.Turn, StartedAt: event.Time}, "requests")
			b.llmNodes[event.Turn] = id
		}
		b.updateNode(id, func(node *GraphNode) {
			node.Status = graphStringDefault(event.Data, "status", string(event.Severity))
			node.DurationMS = graphInt(event.Data, "duration_ms")
			node.Detail = fmt.Sprintf("%d tool calls · %d chars", graphInt(event.Data, "tool_call_count"), graphInt(event.Data, "content_chars"))
			node.Severity = string(event.Severity)
		})
		node := b.graph.Nodes[b.nodeIndex[id]]
		if node.Status != "success" || event.Severity == observability.SeverityError {
			b.addAnomaly(node, "LLM request failed")
		}
	case observability.EventToolStarted:
		callID := graphToolCallID(event)
		id := "tool-" + callID
		if _, exists := b.nodeIndex[id]; exists {
			id = fmt.Sprintf("tool-%d-%s", event.Turn, callID)
		}
		node := GraphNode{
			ID:        id,
			Kind:      "tool",
			Label:     graphStringDefault(event.Data, "tool", "unknown tool"),
			Detail:    "call " + callID,
			Turn:      event.Turn,
			Status:    "running",
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}
		b.addSequential(node, "calls")
		b.toolNodes[callID] = id
	case observability.EventPermissionDecision:
		callID := graphToolCallID(event)
		node := GraphNode{
			ID:        b.nextID("permission"),
			Kind:      "permission",
			Label:     "Permission: " + graphStringDefault(event.Data, "permission_decision", "decision"),
			Detail:    graphString(event.Data, "tool"),
			Turn:      event.Turn,
			Status:    graphString(event.Data, "permission_decision"),
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}
		b.addSequential(node, "gates")
		if toolID := b.resolveToolNode(callID, event); toolID != "" {
			b.addEdge(toolID, node.ID, "permission")
		}
	case observability.EventToolFinished:
		callID := graphToolCallID(event)
		id := b.resolveToolNode(callID, event)
		if id == "" {
			id = "tool-" + callID
			b.addSequential(GraphNode{ID: id, Kind: "tool", Label: graphStringDefault(event.Data, "tool", "unknown tool"), Turn: event.Turn, StartedAt: event.Time}, "calls")
			b.toolNodes[callID] = id
		}
		b.updateNode(id, func(node *GraphNode) {
			node.Status = graphStringDefault(event.Data, "status", "unknown")
			node.DurationMS = graphInt(event.Data, "duration_ms")
			node.Severity = string(event.Severity)
			if code := graphString(event.Data, "error_code"); code != "" {
				node.Detail = "error: " + code
			} else {
				node.Detail = fmt.Sprintf("%d result chars", graphInt(event.Data, "result_chars"))
			}
		})
		node := b.graph.Nodes[b.nodeIndex[id]]
		if node.Status != "success" && !expectedControlErrorCode(graphString(event.Data, "error_code")) {
			b.graph.Summary.FailedTools++
			b.addAnomaly(node, "tool execution failed")
		}
	case observability.EventFileChanged:
		callID := graphToolCallID(event)
		path := graphString(event.Data, "path")
		node := GraphNode{
			ID:        b.nextID("artifact"),
			Kind:      "artifact",
			Label:     "File: " + filepath.Base(path),
			Detail:    path,
			Turn:      event.Turn,
			Status:    "changed",
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}
		previous := b.lastSequence
		b.addNode(node, true)
		if toolID := b.resolveToolNode(callID, event); toolID != "" {
			b.addEdge(toolID, node.ID, "writes")
		}
		if previous != "" && previous != node.ID {
			b.addEdge(previous, node.ID, "changes")
		}
		b.lastSequence = node.ID
	case observability.EventFinishGuardTriggered:
		b.addDecision(event, "Finish Guard", graphString(event.Data, "reason"))
	case observability.EventCapabilityGapRecorded:
		b.addDecision(event, "Capability Gap", graphString(event.Data, "status"))
	case observability.EventTextToolUseParsed:
		b.addDecision(event, "Text Tool Parse", graphString(event.Data, "status"))
	case observability.EventCompactStarted:
		b.addSequential(GraphNode{
			ID:        b.nextID("compact"),
			Kind:      "compact",
			Label:     "Context Compact",
			Turn:      event.Turn,
			Status:    "running",
			StartedAt: event.Time,
			Severity:  string(event.Severity),
		}, "compacts")
	case observability.EventCompactFinished:
		b.finishLatestKind("compact", event)
	case observability.EventRunFinished:
		b.graph.Status = graphStringDefault(event.Data, "status", "unknown")
		b.graph.DurationMS = graphInt(event.Data, "duration_ms")
		b.updateNode("run", func(node *GraphNode) {
			node.Status = b.graph.Status
			node.DurationMS = b.graph.DurationMS
			node.Severity = string(event.Severity)
		})
	}
}

func (b *graphBuilder) addDecision(event observability.Event, label string, detail string) {
	node := GraphNode{
		ID:        b.nextID("decision"),
		Kind:      "decision",
		Label:     label,
		Detail:    detail,
		Turn:      event.Turn,
		Status:    graphString(event.Data, "status"),
		StartedAt: event.Time,
		Severity:  string(event.Severity),
	}
	b.addSequential(node, "decides")
	if event.Severity == observability.SeverityWarn || event.Severity == observability.SeverityError {
		b.addAnomaly(node, strings.ToLower(label))
	}
}

func (b *graphBuilder) addSequential(node GraphNode, relation string) {
	previous := b.lastSequence
	b.addNode(node, true)
	if previous != "" && previous != node.ID {
		b.addEdge(previous, node.ID, relation)
	}
	b.lastSequence = node.ID
}

func (b *graphBuilder) addNode(node GraphNode, count bool) {
	if _, exists := b.nodeIndex[node.ID]; exists {
		return
	}
	node.Order = len(b.graph.Nodes)
	b.nodeIndex[node.ID] = len(b.graph.Nodes)
	b.graph.Nodes = append(b.graph.Nodes, node)
	if count {
		switch node.Kind {
		case "llm":
			b.graph.Summary.LLMNodes++
		case "tool":
			b.graph.Summary.ToolNodes++
		case "artifact":
			b.graph.Summary.FileChanges++
		}
	}
}

func (b *graphBuilder) updateNode(id string, update func(*GraphNode)) {
	index, exists := b.nodeIndex[id]
	if !exists {
		return
	}
	update(&b.graph.Nodes[index])
}

func (b *graphBuilder) addEdge(from string, to string, relation string) {
	if from == "" || to == "" || from == to {
		return
	}
	key := from + "\x00" + to
	if b.edgeSet[key] {
		return
	}
	b.edgeSet[key] = true
	b.graph.Edges = append(b.graph.Edges, GraphEdge{From: from, To: to, Relation: relation})
}

func (b *graphBuilder) nextID(kind string) string {
	b.kindCounts[kind]++
	return fmt.Sprintf("%s-%d", kind, b.kindCounts[kind])
}

func (b *graphBuilder) resolveToolNode(callID string, event observability.Event) string {
	if id := b.toolNodes[callID]; id != "" {
		return id
	}
	tool := graphString(event.Data, "tool")
	for index := len(b.graph.Nodes) - 1; index >= 0; index-- {
		node := b.graph.Nodes[index]
		if node.Kind == "tool" && node.Turn == event.Turn && (tool == "" || node.Label == tool) {
			return node.ID
		}
	}
	return ""
}

func (b *graphBuilder) finishLatestKind(kind string, event observability.Event) {
	for index := len(b.graph.Nodes) - 1; index >= 0; index-- {
		if b.graph.Nodes[index].Kind != kind || b.graph.Nodes[index].Status != "running" {
			continue
		}
		b.graph.Nodes[index].Status = graphStringDefault(event.Data, "status", "success")
		b.graph.Nodes[index].DurationMS = graphInt(event.Data, "duration_ms")
		if b.graph.Nodes[index].DurationMS == 0 && !b.graph.Nodes[index].StartedAt.IsZero() {
			b.graph.Nodes[index].DurationMS = max(0, event.Time.Sub(b.graph.Nodes[index].StartedAt).Milliseconds())
		}
		b.graph.Nodes[index].Severity = string(event.Severity)
		return
	}
}

func (b *graphBuilder) addAnomaly(node GraphNode, reason string) {
	b.graph.Anomalies = append(b.graph.Anomalies, GraphFinding{
		NodeID:     node.ID,
		Kind:       node.Kind,
		Label:      node.Label,
		Reason:     reason,
		DurationMS: node.DurationMS,
	})
}

func (b *graphBuilder) finish() {
	for _, node := range b.graph.Nodes {
		if node.Status == "running" && (node.Kind == "llm" || node.Kind == "tool" || node.Kind == "compact") {
			b.addAnomaly(node, "operation did not emit a completion event")
		}
	}
	b.graph.Summary.NodeCount = len(b.graph.Nodes)
	b.graph.Summary.EdgeCount = len(b.graph.Edges)
	if b.graph.DurationMS == 0 && len(b.graph.Nodes) > 1 {
		startedAt := b.graph.Nodes[0].StartedAt
		finishedAt := b.graph.Nodes[len(b.graph.Nodes)-1].StartedAt
		if !startedAt.IsZero() && finishedAt.After(startedAt) {
			b.graph.DurationMS = finishedAt.Sub(startedAt).Milliseconds()
			b.updateNode("run", func(node *GraphNode) {
				node.DurationMS = b.graph.DurationMS
			})
		}
	}
	b.computeCriticalPath()
	b.computeBottlenecks()
}

func (b *graphBuilder) computeCriticalPath() {
	incoming := map[string][]string{}
	for _, edge := range b.graph.Edges {
		incoming[edge.To] = append(incoming[edge.To], edge.From)
	}
	distance := map[string]int64{}
	previous := map[string]string{}
	var end string
	var best int64
	for _, node := range b.graph.Nodes {
		weight := graphNodeWeight(node)
		distance[node.ID] = weight
		for _, source := range incoming[node.ID] {
			candidate := distance[source] + weight
			if candidate > distance[node.ID] {
				distance[node.ID] = candidate
				previous[node.ID] = source
			}
		}
		if distance[node.ID] >= best {
			best = distance[node.ID]
			end = node.ID
		}
	}
	var reversed []string
	for end != "" {
		reversed = append(reversed, end)
		end = previous[end]
	}
	for index := len(reversed) - 1; index >= 0; index-- {
		b.graph.CriticalPath = append(b.graph.CriticalPath, reversed[index])
	}
	b.graph.CriticalPathMS = best
	critical := map[string]bool{}
	for _, id := range b.graph.CriticalPath {
		critical[id] = true
	}
	for index := range b.graph.Nodes {
		b.graph.Nodes[index].Critical = critical[b.graph.Nodes[index].ID]
	}
}

func (b *graphBuilder) computeBottlenecks() {
	nodes := append([]GraphNode(nil), b.graph.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].DurationMS == nodes[j].DurationMS {
			return nodes[i].Order < nodes[j].Order
		}
		return nodes[i].DurationMS > nodes[j].DurationMS
	})
	for _, node := range nodes {
		if graphNodeWeight(node) <= 0 {
			continue
		}
		b.graph.Bottlenecks = append(b.graph.Bottlenecks, GraphFinding{
			NodeID:     node.ID,
			Kind:       node.Kind,
			Label:      node.Label,
			Reason:     "critical-path latency",
			DurationMS: node.DurationMS,
		})
		if len(b.graph.Bottlenecks) == 5 {
			break
		}
	}
}

func graphNodeWeight(node GraphNode) int64 {
	switch node.Kind {
	case "llm", "tool", "compact":
		return max(0, node.DurationMS)
	default:
		return 0
	}
}

func graphToolCallID(event observability.Event) string {
	if id := strings.TrimSpace(graphString(event.Data, "tool_call_id")); id != "" {
		return sanitizeGraphID(id)
	}
	return sanitizeGraphID(fmt.Sprintf("%d-%s-%d", event.Turn, graphStringDefault(event.Data, "tool", "tool"), graphInt(event.Data, "index")))
}

func sanitizeGraphID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_':
			b.WriteRune(char)
		default:
			b.WriteByte('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "unknown"
	}
	return result
}

func firstEventTime(events []observability.Event) time.Time {
	if len(events) == 0 {
		return time.Time{}
	}
	return events[0].Time
}

func graphString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}

func graphStringDefault(data map[string]any, key string, fallback string) string {
	if value := graphString(data, key); value != "" {
		return value
	}
	return fallback
}

func graphInt(data map[string]any, key string) int64 {
	return intValue(data, key)
}

func graphBool(data map[string]any, key string) bool {
	return boolValue(data, key)
}
