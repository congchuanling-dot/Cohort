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
    recent_first?: boolean;
    allow_missing?: boolean;
  };
}

export type EntityKind = "session" | "eval_run" | "delivery" | "hermes_action" | "skill" | "capability" | "mcp_server" | "model_profile";

export interface EntityDescriptor {
  kind: EntityKind;
  id: string;
  title: string;
  subtitle?: string;
  status?: string;
  updated_at?: string;
  version: string;
  badges?: string[];
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
  nodes: Array<{ id: string; kind: string; label: string; detail?: string; turn?: number; status?: string; severity?: string; started_at?: string; duration_ms?: number; order: number; critical?: boolean }>;
  edges: Array<{ from: string; to: string; relation: string }>;
  critical_path: string[]; critical_path_ms: number;
  bottlenecks?: Array<{ node_id: string; kind: string; label: string; reason: string; duration_ms?: number }>;
  anomalies?: Array<{ node_id: string; kind: string; label: string; reason: string; duration_ms?: number }>;
  summary: { node_count: number; edge_count: number; llm_nodes: number; tool_nodes: number; failed_tools: number; file_changes: number; route_escalations: number };
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
    capabilities: Array<{ id: string; status: string; type: string; risk?: string; entry?: string }>;
    gaps: Array<{ id: string; status: string; missing_capability: string; task: string }>;
    proposals: Array<{ id: string; status: string; summary: string; risk: string }>;
  };
  suggestions: Array<{ missing_capability: string; count: number; reason: string }>;
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
