package traceview

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

const (
	graphCanvasWidth = 1420
	graphNodeWidth   = 190
	graphNodeHeight  = 62
	graphRowHeight   = 88
)

type graphPage struct {
	Graph  Graph
	Width  int
	Height int
	Nodes  []graphRenderNode
	Edges  []graphRenderEdge
}

type graphRenderNode struct {
	GraphNode
	X          int
	Y          int
	CSSClass   string
	LabelText  string
	DetailText string
}

type graphRenderEdge struct {
	GraphEdge
	Path string
}

// WriteGraphHTML 生成不依赖外部脚本和网络资源的离线因果图。
func WriteGraphHTML(view RunView, path string) (Graph, error) {
	graph := view.CausalGraph()
	if strings.TrimSpace(path) == "" {
		return graph, fmt.Errorf("graph output path is required")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return graph, err
	}
	data, err := GraphHTML(view)
	if err != nil {
		return graph, err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return graph, err
	}
	return graph, nil
}

func GraphHTML(view RunView) ([]byte, error) {
	page := buildGraphPage(view.CausalGraph())
	tmpl, err := template.New("causal-graph").Funcs(template.FuncMap{
		"duration": formatGraphDuration,
	}).Parse(causalGraphHTML)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func buildGraphPage(graph Graph) graphPage {
	page := graphPage{
		Graph:  graph,
		Width:  graphCanvasWidth,
		Height: max(520, 120+len(graph.Nodes)*graphRowHeight),
	}
	positions := map[string][2]int{}
	for _, node := range graph.Nodes {
		x := graphColumnX(node.Kind)
		y := 65 + node.Order*graphRowHeight
		positions[node.ID] = [2]int{x, y}
		page.Nodes = append(page.Nodes, graphRenderNode{
			GraphNode:  node,
			X:          x,
			Y:          y,
			CSSClass:   graphNodeClass(node),
			LabelText:  truncateGraphText(node.Label, 28),
			DetailText: truncateGraphText(node.Detail, 42),
		})
	}
	for _, edge := range graph.Edges {
		from, fromOK := positions[edge.From]
		to, toOK := positions[edge.To]
		if !fromOK || !toOK {
			continue
		}
		startX := from[0] + graphNodeWidth
		startY := from[1] + graphNodeHeight/2
		endX := to[0]
		endY := to[1] + graphNodeHeight/2
		controlOffset := max(70, absInt(endX-startX)/2)
		controlOne := startX + controlOffset
		controlTwo := endX - controlOffset
		if endX < startX {
			controlOne = startX + 90
			controlTwo = endX + 90
		}
		page.Edges = append(page.Edges, graphRenderEdge{
			GraphEdge: edge,
			Path: fmt.Sprintf(
				"M %d %d C %d %d, %d %d, %d %d",
				startX, startY,
				controlOne, startY,
				controlTwo, endY,
				endX, endY,
			),
		})
	}
	return page
}

func graphColumnX(kind string) int {
	switch kind {
	case "run", "prompt", "turn":
		return 35
	case "context", "compact":
		return 265
	case "route":
		return 495
	case "llm":
		return 725
	case "permission", "decision":
		return 955
	case "tool":
		return 955
	case "artifact":
		return 1185
	default:
		return 495
	}
}

func graphNodeClass(node GraphNode) string {
	classes := []string{"node", "kind-" + node.Kind}
	if node.Critical {
		classes = append(classes, "critical")
	}
	if node.Severity == "error" || node.Severity == "warn" ||
		node.Kind == "tool" && node.Status != "" && node.Status != "success" && node.Status != "running" {
		classes = append(classes, "problem")
	}
	return strings.Join(classes, " ")
}

func truncateGraphText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func formatGraphDuration(ms int64) string {
	if ms <= 0 {
		return "0ms"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.3fs", float64(ms)/1000)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

const causalGraphHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Cohort Causal Trace · {{.Graph.RunID}}</title>
<style>
:root{--bg:#07101f;--panel:#0d192c;--line:#263a58;--text:#edf4ff;--muted:#8da2c2;--blue:#61a8ff;--cyan:#4de2d0;--amber:#ffc857;--red:#ff6b7a;--green:#58d68d}
*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 80% 0,#172d58 0,transparent 35%),var(--bg);color:var(--text);font:14px/1.45 ui-sans-serif,system-ui,-apple-system,sans-serif}.wrap{max-width:1540px;margin:auto;padding:26px}.eyebrow{color:var(--cyan);font-size:11px;font-weight:700;letter-spacing:.16em}h1{margin:5px 0 3px;font-size:29px}.sub{color:var(--muted)}.cards{display:grid;grid-template-columns:repeat(6,1fr);gap:10px;margin:20px 0}.card,.panel{background:linear-gradient(145deg,var(--panel),#0a1425);border:1px solid var(--line);border-radius:13px;padding:14px}.card .label{color:var(--muted);font-size:10px;letter-spacing:.1em;text-transform:uppercase}.card .value{font-size:21px;font-weight:750;margin-top:4px}.toolbar{display:flex;gap:8px;margin:12px 0}.toolbar button{background:#142642;color:var(--text);border:1px solid #31517a;border-radius:8px;padding:7px 11px;cursor:pointer}.toolbar button:hover{border-color:var(--blue)}.graph-shell{overflow:auto;max-height:72vh;border:1px solid var(--line);border-radius:14px;background:linear-gradient(180deg,#091426,#07101d)}svg{display:block}.edge{fill:none;stroke:#3c5577;stroke-width:1.5;opacity:.78;marker-end:url(#arrow)}.edge.dim{opacity:.08}.node{cursor:pointer;transition:opacity .15s}.node rect{fill:#10213a;stroke:#375679;stroke-width:1.2;rx:10}.node text.label{fill:var(--text);font-size:12px;font-weight:700}.node text.detail{fill:var(--muted);font-size:10px}.node text.meta{fill:var(--cyan);font-size:9px}.node.critical rect{stroke:var(--amber);stroke-width:2.3;filter:url(#glow)}.node.problem rect{stroke:var(--red)}.node.kind-llm rect{fill:#12284b}.node.kind-tool rect{fill:#112d2c}.node.kind-artifact rect{fill:#30261b}.node.kind-route rect{fill:#251f49}.node.selected rect{stroke:#fff;stroke-width:3}.node.dim{opacity:.16}.layout{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-top:14px}.panel h2{font-size:14px;margin:0 0 10px}.finding{display:grid;grid-template-columns:1fr auto;gap:8px;padding:8px 0;border-bottom:1px solid var(--line)}.finding:last-child{border-bottom:0}.finding .reason{color:var(--muted);font-size:12px}.bad{color:var(--red)}.legend{display:flex;flex-wrap:wrap;gap:12px;color:var(--muted);font-size:11px;margin:9px 0}.dot{width:9px;height:9px;border-radius:50%;display:inline-block;margin-right:5px}.empty{color:var(--muted)}@media(max-width:900px){.cards{grid-template-columns:repeat(2,1fr)}.layout{grid-template-columns:1fr}}
</style>
</head>
<body><main class="wrap">
<div class="eyebrow">COHORT AGENT FLIGHT RECORDER</div>
<h1>Causal Trace Graph</h1>
<div class="sub">session {{.Graph.SessionID}} · run {{.Graph.RunID}} · status {{.Graph.Status}}</div>
<section class="cards">
<div class="card"><div class="label">Duration</div><div class="value">{{duration .Graph.DurationMS}}</div></div>
<div class="card"><div class="label">Critical Path</div><div class="value">{{duration .Graph.CriticalPathMS}}</div></div>
<div class="card"><div class="label">Nodes / Edges</div><div class="value">{{.Graph.Summary.NodeCount}} / {{.Graph.Summary.EdgeCount}}</div></div>
<div class="card"><div class="label">LLM / Tools</div><div class="value">{{.Graph.Summary.LLMNodes}} / {{.Graph.Summary.ToolNodes}}</div></div>
<div class="card"><div class="label">Failed Tools</div><div class="value">{{.Graph.Summary.FailedTools}}</div></div>
<div class="card"><div class="label">File Changes</div><div class="value">{{.Graph.Summary.FileChanges}}</div></div>
</section>
<div class="toolbar"><button id="show-critical">只看关键路径</button><button id="reset">重置</button></div>
<div class="legend"><span><i class="dot" style="background:#ffc857"></i>关键路径</span><span><i class="dot" style="background:#61a8ff"></i>LLM</span><span><i class="dot" style="background:#4de2d0"></i>Tool</span><span><i class="dot" style="background:#ff6b7a"></i>异常</span><span>点击节点查看一跳因果邻居</span></div>
<div class="graph-shell">
<svg width="{{.Width}}" height="{{.Height}}" viewBox="0 0 {{.Width}} {{.Height}}">
<defs><marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#58759c"/></marker><filter id="glow"><feGaussianBlur stdDeviation="2" result="blur"/><feMerge><feMergeNode in="blur"/><feMergeNode in="SourceGraphic"/></feMerge></filter></defs>
{{range .Edges}}<path class="edge" data-from="{{.From}}" data-to="{{.To}}" d="{{.Path}}"><title>{{.Relation}}</title></path>{{end}}
{{range .Nodes}}<g class="{{.CSSClass}}" data-id="{{.ID}}" data-critical="{{.Critical}}" transform="translate({{.X}},{{.Y}})">
<rect width="190" height="62"></rect>
<text class="label" x="11" y="19">{{.LabelText}}</text>
<text class="detail" x="11" y="37">{{.DetailText}}</text>
<text class="meta" x="11" y="53">turn {{.Turn}} · {{.Kind}} · {{duration .DurationMS}}</text>
<title>{{.Label}}&#10;{{.Detail}}&#10;status={{.Status}} duration={{duration .DurationMS}}</title>
</g>{{end}}
</svg>
</div>
<section class="layout">
<div class="panel"><h2>Latency Bottlenecks</h2>{{if .Graph.Bottlenecks}}{{range .Graph.Bottlenecks}}<div class="finding"><div><b>{{.Label}}</b><div class="reason">{{.Kind}} · {{.Reason}}</div></div><div>{{duration .DurationMS}}</div></div>{{end}}{{else}}<div class="empty">No timed bottlenecks.</div>{{end}}</div>
<div class="panel"><h2>Anomalies</h2>{{if .Graph.Anomalies}}{{range .Graph.Anomalies}}<div class="finding"><div><b class="bad">{{.Label}}</b><div class="reason">{{.Kind}} · {{.Reason}}</div></div><div>{{duration .DurationMS}}</div></div>{{end}}{{else}}<div class="empty">No anomalies detected.</div>{{end}}</div>
</section>
</main>
<script>
const nodes=[...document.querySelectorAll('.node')],edges=[...document.querySelectorAll('.edge')];
function reset(){nodes.forEach(n=>n.classList.remove('dim','selected'));edges.forEach(e=>e.classList.remove('dim'))}
function focus(id){reset();const related=new Set([id]);edges.forEach(e=>{if(e.dataset.from===id||e.dataset.to===id){related.add(e.dataset.from);related.add(e.dataset.to)}});nodes.forEach(n=>{if(!related.has(n.dataset.id))n.classList.add('dim');if(n.dataset.id===id)n.classList.add('selected')});edges.forEach(e=>{if(e.dataset.from!==id&&e.dataset.to!==id)e.classList.add('dim')})}
nodes.forEach(n=>n.addEventListener('click',()=>focus(n.dataset.id)));
document.getElementById('reset').onclick=reset;
document.getElementById('show-critical').onclick=()=>{reset();const ids=new Set(nodes.filter(n=>n.dataset.critical==='true').map(n=>n.dataset.id));nodes.forEach(n=>{if(!ids.has(n.dataset.id))n.classList.add('dim')});edges.forEach(e=>{if(!ids.has(e.dataset.from)||!ids.has(e.dataset.to))e.classList.add('dim')})};
</script></body></html>`
