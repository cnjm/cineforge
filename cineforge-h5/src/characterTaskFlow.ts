import type { Task } from "./types";

type ReviewTask = Pick<Task, "id" | "locked" | "status">;

export function expectedCharacterReviewTasks<T extends ReviewTask>(tasks: readonly T[]) {
  return tasks.filter((task) => !task.locked && ["submitted", "reviewing"].includes(task.status));
}

export function characterReviewScopeReady(tasks: readonly ReviewTask[], submittedTaskIds: readonly string[]) {
  if (!submittedTaskIds.length) return false;
  const submitted = new Set(submittedTaskIds);
  const expected = expectedCharacterReviewTasks(tasks);
  return submitted.size === expected.length && expected.every((task) => submitted.has(task.id));
}

export function characterViewRequiresIdentityMaster(taskVariant?: string | null) {
  return ["A", "MASTER"].includes(String(taskVariant || "MASTER").toUpperCase());
}

export function characterReviewSelectionReady(
  taskVariant: string | null | undefined,
  selectedSubmissionIds: readonly string[],
  primarySubmissionId?: string | null,
) {
  if (!selectedSubmissionIds.length) return false;
  if (!characterViewRequiresIdentityMaster(taskVariant)) return true;
  return Boolean(primarySubmissionId && selectedSubmissionIds.includes(primarySubmissionId));
}
