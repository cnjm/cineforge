import assert from "node:assert/strict";
import test from "node:test";

import { activeSubmissions, currentBatchSubmissions, hasInvalidatedSubmissions, isActiveSubmission, isMakerRemovableSubmission, rejectedSubmissionHistory } from "./submissionLifecycle.ts";
import type { Submission, SubmissionBatch } from "./types.ts";

test("active submission excludes archived and invalidated history", () => {
  const active = { id: "active", is_archived: false, is_invalidated: false };
  const archived = { id: "archived", is_archived: true, is_invalidated: false };
  const invalidated = { id: "invalidated", is_archived: false, is_invalidated: true };
  const legacy = { id: "legacy", is_archived: false };

  assert.equal(isActiveSubmission(active), true);
  assert.equal(isActiveSubmission(archived), false);
  assert.equal(isActiveSubmission(invalidated), false);
  assert.equal(isActiveSubmission(legacy), true);
  assert.deepEqual(activeSubmissions([active, archived, invalidated, legacy]).map((item) => item.id), ["active", "legacy"]);
});

test("invalidation history remains detectable for B-E re-production messaging", () => {
  assert.equal(hasInvalidatedSubmissions([]), false);
  assert.equal(hasInvalidatedSubmissions([{ is_invalidated: true } as Submission]), true);
});

test("current candidate area uses the latest draft batch and separates rejected history", () => {
  const submissions = [
    { id: "old-rejected", batch_id: "v1", step: "result", status: "rejected", is_archived: false, is_invalidated: false },
    { id: "draft-1", batch_id: "v2", step: "result", status: "draft", is_archived: false, is_invalidated: false },
    { id: "draft-2", batch_id: "v2", step: "result", status: "draft", is_archived: false, is_invalidated: false },
  ] as Submission[];
  const batches = [
    { id: "v1", step: "result", version_no: 1, status: "rework" },
    { id: "v2", step: "result", version_no: 2, status: "draft" },
  ] as SubmissionBatch[];

  assert.deepEqual(currentBatchSubmissions(submissions, batches, "result").map((item) => item.id), ["draft-1", "draft-2"]);
  assert.deepEqual(rejectedSubmissionHistory(submissions, "result").map((item) => item.id), ["old-rejected"]);
});

test("a latest rework batch never revives an older approved batch as current candidates", () => {
  const submissions = [
    { id: "old-approved", batch_id: "v1", step: "result", status: "alternate_master", is_archived: false, is_invalidated: false },
    { id: "latest-rejected", batch_id: "v2", step: "result", status: "rejected", is_archived: false, is_invalidated: false },
  ] as Submission[];
  const batches = [
    { id: "v1", step: "result", version_no: 1, status: "approved" },
    { id: "v2", step: "result", version_no: 2, status: "rework" },
  ] as SubmissionBatch[];

  assert.deepEqual(currentBatchSubmissions(submissions, batches, "result"), []);
  assert.deepEqual(rejectedSubmissionHistory(submissions, "result").map((item) => item.id), ["latest-rejected"]);
});

test("viewer removal is limited to active draft or rejected submissions", () => {
  const submission = (status: string, extra = {}) => ({ status, is_archived: false, is_invalidated: false, ...extra }) as Submission;
  assert.equal(isMakerRemovableSubmission(submission("draft")), true);
  assert.equal(isMakerRemovableSubmission(submission("rejected")), true);
  assert.equal(isMakerRemovableSubmission(submission("submitted")), false);
  assert.equal(isMakerRemovableSubmission(submission("rejected", { is_archived: true })), false);
});
