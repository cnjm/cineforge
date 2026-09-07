import assert from "node:assert/strict";
import test from "node:test";

import { dependencyDownloadName } from "./taskDownloads.ts";

test("dependency downloads prefer backend canonical names", () => {
  assert.equal(dependencyDownloadName(
    { asset_code: "R001", asset_name: "Ran", ready: true, files: [] },
    { file_id: "f1", canonical_display_name: "BL-R001-A-V002.png" },
  ), "BL-R001-A-V002.png");
});

test("dependency downloads prefer the signed download name contract", () => {
  assert.equal(dependencyDownloadName(
    { asset_code: "R001", asset_name: "Ran", ready: true, files: [] },
    { file_id: "f1", download_name: "R001-Ran-A.png", canonical_display_name: "legacy.png" },
  ), "R001-Ran-A.png");
});

test("dependency downloads fall back to asset code name and view", () => {
  assert.equal(dependencyDownloadName(
    { asset_code: "SC001", asset_name: "Ran家/卧室", ready: true, files: [] },
    { file_id: "f1", file_name: "upload.jpeg", view_label: "夜景" },
  ), "SC001-Ran家-卧室-夜景.jpeg");
});
