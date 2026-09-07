export type AssetCompletionValue = {
  mode?: "adopt" | "modify" | "defer" | "ignore";
  text?: string;
  sceneIds?: string[];
};

export function isAssetCompletionResolved(field: string, value?: AssetCompletionValue) {
  if (value?.mode === "defer") return true;
  if (field === "owner_character") return true;
  if (field === "scene_ids" || field === "scene_names") return Boolean(value?.sceneIds?.length);
  return Boolean(value?.text?.trim());
}

export function isOptionalAssetCompletionField(field: string) {
  return field === "owner_character";
}
