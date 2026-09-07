import type { Submission, SubmissionBatch } from "./types";

export type SubmissionLifecycle = Pick<Submission, "is_archived" | "is_invalidated">;

export function isActiveSubmission(submission: SubmissionLifecycle) {
  return !submission.is_archived && !submission.is_invalidated;
}

export function activeSubmissions<T extends SubmissionLifecycle>(submissions: readonly T[]) {
  return submissions.filter(isActiveSubmission);
}

export function hasInvalidatedSubmissions(submissions: readonly Submission[]) {
  return submissions.some((submission) => Boolean(submission.is_invalidated));
}

export function currentBatchSubmissions(
  submissions: readonly Submission[],
  batches: readonly SubmissionBatch[],
  step?: string,
) {
  const relevantBatches = batches
    .filter((batch) => !step || batch.step === step)
    .sort((left, right) => right.version_no - left.version_no);
  const batch = relevantBatches[0];
  if (!batch) return activeSubmissions(submissions).filter((submission) => (
    submission.status !== "rejected" && (!step || (submission.step || "result") === step)
  ));
  if (!["draft", "submitted", "approved"].includes(batch.status)) return [];
  return activeSubmissions(submissions).filter((submission) => (
    submission.batch_id === batch.id && (!step || (submission.step || "result") === step)
  ));
}

export function rejectedSubmissionHistory(submissions: readonly Submission[], step?: string) {
  return activeSubmissions(submissions).filter((submission) => (
    submission.status === "rejected" && (!step || (submission.step || "result") === step)
  ));
}

export function isMakerRemovableSubmission(submission: Submission) {
  return isActiveSubmission(submission) && ["draft", "rejected"].includes(submission.status);
}
