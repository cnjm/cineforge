import assert from "node:assert/strict";
import test from "node:test";
import { addQuery, matchesLineagePayload, normalizeRunList, parseRunEventBlock, selectAgentRunsForHydration } from "./runData.ts";

test("normalizes paginated run summaries", () => {
  const page = normalizeRunList<{ id: string }>({
    items: [{ id: "run-1" }],
    next_cursor: "opaque-cursor",
    has_more: true,
    limit: 50,
  });

  assert.deepEqual(page.items, [{ id: "run-1" }]);
  assert.equal(page.nextCursor, "opaque-cursor");
  assert.equal(page.hasMore, true);
});

test("keeps compatibility with legacy array responses", () => {
  const page = normalizeRunList<{ id: string }>([{ id: "legacy-run" }]);

  assert.deepEqual(page.items, [{ id: "legacy-run" }]);
  assert.equal(page.nextCursor, null);
  assert.equal(page.hasMore, false);
});

test("parses snapshot SSE events", () => {
  const event = parseRunEventBlock([
    "event: snapshot",
    "data: {\"project_id\":\"p1\",\"agent_runs\":[{\"id\":\"r1\",\"status\":\"running\"}],\"workflows\":[]}",
  ].join("\n"));

  assert.equal(event?.event, "snapshot");
  assert.deepEqual(event?.data, {
    project_id: "p1",
    agent_runs: [{ id: "r1", status: "running" }],
    workflows: [],
  });
});

test("adds cursor without losing existing filters", () => {
  assert.equal(
    addQuery("/agent-runs?project_id=p1&include_payload=false", { limit: "100", cursor: "a/b+c" }),
    "/agent-runs?project_id=p1&include_payload=false&limit=100&cursor=a%2Fb%2Bc",
  );
});

test("hydrates only latest and latest successful business runs for an episode", () => {
  const selected = selectAgentRunsForHydration([
    { id: "breakdown-latest-failed", agent_type: "script_breakdown", status: "failed", episode_id: "ep-1" },
    { id: "breakdown-latest-success", agent_type: "script_breakdown", status: "succeeded", episode_id: "ep-1" },
    { id: "breakdown-old-success", agent_type: "script_breakdown", status: "succeeded", episode_id: "ep-1" },
    { id: "reading-latest", agent_type: "script_reading", status: "succeeded", episode_id: "ep-1" },
    { id: "other-episode", agent_type: "asset_extract", status: "succeeded", episode_id: "ep-2" },
    { id: "legacy-agent", agent_type: "query", status: "succeeded", episode_id: "ep-1" },
  ], { episodeId: "ep-1" });

  assert.deepEqual(selected.map((run) => run.id), [
    "breakdown-latest-failed",
    "breakdown-latest-success",
    "reading-latest",
  ]);
});

test("excludes an old script version even when the episode matches", () => {
  const selected = selectAgentRunsForHydration([
    { id: "old-version", agent_type: "script_breakdown", status: "succeeded", episode_id: "ep-1", script_version_id: "sv-old" },
    { id: "current-version", agent_type: "script_breakdown", status: "succeeded", episode_id: "ep-1", script_version_id: "sv-current" },
  ], { episodeId: "ep-1", scriptVersionId: "sv-current" });

  assert.deepEqual(selected.map((run) => run.id), ["current-version"]);
});

test("rejects nested lineage when any explicit version conflicts", () => {
  assert.equal(matchesLineagePayload({
    episode_id: "ep-1",
    script_version_id: "sv-current",
    source: { nested: [{ script_version_id: "sv-old" }] },
  }, { episodeId: "ep-1", scriptVersionId: "sv-current" }), false);
});

test("allows episode fallback only for unversioned legacy lineage", () => {
  assert.equal(matchesLineagePayload(
    { episode_id: "ep-1" },
    { episodeId: "ep-1", scriptVersionId: "sv-current" },
  ), true);
});
