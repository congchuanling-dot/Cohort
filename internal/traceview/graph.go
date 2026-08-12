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
	ID         string               `json:"id"`
	Kind       string               `json:"kind"`
	Label      string               `json:"label"`
	Detail     string               `json:"detail,omitempty"`
	Turn       int                  `json:"turn,omitempty"`
	Status     string               `json:"status,omitempty"`
	Severity   string               `json:"severity,omitempty"`
	StartedAt  time.Time            `json:"started_at,omitempty"`
	DurationMS int64                `json:"duration_ms,omitempty"`
	Order      int                  `json:"order"`
	Critical   bool                 `json:"critical,omitempty"`
	Execution  GraphExecutionDetail `json:"execution"`
}

type GraphExecutionDetail struct {
	What              string            `json:"what,omitempty"`
	How               string            `json:"how,omitempty"`
	InputSummary      string            `json:"input_summary,omitempty"`
	ParametersSummary string            `json:"parameters_summary,omitempty"`
	ParametersHash    string            `json:"parameters_hash,omitempty"`
	OutputSummary     string            `json:"output_summary,omitempty"`
	TokenUsage        *GraphTokenUsage  `json:"token_usage,omitempty"`
	Permission        *GraphPermission  `json:"permission,omitempty"`
	Evidence          []GraphEvidence   `json:"evidence,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
}

type GraphTokenUsage struct {
	Source    string `json:"source"`
	Input     int64  `json:"input,omitempty"`
	Output    int64  `json:"output,omitempty"`
	Total     int64  `json:"total,omitempty"`
	CacheRead int64  `json:"cache_read,omitempty"`
	Estimated int64  `json:"estimated_input,omitempty"`
}

type GraphPermission struct {
	Decision string `json:"decision"`
	Risk     string `json:"risk,omitempty"`
	External bool   `json:"external,omitempty"`
	Server   string `json:"server,omitempty"`
}

type GraphEvidence struct {
	Type  string `json:"type"`
	Ref   string `json:"ref,omitempty"`
	Label string `json:"label"`
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
	contexts     map[int]observability.Event
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
		contexts:   map[int]observability.Event{},
	}
	builder.addNode(GraphNode{
		ID:        "run",
		Kind:      "run",
		Label:     "Agent Run",
		Status:    "running",
		StartedAt: firstEventTime(events),
		Execution: GraphExecutionDetail{
			What: "执行一次 Agent 任务直到完成、失败或被治理策略中止。",
			How:  "按事件证据重建运行主链，不读取原始 Prompt 或工具结果正文。",
		},
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
			Execution: GraphExecutionDetail{
				What:         "接收用户任务并启动 Agent Run。",
				How:          "只记录字符数和不可逆 hash，任务正文不进入控制面。",
				InputSummary: fmt.Sprintf("%d chars", graphInt(event.Data, "chars")),
				Evidence:     eventEvidence(event, "user task submitted"),
			},
		}, "triggers")
	case observability.EventTurnStarted:
		// Turn 是 LLM/Tool 节点属性，不再作为主链中的空操作节点。
	case observability.EventContextBuilt:
		b.contexts[event.Turn] = event
		if contextChanged(event.Data) {
			b.addSequential(contextGraphNode(b.nextID(fmt.Sprintf("context-%d", event.Turn)), event), "governs")
		}
	case observability.EventToolRouteSelected:
		mode := graphString(event.Data, "mode")
		if mode != "adaptive" && mode != "escalating" && !graphBool(event.Data, "escalated") {
			return
		}
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
			Execution: GraphExecutionDetail{
				What:         "选择本轮可见工具 Schema。",
				How:          graphStringDefault(event.Data, "reason", "runtime route policy"),
				InputSummary: fmt.Sprintf("%d registered tools", graphInt(event.Data, "full_schema_count")),
				OutputSummary: fmt.Sprintf("%d selected, %d schema bytes saved",
					graphInt(event.Data, "selected_count"), graphInt(event.Data, "saved_schema_bytes")),
				Evidence: eventEvidence(event, "tool route decision"),
			},
		}
		b.addSequential(node, "routes")
		if mode == "escalating" || graphBool(event.Data, "escalated") {
			b.graph.Summary.RouteEscalations++
			b.addAnomaly(node, "tool surface escalated")
		}
	case observability.EventLLMRequestStarted:
		id := fmt.Sprintf("llm-%d", event.Turn)
		contextEvent := b.contexts[event.Turn]
		execution := llmRequestExecution(event, contextEvent)
		node := GraphNode{
			ID:        id,
			Kind:      "llm",
			Label:     fmt.Sprintf("LLM Turn %d", event.Turn),
			Detail:    fmt.Sprintf("%d messages · %d tools", graphInt(event.Data, "message_count"), graphInt(event.Data, "tool_schema_count")),
			Turn:      event.Turn,
			Status:    "running",
			StartedAt: event.Time,
			Execution: execution,
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
			applyLLMResponseExecution(&node.Execution, event)
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
			Execution: GraphExecutionDetail{
				What:              "调用工具 " + graphStringDefault(event.Data, "tool", "unknown tool") + "。",
				How:               "由工具 Registry 按名称分发，在当前 Run 上下文中执行。",
				ParametersSummary: graphStringDefault(event.Data, "args_summary", graphString(event.Data, "arguments_summary")),
				ParametersHash:    graphString(event.Data, "args_hash"),
				Evidence:          eventEvidence(event, "tool invocation"),
			},
		}
		b.addSequential(node, "calls")
		b.toolNodes[callID] = id
	case observability.EventPermissionDecision:
		callID := graphToolCallID(event)
		if toolID := b.resolveToolNode(callID, event); toolID != "" {
			b.updateNode(toolID, func(node *GraphNode) {
				node.Execution.Permission = &GraphPermission{
					Decision: graphStringDefault(event.Data, "permission_decision", "unknown"),
					Risk:     graphString(event.Data, "risk"),
					External: graphBool(event.Data, "external"),
					Server:   graphString(event.Data, "server"),
				}
				node.Execution.Evidence = append(node.Execution.Evidence, eventEvidence(event, "permission decision")...)
			})
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
				node.Execution.OutputSummary = "执行失败，error_code=" + code
			} else {
				node.Detail = fmt.Sprintf("%d result chars", graphInt(event.Data, "result_chars"))
				node.Execution.OutputSummary = fmt.Sprintf(
					"status=%s, result=%d chars, truncated=%t",
					node.Status, graphInt(event.Data, "result_chars"), graphBool(event.Data, "truncated"),
				)
			}
			if node.Execution.Permission == nil && graphString(event.Data, "permission_decision") != "" {
				node.Execution.Permission = &GraphPermission{
					Decision: graphString(event.Data, "permission_decision"),
					Risk:     graphString(event.Data, "risk"),
					External: graphBool(event.Data, "external"),
					Server:   graphString(event.Data, "server"),
				}
			}
			node.Execution.Evidence = append(node.Execution.Evidence, eventEvidence(event, "tool result")...)
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
			Execution: GraphExecutionDetail{
				What:          "记录工具产生的文件变更。",
				How:           "成功执行文件变更工具后，从受控参数中提取路径并建立 Artifact 证据。",
				InputSummary:  graphStringDefault(event.Data, "tool", "unknown tool"),
				OutputSummary: path,
				Evidence:      eventEvidence(event, "file changed"),
			},
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
	case observability.EventGovernanceIntervention:
		b.addDecision(
			event,
			"Governance: "+graphStringDefault(event.Data, "action", "intervention"),
			graphString(event.Data, "reason"),
		)
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
			Execution: GraphExecutionDetail{
				What:     "生成上下文压缩摘要。",
				How:      graphStringDefault(event.Data, "kind", "context compact"),
				Evidence: eventEvidence(event, "compact started"),
			},
		}, "compacts")
	case observability.EventCompactFinished:
		b.finishLatestKind("compact", event)
		for index := len(b.graph.Nodes) - 1; index >= 0; index-- {
			if b.graph.Nodes[index].Kind != "compact" {
				continue
			}
			b.graph.Nodes[index].Execution.OutputSummary = fmt.Sprintf(
				"status=%s, %d chars", graphStringDefault(event.Data, "status", "success"), graphInt(event.Data, "chars"),
			)
			b.graph.Nodes[index].Execution.Evidence = append(
				b.graph.Nodes[index].Execution.Evidence, eventEvidence(event, "compact finished")...,
			)
			break
		}
	case observability.EventRunFinished:
		b.graph.Status = graphStringDefault(event.Data, "status", "unknown")
		b.graph.DurationMS = graphInt(event.Data, "duration_ms")
		b.updateNode("run", func(node *GraphNode) {
			node.Status = b.graph.Status
			node.DurationMS = b.graph.DurationMS
			node.Severity = string(event.Severity)
			node.Execution.OutputSummary = fmt.Sprintf(
				"status=%s, duration=%dms", b.graph.Status, b.graph.DurationMS,
			)
			node.Execution.Evidence = append(node.Execution.Evidence, eventEvidence(event, "run finished")...)
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
		Execution: GraphExecutionDetail{
			What:         label,
			How:          "运行时根据当前证据执行确定性决策。",
			InputSummary: detail,
			Evidence:     eventEvidence(event, strings.ToLower(label)),
		},
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

func contextChanged(data map[string]any) bool {
	return intValue(data, "trimmed_messages") > 0 ||
		intValue(data, "compacted_tool_results") > 0 ||
		boolValue(data, "injected_session_memory") ||
		boolValue(data, "injected_relevant_memory") ||
		boolValue(data, "injected_compact_summary") ||
		firstString(data, "trigger_reason") != "below_compact_trigger_threshold"
}

func contextGraphNode(id string, event observability.Event) GraphNode {
	finalTokens := graphInt(event.Data, "final_tokens")
	usableTokens := graphInt(event.Data, "usable_input_tokens")
	attributes := contextAttributes(event.Data)
	return GraphNode{
		ID:    id,
		Kind:  "context",
		Label: "Context Governance",
		Detail: fmt.Sprintf(
			"%d messages · %d/%d tokens",
			graphInt(event.Data, "final_messages"), finalTokens, usableTokens,
		),
		Turn:      event.Turn,
		Status:    capacityState(ratioOf(finalTokens, usableTokens)),
		StartedAt: event.Time,
		Severity:  string(event.Severity),
		Execution: GraphExecutionDetail{
			What:          "改变本轮发送给模型的上下文。",
			How:           firstStringDefault(event.Data, "trigger_reason", "context manager"),
			InputSummary:  fmt.Sprintf("%d messages, estimated %d tokens", graphInt(event.Data, "original_messages"), graphInt(event.Data, "original_tokens")),
			OutputSummary: fmt.Sprintf("%d messages, estimated %d tokens", graphInt(event.Data, "final_messages"), finalTokens),
			TokenUsage: &GraphTokenUsage{
				Source:    "estimated",
				Estimated: finalTokens,
			},
			Attributes: attributes,
			Evidence:   eventEvidence(event, "context governance"),
		},
	}
}

func llmRequestExecution(event, contextEvent observability.Event) GraphExecutionDetail {
	detail := GraphExecutionDetail{
		What: "调用模型生成下一步回答或工具计划。",
		How:  "使用 OpenAI-compatible Chat Completions 请求；控制面仅展示结构化摘要。",
		InputSummary: fmt.Sprintf(
			"%d messages, %d tools, %d chars",
			graphInt(event.Data, "message_count"),
			graphInt(event.Data, "tool_schema_count"),
			graphInt(event.Data, "request_chars"),
		),
		Attributes: map[string]string{
			"tool_schema_count": fmt.Sprint(graphInt(event.Data, "tool_schema_count")),
			"request_chars":     fmt.Sprint(graphInt(event.Data, "request_chars")),
		},
		Evidence: eventEvidence(event, "LLM request"),
	}
	if contextEvent.EventType == observability.EventContextBuilt {
		estimated := graphInt(contextEvent.Data, "final_tokens")
		detail.TokenUsage = &GraphTokenUsage{Source: "estimated", Estimated: estimated}
		detail.Attributes["context_trigger"] = firstStringDefault(contextEvent.Data, "trigger_reason", "unknown")
		detail.Attributes["context_messages"] = fmt.Sprint(graphInt(contextEvent.Data, "final_messages"))
		detail.Attributes["context_window"] = fmt.Sprint(graphInt(contextEvent.Data, "context_window_tokens"))
		detail.Attributes["usable_input_tokens"] = fmt.Sprint(graphInt(contextEvent.Data, "usable_input_tokens"))
		detail.Attributes["trimmed_messages"] = fmt.Sprint(graphInt(contextEvent.Data, "trimmed_messages"))
		detail.Attributes["compacted_tool_results"] = fmt.Sprint(graphInt(contextEvent.Data, "compacted_tool_results"))
		detail.Evidence = append(detail.Evidence, eventEvidence(contextEvent, "context build attribute")...)
	}
	return detail
}

func applyLLMResponseExecution(detail *GraphExecutionDetail, event observability.Event) {
	if detail == nil {
		return
	}
	detail.OutputSummary = fmt.Sprintf(
		"status=%s, %d tool calls, %d response chars",
		graphStringDefault(event.Data, "status", "unknown"),
		graphInt(event.Data, "tool_call_count"),
		graphInt(event.Data, "content_chars"),
	)
	usage := valueMap(event.Data["usage"])
	if numericUsageAvailable(usage) {
		detail.TokenUsage = &GraphTokenUsage{
			Source:    UsageSourceProviderReported,
			Input:     intValue(usage, "input_tokens"),
			Output:    intValue(usage, "output_tokens"),
			Total:     intValue(usage, "total_tokens"),
			CacheRead: intValue(usage, "cache_read_input_tokens"),
		}
	} else if detail.TokenUsage == nil {
		detail.TokenUsage = &GraphTokenUsage{Source: UsageSourceUnavailable}
	}
	detail.Evidence = append(detail.Evidence, eventEvidence(event, "LLM provider response")...)
}

func eventEvidence(event observability.Event, label string) []GraphEvidence {
	evidence := []GraphEvidence{{
		Type:  "runtime_event",
		Ref:   event.EventID,
		Label: label,
	}}
	if event.Redaction.Applied {
		evidence = append(evidence, GraphEvidence{
			Type:  "redaction",
			Label: fmt.Sprintf("%d fields redacted", len(event.Redaction.Fields)),
		})
	}
	return evidence
}

func contextAttributes(data map[string]any) map[string]string {
	return map[string]string{
		"trimmed_messages":         fmt.Sprint(intValue(data, "trimmed_messages")),
		"compacted_tool_results":   fmt.Sprint(intValue(data, "compacted_tool_results")),
		"injected_session_memory":  fmt.Sprint(boolValue(data, "injected_session_memory")),
		"injected_relevant_memory": fmt.Sprint(boolValue(data, "injected_relevant_memory")),
		"injected_compact_summary": fmt.Sprint(boolValue(data, "injected_compact_summary")),
		"context_window_source":    firstStringDefault(data, "context_window_source", "legacy_event"),
		"capability_confidence":    firstStringDefault(data, "capability_confidence", "unknown"),
	}
}

func ratioOf(value, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(value) / float64(total)
}
