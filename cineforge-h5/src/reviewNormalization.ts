import type { BreakdownManualReviewItem } from "./types";

export function normalizeManualReviewItems(value: unknown): BreakdownManualReviewItem[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item) return [];
    if (typeof item === "object") {
      const raw = item as Record<string, unknown>;
      const fieldPath = reviewTextValue(raw.path || raw.field);
      const rawItem = reviewTextValue(raw.item);
      const title = reviewTextValue(raw.title || raw.message || raw.detail)
        || (fieldPath ? manualReviewFieldLabel(fieldPath) : rawItem);
      if (!title) return [];
      return [{
        item: title,
        detail: reviewTextValue(raw.detail || raw.message),
        status: reviewTextValue(raw.status) || "待确认",
        severity: reviewTextValue(raw.severity) || "warning",
        source: reviewTextValue(raw.source),
        path: fieldPath,
        code: reviewTextValue(raw.code),
        asset_code: reviewTextValue(raw.asset_code),
        asset_name: reviewTextValue(raw.asset_name) || manualReviewAssetName(fieldPath),
        issue_type: reviewTextValue(raw.issue_type),
        uncertainty: reviewTextValue(raw.uncertainty),
        suggestion: reviewTextValue(raw.suggestion) || (fieldPath ? rawItem : ""),
        target: reviewTextValue(raw.target),
        target_field: reviewTextValue(raw.target_field) || manualReviewTargetField(fieldPath),
        missing_reason: reviewTextValue(raw.missing_reason),
        requirement_level: reviewTextValue(raw.requirement_level),
        human_input: reviewTextValue(raw.human_input),
        applied_at: reviewTextValue(raw.applied_at),
        match_status: reviewTextValue(raw.match_status),
        matched_prop_code: reviewTextValue(raw.matched_prop_code),
        matched_prop_name: reviewTextValue(raw.matched_prop_name),
        match_action: reviewTextValue(raw.match_action),
        match_confidence: reviewTextValue(raw.match_confidence),
      }];
    }
    const text = String(item).trim();
    if (!text) return [];
    if (text.startsWith("{") && text.endsWith("}")) {
      try {
        return normalizeManualReviewItems([JSON.parse(text)]);
      } catch {
        return [{ item: text, status: "待确认", severity: "warning" }];
      }
    }
    return [{ item: text, status: "待确认", severity: "warning" }];
  }).filter((item, index, list) => {
    const key = `${item.item}-${item.detail || ""}-${item.source || ""}-${item.code || ""}`;
    return list.findIndex((candidate) => `${candidate.item}-${candidate.detail || ""}-${candidate.source || ""}-${candidate.code || ""}` === key) === index;
  });
}

function manualReviewPathParts(path: string) {
  return path.split(/[.\/]/).map((part) => part.trim()).filter(Boolean);
}

function manualReviewAssetName(path: string) {
  const parts = manualReviewPathParts(path);
  if (parts.length < 3 || /^(?:assets?|characters?|roles?|scenes?|props?|人物|角色|场景|道具)$/i.test(parts[1])) return "";
  return /^\d+$/.test(parts[1]) || /^\[\d+\]$/.test(parts[1]) ? "" : parts[1].replace(/^\[|\]$/g, "");
}

function manualReviewTargetField(path: string) {
  const parts = manualReviewPathParts(path);
  const last = parts.length ? parts[parts.length - 1] : "";
  return last.replace(/^\[|\]$/g, "");
}

function manualReviewFieldLabel(path: string) {
  const assetName = manualReviewAssetName(path);
  const field = manualReviewTargetField(path);
  const fieldLabel = ({ name: "名称", relationship: "人物关系", related_scene_codes: "关联场景" } as Record<string, string>)[field] || field || "待确认字段";
  return assetName ? `${assetName} · ${fieldLabel}` : fieldLabel;
}

function reviewTextValue(value: unknown) {
  if (value === null || value === undefined) return "";
  if (Array.isArray(value)) return value.map(reviewTextValue).filter(Boolean).join(" / ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value).trim();
}
