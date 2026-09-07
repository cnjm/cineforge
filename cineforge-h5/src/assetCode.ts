/** Derive a display code from a project-prefixed normalized code or a backend-issued short code. */
export function shortAssetCode(value?: unknown, source?: unknown) {
  const normalizedSource = String(source || "").trim().toLowerCase();
  const raw = String(value ?? "").trim().toUpperCase();
  const fullMatch = raw.match(/^[A-Z0-9]+-(R|SC|S|P)(\d+)$/);
  const backendShortMatch = normalizedSource === "backend" ? raw.match(/^(R|SC|S|P)(\d+)$/) : null;
  const match = fullMatch || backendShortMatch;
  if (!match) return "";
  const prefix = match[1] === "S" ? "SC" : match[1];
  return `${prefix}${String(Number(match[2])).padStart(3, "0")}`;
}

export type AssetReference = {
  asset_code?: unknown;
  display_code?: unknown;
  display_label?: unknown;
  asset_type?: unknown;
  type?: unknown;
  name?: unknown;
};

export function canonicalAssetReferenceCode(value?: unknown) {
  const raw = String(value ?? "").trim().toUpperCase();
  if (!raw) return "";
  const match = raw.match(/(?:^|[-\s])(R|SC|S|P)0*(\d+)(?=$|[-\s:：·])/i);
  if (!match) return "";
  const prefix = match[1].toUpperCase() === "S" ? "SC" : match[1].toUpperCase();
  return `${prefix}${String(Number(match[2])).padStart(3, "0")}`;
}

/** Resolve a formal asset reference to one stable `R001-Name` / `SC001-Name` label. */
export function assetReferenceDisplayLabel(value: unknown, assets: AssetReference[], assetType?: "character" | "scene" | "prop") {
  const code = canonicalAssetReferenceCode(value);
  const raw = String(value || "").trim();
  const matched = assets.find((asset) => {
    const candidateType = String(asset.asset_type || asset.type || "").trim().toLowerCase();
    if (assetType && candidateType && candidateType !== assetType) return false;
    return Boolean(code && (
      canonicalAssetReferenceCode(asset.asset_code) === code
      || canonicalAssetReferenceCode(asset.display_code) === code
    ));
  });
  const canonicalLabel = String(matched?.display_label || "").trim();
  if (canonicalLabel) return canonicalLabel;
  const matchedCode = canonicalAssetReferenceCode(matched?.asset_code);
  const name = stripReferenceCode(String(matched?.name || "").trim());
  const resolvedCode = code || matchedCode;
  if (resolvedCode && name) return `${resolvedCode}-${name}`;
  return resolvedCode || (raw ? `未匹配资产（${raw}）` : "未匹配资产");
}

/** Strip a leading formal code from a display name so labels do not double-prefix. */
export function assetNamePart(value?: unknown) {
  const raw = String(value ?? "").trim();
  if (!raw) return "";
  if (/^(?:[A-Z0-9]+-)?(?:R|SC|S|P|M)0*\d+$/i.test(raw)) return "";
  const match = raw.match(/^(?:[A-Z0-9]+-)?(?:R|SC|S|P|M)0*\d+\s*[-:：·]?\s*(.+)$/i);
  return match?.[1]?.trim() || raw;
}

/** Display label as `R001-郑夏允` / `SC001-俱乐部走廊` / `P001-酒杯`. */
export function assetDisplayLabel(code?: unknown, name?: unknown, source?: unknown) {
  const shortCode = shortAssetCode(code, source) || canonicalAssetReferenceCode(code);
  const cleanName = assetNamePart(name) || assetNamePart(code);
  if (shortCode && cleanName) return `${shortCode}-${cleanName}`;
  return cleanName || shortCode || "未命名";
}

function stripReferenceCode(value: string) {
  return value.replace(/^(?:[A-Z0-9]+-)?(?:R|SC|S|P)0*\d+\s*[-:：·]?\s*/i, "").trim();
}

export function hydrateStoryboardSceneNames<T extends { scene_code?: unknown; scene_name?: unknown }>(storyboards: T[], assets: AssetReference[]): T[] {
  return storyboards.map((storyboard) => {
    if (String(storyboard.scene_name || "").trim() || !storyboard.scene_code) return storyboard;
    const code = canonicalAssetReferenceCode(storyboard.scene_code);
    const matched = assets.find((asset) => (
      String(asset.asset_type || asset.type || "").trim().toLowerCase() === "scene"
      && canonicalAssetReferenceCode(asset.asset_code) === code
    ));
    return matched?.name ? { ...storyboard, scene_name: String(matched.name).trim() } : storyboard;
  });
}
