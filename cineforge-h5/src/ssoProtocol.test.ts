import test from "node:test";
import assert from "node:assert/strict";
import { createCineForgeSsoSessionMessage, CINEFORGE_SSO_SESSION_MESSAGE } from "./ssoProtocol.ts";

test("builds a versioned dual-token SSO message", () => {
  assert.deepEqual(
    createCineForgeSsoSessionMessage("tap", "platform", 1_700_028_800),
    {
      type: CINEFORGE_SSO_SESSION_MESSAGE,
      tapcanvas_token: "tap",
      platform_token: "platform",
      session_expires_at: 1_700_028_800,
      source: "cine-forge",
    },
  );
});

test("rejects incomplete SSO sessions", () => {
  assert.throws(() => createCineForgeSsoSessionMessage("tap", "", 1_700_028_800));
});
