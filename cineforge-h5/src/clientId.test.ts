import assert from "node:assert/strict";
import test from "node:test";
import { createClientRequestId, createClientUuid } from "./clientId.ts";

test("client request id is available when browser UUID APIs are missing", () => {
  const first = createClientRequestId("temporary");
  const second = createClientRequestId("temporary");
  assert.match(first, /^temporary-/);
  assert.notEqual(first, second);
});

test("creates a backend-compatible UUID for idempotent task actions", () => {
  assert.match(createClientUuid(), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i);
});
