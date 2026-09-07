import type {
  AssetNormalizationRelations,
  AssetNormalizationGlobalReviewItem,
  AssetNormalizationReviewItem,
  AssetNormalizationV1,
  AssetRelationTarget,
  AssetTypeV1,
  BreakdownDraftAsset,
  BreakdownDraftData,
  BreakdownRead,
} from "./types.ts";

export const ASSET_NORMALIZATION_VERSION = "AssetNormalization.v1" as const;

type UnknownRecord = Record<string, unknown>;

export function assertAssetNormalizationV1(value: unknown): asserts value is AssetNormalizationV1 {
  const asset = requiredRecord(value, "asset");
  exactValue(asset.normalization_version, ASSET_NORMALIZATION_VERSION, "asset.normalization_version");
  const clientAssetKey = nonEmptyString(asset.client_asset_key, "asset.client_asset_key");
  const assetCode = nonEmptyString(asset.asset_code, "asset.asset_code");
  if (!/^[A-Z0-9]+-[A-Z][A-Z0-9]*$/i.test(assetCode)) {
    invalid("asset.asset_code", "a complete project-prefixed asset code");
  }
  const displayCode = nonEmptyString(asset.display_code, "asset.display_code");
  const displayLabel = nonEmptyString(asset.display_label, "asset.display_label");
  enumValue(asset.code_state, ["candidate", "formal"] as const, "asset.code_state");
  const assetType = enumValue(asset.asset_type, ["character", "scene", "prop"] as const, "asset.asset_type");
  const name = nonEmptyString(asset.name, "asset.name");
  const typePrefix = assetType === "character" ? "R" : assetType === "scene" ? "SC" : "P";
  if (!displayCode.startsWith(typePrefix)) invalid("asset.display_code", `a ${typePrefix} asset code`);
  if (!assetCode.endsWith(`-${displayCode}`)) invalid("asset.asset_code", "the same identity as display_code");
  if (displayLabel !== `${displayCode}-${name}`) invalid("asset.display_label", "display_code joined with name");
  nullableString(asset.description, "asset.description");
  enumValue(asset.priority, ["S", "A", "B", "C"] as const, "asset.priority");
  assertAttributes(asset.attributes, assetType, "asset.attributes");
  assertRelations(asset.relations, "asset.relations");
  requiredArray(asset.review_items, "asset.review_items").forEach((item, index) => {
    assertReviewItem(item, clientAssetKey, `asset.review_items[${index}]`);
  });
  assertSource(asset.source, "asset.source");
  requiredArray(asset.repairs, "asset.repairs").forEach((item, index) => {
    assertRepair(item, `asset.repairs[${index}]`);
  });
}

export function parseAssetNormalizationV1(value: unknown): AssetNormalizationV1 {
  assertAssetNormalizationV1(value);
  return value;
}

export function parseAssetNormalizations(value: unknown): AssetNormalizationV1[] {
  const clientKeys = new Set<string>();
  const assetCodes = new Set<string>();
  const displayCodes = new Set<string>();
  const assets = requiredArray(value, "assets").map((asset, index) => {
    try {
      const parsed = parseAssetNormalizationV1(asset);
      if (clientKeys.has(parsed.client_asset_key)) {
        invalid(`assets[${index}].client_asset_key`, "a unique client_asset_key");
      }
      if (assetCodes.has(parsed.asset_code)) invalid(`assets[${index}].asset_code`, "a unique asset_code");
      if (displayCodes.has(parsed.display_code)) invalid(`assets[${index}].display_code`, "a unique display_code");
      clientKeys.add(parsed.client_asset_key);
      assetCodes.add(parsed.asset_code);
      displayCodes.add(parsed.display_code);
      return parsed;
    } catch (error) {
      if (error instanceof TypeError) {
        throw new TypeError(`assets[${index}]: ${error.message}`);
      }
      throw error;
    }
  });
  const byKey = new Map(assets.map((asset) => [asset.client_asset_key, asset]));
  const reviewKeys = new Set<string>();
  assets.forEach((asset, assetIndex) => {
    asset.review_items.forEach((item, reviewIndex) => {
      const reviewKey = `${item.asset_key}\u0000${item.review_id}`;
      if (reviewKeys.has(reviewKey)) invalid(`assets[${assetIndex}].review_items[${reviewIndex}].review_id`, "unique within its asset");
      reviewKeys.add(reviewKey);
    });
    asset.relations.scene_links.forEach((link, linkIndex) => {
      assertClosedRelation(link, byKey, "scene", `assets[${assetIndex}].relations.scene_links[${linkIndex}]`);
    });
    if (asset.relations.owner_character) {
      assertClosedRelation(asset.relations.owner_character, byKey, "character", `assets[${assetIndex}].relations.owner_character`);
    }
  });
  return assets;
}

