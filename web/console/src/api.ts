export type RiskLevel = "read" | "execute" | "confirm" | "danger";

export interface InputField {
  name: string;
  label: string;
  description?: string;
  type: "string" | "text" | "boolean" | "integer" | "select" | "path" | "secret" | "duration" | "entity";
  required?: boolean;
  default?: unknown;
  options?: string[];
  placeholder?: string;
  sensitive?: boolean;
  entity?: {
    kind: EntityKind;
    status?: string[];
    depends_on?: Record<string, string>;
    recent_first?: boolean;
    allow_missing?: boolean;
  };
}

export type EntityKind =
  | "session"
  | "run"
  | "replay_bundle"
  | "eval_run"
  | "delivery"
  | "hermes_action"
  | "skill"
  | "capability"
  | "capability_gap"
  | "capability_proposal"
  | "dependency_plan"
  | "reflection_job"
  | "mcp_server"
  | "mcp_tool"
  | "model_profile";

export interface EntityDescriptor {
  kind: EntityKind;
  id: string;
  title: string;
  subtitle?: string;
  status?: string;
  updated_at?: string;
  version: string;
  badges?: string[];
  relations?: Record<string, string>;
  actions?: Array<{ action_id: string; label: string; risk: RiskLevel; enabled: boolean; disabled_reason?: string }>;
}

export interface ActionPreparation {
  preparation_token: string;
  action_id: string;
  resolved_input: Record<string, unknown>;
  entities?: Array<{ field: string; kind: EntityKind; id: string; title: string; status?: string; version: string }>;
  impact: string;
  confirmation_text?: string;
  expires_at: string;
}

export interface ActionSpec {
  id: string;
  category: string;
  label: string;
  description: string;
  keywords?: string[];
  risk: RiskLevel;
  async?: boolean;
  confirmation_text?: string;
  inputs?: InputField[];
}

