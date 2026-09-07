import type { DependencyAsset, DependencyFile } from "./types";

function safeFilename(value: string) {
  return value.replace(/[\\/:*?"<>|\r\n]+/g, "-").replace(/\s+/g, " ").trim();
}

function fileExtension(file: DependencyFile) {
  const match = String(file.file_name || "").match(/(\.[a-z0-9]{1,10})$/i);
  if (match) return match[1].toLowerCase();
  const mime = String(file.mime_type || "").toLowerCase();
  if (mime.includes("png")) return ".png";
  if (mime.includes("jpeg") || mime.includes("jpg")) return ".jpg";
  if (mime.includes("webp")) return ".webp";
  if (mime.includes("mp4")) return ".mp4";
  if (mime.includes("quicktime")) return ".mov";
  if (mime.includes("mpeg")) return ".mp3";
  if (mime.includes("wav")) return ".wav";
  return "";
}

export function dependencyDownloadName(asset: DependencyAsset, file: DependencyFile) {
  if (file.download_name) return safeFilename(file.download_name);
  if (file.canonical_display_name) return safeFilename(file.canonical_display_name);
  const base = [asset.asset_code, asset.asset_name, file.view_label].filter(Boolean).join("-") || "前置资产";
  const safeBase = safeFilename(base);
  return /\.[a-z0-9]{1,10}$/i.test(safeBase) ? safeBase : `${safeBase}${fileExtension(file)}`;
}