export function parseGlobalAssetReviewItems(value: unknown): AssetNormalizationGlobalReviewItem[] {
  const reviewIds = new Set<string>();
  return requiredArray(value, "global_review_items").map((item, index) => {
    const path = `global_review_items[${index}]`;
    const review = requiredRecord(item, path);
    exactKeys(review, [
      "review_id", "target_scope", "target_field", "issue", "suggestion", "status",
      "severity", "requirement_level", "source", "source_path", "human_input",
    ], path);
    const reviewId = nonEmptyString(review.review_id, `${path}.review_id`);
    if (reviewIds.has(reviewId)) invalid(`${path}.review_id`, "a unique review_id");
    reviewIds.add(reviewId);
    exactValue(review.target_scope, "inventory", `${path}.target_scope`);
    nonEmptyString(review.target_field, `${path}.target_field`);
    nonEmptyString(review.issue, `${path}.issue`);
    nullableString(review.suggestion, `${path}.suggestion`);
    nonEmptyString(review.status, `${path}.status`);
    enumValue(review.severity, ["warning", "error"] as const, `${path}.severity`);
    enumValue(review.requirement_level, ["required", "optional"] as const, `${path}.requirement_level`);
    nonEmptyString(review.source, `${path}.source`);
    nullableString(review.source_path, `${path}.source_path`);
    nullableString(review.human_input, `${path}.human_input`);
    return review as AssetNormalizationGlobalReviewItem;
  });
}

function assertClosedRelation(
  link: AssetRelationTarget,
  byKey: Map<string, AssetNormalizationV1>,
  expectedType: AssetTypeV1,
  path: string,
): void {
  const target = byKey.get(link.target_asset_key);
  if (!target) invalid(`${path}.target_asset_key`, "an asset in the same inventory");
  if (target.asset_type !== expectedType) invalid(`${path}.target_asset_key`, `a ${expectedType} asset`);
  if (
    link.target_asset_code !== target.asset_code
    || link.target_display_code !== target.display_code
    || link.target_display_label !== target.display_label
  ) {
    invalid(path, "the exact target asset identity");
  }
}

export function parseBreakdownRead(value: unknown): BreakdownRead {
  const breakdown = requiredRecord(value, "breakdown");
  exactKeys(breakdown, ["project_id", "version", "data_state", "view", "agent_run_id", "updated_at"], "breakdown");
  nonEmptyString(breakdown.project_id, "breakdown.project_id");
  if (!Number.isInteger(breakdown.version) || Number(breakdown.version) < 1) {
    invalid("breakdown.version", "a positive integer");
  }
  enumValue(breakdown.data_state, ["agent_raw", "normalized", "human_revision", "final"] as const, "breakdown.data_state");
  const view = requiredRecord(breakdown.view, "breakdown.view");
  exactValue(view.normalization_version, ASSET_NORMALIZATION_VERSION, "breakdown.view.normalization_version");
  parseAssetNormalizations(view.assets);
  parseGlobalAssetReviewItems(view.global_review_items);
  assertBreakdownReviewState(view.reading_review_state, "breakdown.view.reading_review_state");
  assertBreakdownReviewState(view.asset_review_state, "breakdown.view.asset_review_state");
  if (breakdown.agent_run_id !== null) nonEmptyString(breakdown.agent_run_id, "breakdown.agent_run_id");
  nonEmptyString(breakdown.updated_at, "breakdown.updated_at");
  return breakdown as BreakdownRead;
}