export interface Operation {
  id: string;
  action_id: string;
  project_root: string;
  actor: string;
  status: "pending" | "running" | "succeeded" | "failed" | "cancelled";
  input?: Record<string, unknown>;
  summary?: string;
  result?: unknown;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface SessionInfo {
  authenticated: boolean;
  csrf_token: string;
  project_root: string;
}

export interface DashboardSnapshot {
  generated_at: string;
  project: { root: string; name: string; branch: string; head: string; dirty: boolean };
  model: { profile: string; provider: string; model: string; api_key_present: boolean };
  counts: { sessions: number; deliveries: number; explorers: number; eval_runs: number };
  delivery: {
    active: number;
    verified: number;
    failed: number;
    latest?: { id: string; status: string; requirement: string; updated_at: string };
    by_status: Record<string, number>;
  };
  hermes: {
    running: boolean;
    open_actions: number;
    critical_actions: number;
    running_jobs: number;
    running_repairs: number;
    last_error?: string;
  };
  evaluation: { latest_run_id?: string; pass_rate: number; score: number; regressions: number };
  reflection?: { pending: number; running: number; dead: number };
}

export interface ProjectRecord {
  id: string;
  name: string;
  root: string;
  last_opened_at: string;
}

export interface DataSourceHealth {
  kind: string;
  label: string;
  state: "ready" | "empty" | "unavailable" | "error" | "stale";
  relative_path: string;
  count: number;
  updated_at?: string;
  scanned_at: string;
  error_code?: string;
  error?: string;
}

export interface DeliveryItem {
  id: string;
  status: string;
  requirement: string;
  base_commit: string;
  updated_at: string;
  error?: string;
}

export interface HermesResource {
  status?: { running: boolean; open_actions: number; critical_actions: number };
  actions: Array<{ id: string; status: string; severity: string; category: string; title: string; detail?: string }>;
  repairs: Array<{ id: string; status: string; summary?: string; last_error?: string }>;
  jobs: Array<{ id: string; enabled: boolean; suite: string; last_status?: string; next_run_at?: string }>;
  events: Array<{ id: string; time: string; type: string; severity?: string }>;
}

export interface EvalRun {
  run_id: string;
  suite_id: string;
  model?: string;
  pass_rate: number;
  score: number;
  total_cases: number;
  failed_cases: number;
  total_tokens?: number;
  finished_at: string;
}

export interface StabilityRun {
  run_id: string;
  suite_id: string;
  suite_name: string;
  profile?: string;
  model?: string;
  started_at: string;
  pass_rate: number;
  score: number;
  stability_rate: number;
  failed_cases: number;
  total_cases: number;
  duration_ms: number;
  total_tokens?: number;
}

export interface QualitySummary {
  summary: {
    runs: number; suites: number; cases: number;
    average_pass_rate: number; average_score: number; average_stability: number;
    flaky_cases: number; regressions: number; failure_signatures: number; action_items: number;
  };
  runs: StabilityRun[];
  suites: Array<{ suite_id: string; suite_name: string; runs: number; average_pass_rate: number; average_score: number; average_stability: number; regressions: number; flaky_cases: number }>;
}

export interface EvalCaseResult {
  case_id: string; name: string; tags?: string[]; passed: boolean; skipped?: boolean;
  score: number; status: string; error?: string; session_id?: string; trace_run_id?: string; trace_path?: string;
  duration_ms: number; turns: number; tools?: string[]; tool_failures: number; total_tokens?: number;
  attempts: number; passed_attempts: number; stability_rate: number;
  assertion_results: Array<{ kind: string; passed: boolean; expected?: string; actual?: string; detail?: string }>;
  action_items?: Array<{ id: string; severity: string; category: string; title: string; detail?: string; evidence?: string }>;
}

export interface EvalDashboard {
  result: EvalRun & {
    suite_name: string; profile?: string; duration_ms: number; passed_cases: number; skipped_cases?: number;
    input_tokens?: number; output_tokens?: number; cases: EvalCaseResult[];
    gate?: { passed: boolean; violations?: string[] };
  };
  history: EvalRun[];
  tags: Array<{ tag: string; total: number; passed: number; pass_rate: number }>;
  average_stability: number;
  generated_at: string;
}

export interface StabilityIndex {
  generated_at: string; window: number;
  summary: QualitySummary["summary"];
  runs: StabilityRun[];
  suites: QualitySummary["suites"];
  cases: Array<{ suite_id: string; case_id: string; name: string; model?: string; pass_rate: number; average_score: number; average_stability: number; flaky: boolean; regressions: number; latest_run_id: string; latest_passed: boolean; latest_trace_run_id?: string }>;
  failure_signatures: Array<{ signature: string; kind: string; count: number; case_ids?: string[]; example?: string }>;
  regressions: Array<{ suite_id: string; case_id: string; from_run_id: string; to_run_id: string; to_started_at: string }>;
  action_items?: Array<{ id: string; severity: string; category: string; title: string; detail?: string; evidence?: string; trace_run_id?: string }>;
}

export interface TraceGraph {
  session_id: string; run_id: string; status: string; duration_ms: number;
  nodes: Array<{
    id: string; kind: string; label: string; detail?: string; turn?: number; status?: string; severity?: string;
    started_at?: string; duration_ms?: number; order: number; critical?: boolean;
    execution: {
      what?: string; how?: string; input_summary?: string; parameters_summary?: string; parameters_hash?: string;
      output_summary?: string;
      token_usage?: { source: string; input?: number; output?: number; total?: number; cache_read?: number; estimated_input?: number };
      permission?: { decision: string; risk?: string; external?: boolean; server?: string };
      evidence?: Array<{ type: string; ref?: string; label: string }>;
      attributes?: Record<string, string>;
    };
  }>;
  edges: Array<{ from: string; to: string; relation: string }>;
  critical_path: string[]; critical_path_ms: number;
  bottlenecks?: Array<{ node_id: string; kind: string; label: string; reason: string; duration_ms?: number }>;
  anomalies?: Array<{ node_id: string; kind: string; label: string; reason: string; duration_ms?: number }>;
  summary: { node_count: number; edge_count: number; llm_nodes: number; tool_nodes: number; failed_tools: number; file_changes: number; route_escalations: number };
}

export interface ReceiptLedger {
  usage_source: string; input_tokens: number; output_tokens: number; total_tokens: number;
  cache_read_tokens: number; cache_write_tokens: number; estimated_cost_usd?: number;
  cost_pricing_source: string; provider_turns: number; unavailable_turns: number;
  receipts: Array<{
    turn: number; status: string; duration_ms: number; usage_source: string; input_tokens?: number; output_tokens?: number;
    total_tokens?: number; cache_read_tokens?: number; estimated_input_tokens?: number; estimate_source?: string;
    request_messages?: number; request_chars?: number; tool_schema_count?: number;
  }>;
}

export interface ContextCapacityReport {
  state: string; max_occupancy_ratio: number;
  capability: { model: string; context_window_tokens: number; source: string; version: string; confidence: string };
  calibration: { samples: number; average_actual_ratio?: number; last_actual_ratio?: number };
  recommended_actions: string[];
  turns: Array<{
    turn: number; build: number; estimated_input_tokens: number; provider_input_tokens?: number; effective_input_tokens: number;
    measurement_source: string; usable_input_tokens: number; occupancy_ratio: number; state: string; trigger_reason?: string;
    trimmed_messages: number; compacted_tool_results: number;
    waterfall: Array<{ kind: string; label: string; tokens: number }>;
  }>;
}

export interface GovernanceReport {
  state: string;
  policies: Array<{ id: string; description: string; enabled: boolean; threshold: string; action: string }>;
  interventions: Array<{
    id: string; policy_id: string; turn?: number; action: string; enforcement: string; status: string; reason: string;
    evidence: Array<{ type: string; ref?: string; label: string }>;
  }>;
}

export interface RunComparison {
  state: string;
  current: { session_id: string; run_id: string; status: string };
  baseline?: { session_id: string; run_id: string; status: string };
  deltas: Array<{ metric: string; current: number; baseline: number; delta: number; delta_rate?: number; unit: string }>;
  findings: Array<{ severity: string; category: string; title: string; detail: string; evidence: string }>;
  proposal: { summary: string; risk: string; recommendations: string[]; verification_command: string; evidence: string[] };
}

export interface TraceRuntimeView {
  graph: TraceGraph;
  receipts: ReceiptLedger;
  capacity: ContextCapacityReport;
  governance: GovernanceReport;
}

export interface ReplayRuntimeView {
  manifest: {
    schema_version: number;
    session_id: string;
    run_id: string;
    created_at: string;
    completed_at?: string;
    status: string;
    replayability: "exact_only" | "forkable";
    replay_block_reason?: string;
    provider?: string;
    model?: string;
    frame_count: number;
    frames_hash?: string;
    final_status?: string;
    git: { available: boolean; head_commit?: string; tree_hash?: string; dirty?: boolean };
    workspace_snapshot?: { available: boolean; total_bytes?: number; error?: string };
  };
  exact_proof: {
    verified: boolean;
    frame_count: number;
    turn_count: number;
    llm_calls: number;
    tool_calls: number;
    final_status: string;
    proof_hash?: string;
    first_divergence?: { sequence: number; turn?: number; kind: string; reason: string };
  };
  experiments: Array<{
    id: string;
    created_at: string;
    fork_turn: number;
    trials: number;
    success_rate: number;
    proof_hash: string;
    report_path: string;
  }>;
  // turn_detail 仅在请求 ?turn=N 时返回，包含该 turn 的原文明细，供排查每一步实际内容。
  turn_detail?: ReplayTurnDetail;
}

export interface ReplayTurnDetail {
  turn: number;
  requests?: Array<{
    sequence: number;
    system?: string;
    message_count: number;
    messages?: Array<{ role: string; name?: string; content?: string }>;
    tool_count: number;
  }>;
  responses?: Array<{
    sequence: number;
    content?: string;
    tool_call_count: number;
    raw?: string;
  }>;
  tools?: Array<{
    sequence: number;
    index: number;
    name: string;
    arguments?: string;
    result?: string;
    duration_ms: number;
  }>;
}

export interface TuningReport {
  runs_scanned: number; sessions_scanned: number; total_duration_ms: number; llm_duration_ms: number; tool_duration_ms: number;
  tool_failures: number; ask_user_calls: number; permission_events: number; schema_bloat_runs: number; adaptive_routed_runs: number;
  tool_route_escalations: number; schema_bytes_saved: number; request_bloat_runs: number; context_bloat_runs: number;
  slow_llms: Array<{ session_id: string; run_id: string; turn: number; duration_ms: number; tool_schema_count: number; request_chars: number; total_tokens: number }>;
  failed_tools: Array<{ tool: string; error_code: string; status: string; count: number; sessions: number }>;
  recommendations: string[];
}

export interface SessionSummary {
  id: string;
  title: string;
  model: string;
  updated_at: string;
}

export interface CapabilityResource {
  registry: {
    capabilities: Array<{
      id: string; status: string; type: string; risk?: string; entry?: string; triggers?: string[];
      requires?: { tools?: string[]; commands?: string[]; python?: string[]; npm?: string[]; brew?: string[]; env?: string[] };
      verification?: { command?: string; sample_task?: string; last_passed_at?: string };
      updated_at?: string;
    }>;
    gaps: Array<{
      id: string; status: string; missing_capability: string; task: string; source?: string;
      evidence?: string[]; suggested_actions?: string[]; updated_at?: string;
    }>;
    proposals: Array<{
      id: string; gap_id?: string; status: string; summary: string; risk: string; install_scope?: string;
      artifacts?: string[]; dependencies?: { python?: string[]; npm?: string[]; brew?: string[] };
      verification?: { command?: string; sample_task?: string; last_passed_at?: string };
      updated_at?: string;
    }>;
  };
  suggestions: Array<{ missing_capability: string; count: number; reason: string }>;
  dependencies?: {
    plans: Array<{
      id: string; proposal_id: string; capability_id: string; status: string; scope: string; risk: string;
      actions: Array<{ id: string; manager: string; name: string; scope: string; command: string[]; risk: string }>;
      updated_at?: string;
    }>;
    installs: Array<{ id: string; plan_id: string; action_id: string; status: string; exit_code: number; output?: string; installed_at: string }>;
  };
  enabled_adapters?: Array<{ capability_id: string; type: string; entry: string; enabled_at: string }>;
}

export interface SkillSummary {
  id: string;
  name: string;
  description: string;
  scope: string;
  requires?: { mcp?: string[]; env?: string[]; commands?: string[] };
}

export interface MCPServerSummary {
  name: string;
  scope: string;
  type: string;
  command?: string;
  arg_count?: number;
  url?: string;
  env_keys?: string[];
  header_keys?: string[];
}

export interface LSPResource {
  doctor: Array<{ language: string; command: string; version?: string; ok: boolean; error?: string }>;
  servers: Array<{ language: string; running: boolean; pid?: number; error?: string }>;
}

export interface SettingsResource {
  config_path: string;
  language: string;
  workspace: string;
  max_turns: number;
  active_profile: string;
  profiles: Array<{ id: string; name: string; provider: string; model: string; api_base: string; api_key_present: boolean }>;
}

let csrfToken = "";

async function decode<T>(response: Response): Promise<T> {
  const payload = (await response.json()) as T & { error?: string };
  if (!response.ok) {
    throw new Error(payload.error || `${response.status} ${response.statusText}`);
  }
  return payload;
}

export async function initializeSession(): Promise<SessionInfo> {
  const hash = new URLSearchParams(window.location.hash.replace(/^#/, ""));
  const bootstrapToken = hash.get("token");
  const existing = await fetch("/api/v1/auth/session", { credentials: "same-origin" });
  if (existing.ok) {
    const session = await decode<SessionInfo>(existing);
    csrfToken = session.csrf_token;
    if (bootstrapToken) history.replaceState(null, "", window.location.pathname + window.location.search);
    return session;
  }
  if (bootstrapToken) {
    const response = await fetch("/api/v1/auth/bootstrap", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-Cohort-Bootstrap": bootstrapToken,
      },
      body: "{}",
    });
    const bootstrap = await decode<{ csrf_token: string }>(response);
    csrfToken = bootstrap.csrf_token;
    history.replaceState(null, "", window.location.pathname + window.location.search);
  }
  const response = await fetch("/api/v1/auth/session", { credentials: "same-origin" });
  const session = await decode<SessionInfo>(response);
  csrfToken = session.csrf_token;
  return session;
}

export async function apiGet<T>(path: string): Promise<T> {
  const response = await fetch(path, { credentials: "same-origin" });
  return decode<T>(response);
}

export async function apiPost<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrfToken,
    },
    body: JSON.stringify(body),
  });
  return decode<T>(response);
}

export function operationEvents(onEvent: (operation: Operation) => void): () => void {
  const source = new EventSource("/api/v1/events", { withCredentials: true });
  const listener = (event: MessageEvent<string>) => {
    const payload = JSON.parse(event.data) as { operation: Operation };
    onEvent(payload.operation);
  };
  for (const status of ["created", "running", "progress", "succeeded", "failed", "cancelled"]) {
    source.addEventListener(`operation.${status}`, listener as EventListener);
  }
  return () => source.close();
}
