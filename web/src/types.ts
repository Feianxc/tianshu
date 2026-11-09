// 这些类型对应 server/internal/view。集合永远是数组，没测到的数字是 null。

export type HealthState = "ok" | "degraded" | "down" | "unknown" | "paused" | "offline" | "retired" | "planned";
export type Freshness = "fresh" | "partial" | "stale" | "missing";
export type Kind = "device" | "service" | "project" | "repository" | "credential";

export interface Health {
  state: HealthState;
  freshness: Freshness;
  reason: string;
  checked_at: string | null;
  contributors: string[];
  rule_version: number;
}

export interface Workspace {
  id: string;
  slug: string;
  name: string;
  kind: string;
  color: string;
  note: string;
  cloud_policy: string;
  confirmed: boolean;
}

export interface DeviceBrief {
  type: string;
  os: string;
  cpu_pct: number | null;
  mem_pct: number | null;
  disk_pct: number | null;
  disk_free_gb: number | null;
  battery_pct: number | null;
  uptime_s: number | null;
  services: number;
  last_seen: string | null;
  collector: string;
}

export interface ProjectBrief {
  stage: string;
  priority: string;
  next_step: string;
  blockers: number;
  role: string;
  services: number;
  services_up: number;
  repos: number;
  last_verdict: string;
  last_activity: string | null;
  update_stale: boolean;
}

export interface RepoBrief {
  full_name: string;
  visibility: string;
  language: string;
  pushed_at: string | null;
  open_issues: number | null;
  open_prs: number | null;
  ci: string;
  local_dirty: number;
  local_ahead: number;
}

export interface Ref {
  relation: string;
  direction: "out" | "in";
  id: string;
  kind: Kind;
  slug: string;
  name: string;
  health: HealthState;
  restricted: boolean;
}

export interface ServiceBrief {
  kind: string;
  environment: string;
  url: string;
  hosts: Ref[];
  projects: Ref[];
}

export interface Card {
  id: string;
  kind: Kind;
  workspace_id: string;
  workspace: string;
  slug: string;
  name: string;
  summary: string;
  lifecycle: string;
  attrs: Record<string, unknown>;
  tags: string[];
  origin: string;
  confirmed: boolean;
  rev: number;
  updated_at: string;
  health: Health;
  findings: number;
  worst_severity: string;
  device?: DeviceBrief;
  project?: ProjectBrief;
  repo?: RepoBrief;
  service?: ServiceBrief;
}

export interface Signal {
  entity_id: string;
  signal: string;
  state: string;
  value: Record<string, unknown>;
  summary: string;
  source: string;
  observed_at: string;
  received_at: string;
  stale_after_s: number;
  error?: string;
  stale: boolean;
}

export interface Check {
  id: string;
  kind: string;
  target: string;
  interval_s: number;
  enabled: boolean;
}

export interface Finding {
  id: string;
  rule: string;
  entity_id: string;
  entity_name: string;
  entity_kind: Kind;
  workspace_id: string;
  severity: "critical" | "warning" | "info";
  title: string;
  detail: string;
  action: string;
  first_seen: string;
  last_seen: string;
  ack_at: string | null;
  snooze_until: string | null;
}

export interface Evidence {
  id: string;
  verdict: string;
  scope: string;
  environment: string;
  method: string;
  source_url: string;
  by_kind: string;
  by_name: string;
  note: string;
  created_at: string;
}

export interface Detail extends Card {
  relations: Ref[];
  signals: Signal[];
  checks: Check[];
  open_findings: Finding[];
  evidence: Evidence[];
}

export interface Lamp {
  id: string;
  kind: Kind;
  name: string;
  state: HealthState;
  freshness: Freshness;
  reason: string;
}

export interface WallRow {
  workspace: Workspace;
  devices: Lamp[];
  services: Lamp[];
}

export interface Expiring {
  entity_id: string;
  name: string;
  what: string;
  at: string;
  days_left: number;
}

export interface SourceRow {
  name: string;
  kind: string;
  interval_s: number;
  last_ok_at: string | null;
  last_error_at: string | null;
  last_error: string;
  detail: Record<string, unknown>;
  condition: "ok" | "stale" | "error" | "never";
}

export interface Overview {
  generated_at: string;
  attention: Finding[];
  gaps: Finding[];
  snoozed: number;
  wall: WallRow[];
  devices: Card[];
  projects: Card[];
  expiring: Expiring[];
  sources: SourceRow[];
  totals: Record<string, number>;
}

export interface Me {
  name: string;
  kind: string;
  scopes: string[];
  all_workspaces: boolean;
}

export interface Sample {
  ts: string;
  cpu_pct: number | null;
  mem_pct: number | null;
  disk_pct: number | null;
  swap_pct: number | null;
  load1: number | null;
  net_rx_bps: number | null;
  net_tx_bps: number | null;
  battery_pct: number | null;
}

export interface Discovery {
  id: string;
  kind: string;
  device_id: string;
  key: string;
  payload: Record<string, unknown>;
  state: string;
  first_seen: string;
  last_seen: string;
}

export interface Principal {
  id: string;
  kind: string;
  name: string;
  token_prefix: string;
  scopes: string[];
  workspaces: string[];
  devices: string[];
  expires_at: string | null;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
}

export interface AuditEvent {
  id: number;
  ts: string;
  principal: string;
  action: string;
  target: string;
  result: string;
  ip: string;
  detail: string;
}