function assertBreakdownReviewState(value: unknown, path: string): void {
  const state = requiredRecord(value, path);
  onlyKeys(state, ["status", "confirmed_revision_id", "confirmation_mode"], path);
  enumValue(state.status, ["needs_review", "pending_confirmation", "confirmed"] as const, `${path}.status`);
  if (Object.prototype.hasOwnProperty.call(state, "confirmed_revision_id")) nullableString(state.confirmed_revision_id, `${path}.confirmed_revision_id`);
  if (Object.prototype.hasOwnProperty.call(state, "confirmation_mode")) nullableString(state.confirmation_mode, `${path}.confirmation_mode`);
}

export function assetByType(assets: readonly AssetNormalizationV1[], assetType: AssetTypeV1): AssetNormalizationV1[] {
  return assets.filter((asset) => asset.asset_type === assetType);
}

export function assetByClientKey(
  assets: readonly AssetNormalizationV1[],
  clientAssetKey: string,
): AssetNormalizationV1 | undefined {
  return assets.find((asset) => asset.client_asset_key === clientAssetKey);
}

export function relationsByClientAssetKey(
  assets: readonly AssetNormalizationV1[],
  clientAssetKey: string,
): AssetNormalizationRelations | undefined {
  return assetByClientKey(assets, clientAssetKey)?.relations;
}

export function reviewItemsByClientAssetKey(
  assets: readonly AssetNormalizationV1[],
  clientAssetKey: string,
): AssetNormalizationReviewItem[] | undefined {
  return assetByClientKey(assets, clientAssetKey)?.review_items;
}

export function assetDisplayCode(asset: AssetNormalizationV1): string {
  return asset.display_code;
}

export function assetDisplayLabel(asset: AssetNormalizationV1): string {
  return asset.display_label;
}

export function assetCodeIsFormal(asset: AssetNormalizationV1): boolean {
  return asset.code_state === "formal";
}

export function withEditedAssetInventory(draft: BreakdownDraftData, assets: BreakdownDraftAsset[]): BreakdownDraftData {
  return {
    ...draft,
    assets,
    assetReviewState: {
      ...draft.assetReviewState,
      status: "pending_confirmation",
    },
  };
}

export function withFreshAssetConfirmation(draft: BreakdownDraftData): BreakdownDraftData {
  return {
    ...draft,
    assetReviewState: {
      ...draft.assetReviewState,
      status: "confirmed",
      confirmed_revision_id: null,
      confirmation_mode: "human_confirmed",
    },
  };
}

export function breakdownPayload(draft: BreakdownDraftData, episodeCode = "EP01") {
  const revisionContext = draft.readingRevisionContext || {};
  const resolvedEpisodeCode = scopeString(revisionContext.episode_code) || episodeCode;
  // Only send fields the human actually edits in the workbench. Agent-derived,
  // display-only fields (readthrough, dialogue_script, coverage_checks, keyframe_plan,
  // video_plan, cross_references, delivery_checklist, costume/prompt summaries, etc.)
  // are NOT round-tripped — echoing them back re-persisted stale Agent output into the
  // human revision, violating the single-source version chain. The backend keeps its
  // own copies of those in the normalized layer.
  return {
    view: {
      normalization_version: ASSET_NORMALIZATION_VERSION,
      script_segments: draft.scriptSegments,
      storyboards: draft.storyboards.map((item, index) => ({
        ...item,
        order_num: item.order_num || index + 1,
        episode_code: resolvedEpisodeCode,
      })),
      assets: parseAssetNormalizations(draft.assets),
      global_review_items: parseGlobalAssetReviewItems(draft.globalAssetReviewItems),
      reading_report: draft.readingReport,
      reading_review_state: draft.readingReviewState,
      asset_review_state: draft.assetReviewState,
      notes: draft.notes,
      reading_revision_context: draft.readingRevisionContext,
    },
    episode_code: resolvedEpisodeCode,
    episode_id: scopeString(revisionContext.episode_id) || undefined,
    script_version_id: scopeString(revisionContext.script_version_id) || undefined,
    change_summary: ["前端人工修正保存"],
    source_label: "human_modified",
    data_state: "human_revision",
    expected_version: draft.version,
  };
}

