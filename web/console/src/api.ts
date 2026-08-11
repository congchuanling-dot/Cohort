export type RiskLevel = "read" | "execute" | "confirm" | "danger";

export interface InputField {
  name: string;
  label: string;
  description?: string;
  type: "string" | "text" | "boolean" | "integer" | "select" | "path" | "secret" | "duration";
  required?: boolean;
  default?: unknown;
  options?: string[];
  placeholder?: string;
  sensitive?: boolean;
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
  for (const status of ["created", "running", "succeeded", "failed", "cancelled"]) {
    source.addEventListener(`operation.${status}`, listener as EventListener);
  }
  return () => source.close();
}
