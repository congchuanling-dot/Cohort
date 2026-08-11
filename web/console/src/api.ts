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