function assertRelations(value: unknown, path: string): void {
  const relations = requiredRecord(value, path);
  exactKeys(relations, [
    "relationship_text", "scene_links", "owner_character", "storyboard_codes",
    "script_segment_codes", "evidence", "confidence",
  ], path);
  nullableString(relations.relationship_text, `${path}.relationship_text`);
  requiredArray(relations.scene_links, `${path}.scene_links`).forEach((item, index) => {
    assertRelationTarget(item, `${path}.scene_links[${index}]`);
  });
  if (relations.owner_character !== null) {
    assertRelationTarget(relations.owner_character, `${path}.owner_character`);
  }
  stringArray(relations.storyboard_codes, `${path}.storyboard_codes`);
  stringArray(relations.script_segment_codes, `${path}.script_segment_codes`);
  requiredArray(relations.evidence, `${path}.evidence`).forEach((item, index) => {
    assertEvidence(item, `${path}.evidence[${index}]`);
  });
  enumValue(relations.confidence, ["high", "medium", "low", "unknown"] as const, `${path}.confidence`);
}

function assertRelationTarget(value: unknown, path: string): asserts value is AssetRelationTarget {
  const target = requiredRecord(value, path);
  exactKeys(target, ["target_asset_key", "target_asset_code", "target_display_code", "target_display_label"], path);
  nonEmptyString(target.target_asset_key, `${path}.target_asset_key`);
  nonEmptyString(target.target_asset_code, `${path}.target_asset_code`);
  nonEmptyString(target.target_display_code, `${path}.target_display_code`);
  nonEmptyString(target.target_display_label, `${path}.target_display_label`);
}

function assertReviewItem(value: unknown, expectedAssetKey: string, path: string): void {
  const item = requiredRecord(value, path);
  exactKeys(item, [
    "review_id", "asset_key", "target_field", "issue", "suggestion", "status",
    "severity", "requirement_level", "source", "source_path", "human_input",
  ], path);
  nonEmptyString(item.review_id, `${path}.review_id`);
  exactValue(item.asset_key, expectedAssetKey, `${path}.asset_key`);
  nonEmptyString(item.target_field, `${path}.target_field`);
  nonEmptyString(item.issue, `${path}.issue`);
  nullableString(item.suggestion, `${path}.suggestion`);
  nonEmptyString(item.status, `${path}.status`);
  enumValue(item.severity, ["warning", "error"] as const, `${path}.severity`);
  enumValue(item.requirement_level, ["required", "optional"] as const, `${path}.requirement_level`);
  nonEmptyString(item.source, `${path}.source`);
  nullableString(item.source_path, `${path}.source_path`);
  nullableString(item.human_input, `${path}.human_input`);
}

function assertSource(value: unknown, path: string): void {
  const source = requiredRecord(value, path);
  exactKeys(source, ["source_kind", "agent_run_id", "skill_name", "raw_group", "raw_index"], path);
  enumValue(source.source_kind, ["agent", "human", "confirmed_inventory"] as const, `${path}.source_kind`);
  nullableString(source.agent_run_id, `${path}.agent_run_id`);
  nullableString(source.skill_name, `${path}.skill_name`);
  enumValue(source.raw_group, ["assets", "characters", "scenes", "props"] as const, `${path}.raw_group`);
  if (!Number.isInteger(source.raw_index) || Number(source.raw_index) < 0) {
    invalid(`${path}.raw_index`, "a non-negative integer");
  }
}

function assertRepair(value: unknown, path: string): void {
  const repair = requiredRecord(value, path);
  exactKeys(repair, ["field", "action", "before", "after", "reason"], path);
  stringValue(repair.field, `${path}.field`);
  stringValue(repair.action, `${path}.action`);
  requiredField(repair, "before", path);
  requiredField(repair, "after", path);
  stringValue(repair.reason, `${path}.reason`);
}

