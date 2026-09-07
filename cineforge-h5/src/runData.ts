import type { AgentRun } from "./types";

export type NormalizedPage<T> = {
  items: T[];
  nextCursor: string | null;
  hasMore: boolean;
};

export type RunLineageScope = {
  episodeId?: string;
  episodeCode?: string;
  scriptVersionId?: string;
};

const DETAIL_AGENT_TYPES = new Set([
  "script_reading",
  "script_segmentation",
  "script_breakdown",
  "asset_extract",
  "relation_check",
  "content_compliance_review",
  "character_design_prompt",
  "scene_design_prompt",
  "prop_design_prompt",
  "text_to_image_prompt",
  "image_to_video_prompt",
]);

export function normalizeRunList<T>(value: unknown): NormalizedPage<T> {
  if (Array.isArray(value)) return { items: value as T[], nextCursor: null, hasMore: false };
  if (!value || typeof value !== "object") return { items: [], nextCursor: null, hasMore: false };
  const data = value as Record<string, unknown>;
  const items = Array.isArray(data.items)
    ? data.items
    : Array.isArray(data.runs)
      ? data.runs
      : Array.isArray(data.results)
        ? data.results
        : [];
  const nextCursor = typeof data.next_cursor === "string" ? data.next_cursor : null;
  return {
    items: items as T[],
    nextCursor,
    hasMore: data.has_more === true || Boolean(nextCursor),
  };
}

export function addQuery(path: string, params: Record<string, string | undefined>) {
  const [base, rawQuery = ""] = path.split("?", 2);
  const query = new URLSearchParams(rawQuery);
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== "") query.set(key, value);
  });
  const encoded = query.toString();
  return encoded ? `${base}?${encoded}` : base;
}

export function parseRunEventBlock(block: string): { event: string; data: unknown } | null {
  const event = block.match(/^event:\s*(.+)$/m)?.[1]?.trim() || "message";
  const dataLines = block.split(/\r?\n/)
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trim());
  if (!dataLines.length) return { event, data: null };
  const raw = dataLines.join("\n");
  try {
    return { event, data: JSON.parse(raw) };
  } catch {
    return { event, data: raw };
  }
}

export function matchesLineagePayload(payload: unknown, scope: RunLineageScope): boolean {
  if (!payload || typeof payload !== "object") return false;
  const versionIds: string[] = [];
  const collectVersions = (value: unknown) => {
    if (!value || typeof value !== "object") return;
    if (Array.isArray(value)) {
      value.forEach(collectVersions);
      return;
    }
    const record = value as Record<string, unknown>;
    if (record.script_version_id) versionIds.push(String(record.script_version_id).toUpperCase());
    Object.values(record).forEach(collectVersions);
  };
  collectVersions(payload);
  if (scope.scriptVersionId && versionIds.length) {
    const expectedVersion = scope.scriptVersionId.toUpperCase();
    return versionIds.every((value) => value === expectedVersion);
  }
  if (Array.isArray(payload)) return payload.some((item) => matchesLineagePayload(item, scope));
  const record = payload as Record<string, unknown>;
  const episodeId = String(record.episode_id || "").toUpperCase();
  const episodeCode = String(record.episode_code || "").toUpperCase();
  if (scope.episodeId && episodeId === scope.episodeId.toUpperCase()) return true;
  if (scope.episodeCode && episodeCode === scope.episodeCode.toUpperCase()) return true;
  return Object.values(record).some((value) => value && typeof value === "object" && matchesLineagePayload(value, scope));
}

export function matchesRunScope(run: AgentRun, scope: RunLineageScope) {
  if (!scope.episodeId && !scope.episodeCode && !scope.scriptVersionId) return false;
  const summary = run.summary || {};
  return matchesLineagePayload({
    episode_id: run.episode_id,
    episode_code: run.episode_code,
    script_version_id: run.script_version_id,
    summary,
  }, scope);
}

export function selectAgentRunsForHydration(runs: AgentRun[], scope: RunLineageScope) {
  const selected: AgentRun[] = [];
  const seen = new Set<string>();
  const byType = new Map<string, AgentRun[]>();
  runs.filter((run) => DETAIL_AGENT_TYPES.has(run.agent_type) && matchesRunScope(run, scope)).forEach((run) => {
    byType.set(run.agent_type, [...(byType.get(run.agent_type) || []), run]);
  });
  byType.forEach((items) => {
    const latest = items[0];
    const latestSucceeded = items.find((run) => run.status === "succeeded");
    [latest, latestSucceeded].filter(Boolean).forEach((run) => {
      if (run && !seen.has(run.id)) {
        seen.add(run.id);
        selected.push(run);
      }
    });
  });
  return selected;
}