function assertAttributes(value: unknown, assetType: AssetTypeV1, path: string): void {
  const attributes = requiredRecord(value, path);
  if (assetType === "character") {
    exactKeys(attributes, [
      "character_type", "visual_presence", "role", "gender", "age", "appearance",
      "core_requirement", "personality", "background", "has_dialogue",
      "dialogue_evidence", "context_codes",
    ], path);
    for (const field of ["character_type", "role", "gender", "age", "appearance", "core_requirement", "personality", "background"]) {
      nullableString(attributes[field], `${path}.${field}`);
    }
    enumValue(attributes.visual_presence, ["on_screen", "voice_only", "mentioned_only"] as const, `${path}.visual_presence`);
    if (typeof attributes.has_dialogue !== "boolean") invalid(`${path}.has_dialogue`, "a boolean");
    stringArray(attributes.dialogue_evidence, `${path}.dialogue_evidence`);
    stringArray(attributes.context_codes, `${path}.context_codes`);
    return;
  }
  if (assetType === "scene") {
    exactKeys(attributes, [
      "context_code", "render_mode", "scene_no", "interior_exterior", "time", "location",
      "atmosphere", "narrative_function", "spatial_zones", "visual_goal", "key_props",
      "key_prop_codes", "continuity_risks", "episode_code",
    ], path);
    for (const field of ["context_code", "scene_no", "interior_exterior", "time", "location", "atmosphere", "narrative_function", "visual_goal", "episode_code"]) {
      nullableString(attributes[field], `${path}.${field}`);
    }
    if (attributes.render_mode !== null) {
      enumValue(attributes.render_mode, ["animated", "live_action"] as const, `${path}.render_mode`);
    }
    for (const field of ["spatial_zones", "key_props", "key_prop_codes", "continuity_risks"]) {
      stringArray(attributes[field], `${path}.${field}`);
    }
    return;
  }
  exactKeys(attributes, [
    "level", "prop_type", "appearance", "source", "status_change", "variant_states",
    "usage_scene", "scene_description", "context_codes",
  ], path);
  for (const field of ["level", "prop_type", "appearance", "source", "status_change", "usage_scene", "scene_description"]) {
    nullableString(attributes[field], `${path}.${field}`);
  }
  stringArray(attributes.variant_states, `${path}.variant_states`);
  stringArray(attributes.context_codes, `${path}.context_codes`);
}

function assertEvidence(value: unknown, path: string): void {
  const evidence = requiredRecord(value, path);
  exactKeys(evidence, ["target_code", "storyboard_code", "script_segment_code", "source_field", "excerpt"], path);
  for (const field of ["target_code", "storyboard_code", "script_segment_code", "source_field", "excerpt"]) {
    nullableString(evidence[field], `${path}.${field}`);
  }
}

function requiredRecord(value: unknown, path: string): UnknownRecord {
  if (!value || typeof value !== "object" || Array.isArray(value)) invalid(path, "an object");
  return value as UnknownRecord;
}

function scopeString(value: unknown): string {
  return value === null || value === undefined ? "" : String(value).trim();
}

function requiredArray(value: unknown, path: string): unknown[] {
  if (!Array.isArray(value)) invalid(path, "an array");
  return value;
}

function nonEmptyString(value: unknown, path: string): string {
  if (typeof value !== "string" || !value.trim()) invalid(path, "a non-empty string");
  return value;
}

function stringValue(value: unknown, path: string): string {
  if (typeof value !== "string") invalid(path, "a string");
  return value;
}

function nullableString(value: unknown, path: string): void {
  if (value !== null && typeof value !== "string") invalid(path, "a string or null");
}

function stringArray(value: unknown, path: string): void {
  requiredArray(value, path).forEach((item, index) => stringValue(item, `${path}[${index}]`));
}

function exactKeys(record: UnknownRecord, fields: readonly string[], path: string): void {
  const expected = new Set(fields);
  for (const field of fields) requiredField(record, field, path);
  const extra = Object.keys(record).find((field) => !expected.has(field));
  if (extra) invalid(`${path}.${extra}`, "no unknown fields");
}

function onlyKeys(record: UnknownRecord, fields: readonly string[], path: string): void {
  const expected = new Set(fields);
  const extra = Object.keys(record).find((field) => !expected.has(field));
  if (extra) invalid(`${path}.${extra}`, "no unknown fields");
}

function enumValue<const T extends readonly string[]>(value: unknown, allowed: T, path: string): T[number] {
  if (typeof value !== "string" || !allowed.includes(value)) invalid(path, allowed.join(" | "));
  return value as T[number];
}

function exactValue(value: unknown, expected: string, path: string): void {
  if (value !== expected) invalid(path, JSON.stringify(expected));
}

function requiredField(record: UnknownRecord, field: string, path: string): void {
  if (!Object.prototype.hasOwnProperty.call(record, field)) invalid(`${path}.${field}`, "a present field");
}

function invalid(path: string, expected: string): never {
  throw new TypeError(`Invalid AssetNormalization.v1 contract at ${path}; expected ${expected}`);
}
