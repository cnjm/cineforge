package repository

// P3e task 域常量（对齐 legacy app/models/enums.py 与 repositories.py 的字符串字面量）。
// 不建 PG enum：submissions.status / task_submission_batches.status 是 String 列。

// ---- task status（对齐 TaskStatus StrEnum） ----
const (
	taskStatusTodo        = "todo"
	taskStatusInProgress  = "in_progress"
	taskStatusSubmitted   = "submitted"
	taskStatusReviewing   = "reviewing"
	taskStatusCompleted   = "completed"
	taskStatusRejected    = "rejected"
	taskStatusOverdue     = "overdue"
)

// ---- task type（对齐 TaskType StrEnum） ----
const (
	taskTypeScriptBreakdown = "script_breakdown"
	taskTypeAssetConfirm    = "asset_confirm"
	taskTypeAsset           = "asset"
	taskTypeAudio           = "audio"
	taskTypeTextToImage     = "text_to_image"
	taskTypeImageToVideo    = "image_to_video"
	taskTypeStoryboardShot  = "storyboard_shot"
	taskTypeVideoGeneration = "video_generation"
	taskTypeAssembly        = "assembly"
	taskTypeFinalOutput     = "final_output"
)

// ---- batch / submission status（String 列字面量） ----
const (
	batchStatusDraft     = "draft"
	batchStatusSubmitted = "submitted"
	batchStatusApproved  = "approved"
	batchStatusRework    = "rework"
)

const (
	submissionStatusDraft           = "draft"
	submissionStatusSubmitted       = "submitted"
	submissionStatusRejected        = "rejected"
	submissionStatusPrimaryMaster   = "primary_master"
	submissionStatusAlternateMaster = "alternate_master"
	submissionStatusNotSelected     = "not_selected"
)

// ---- asset type（对齐 AssetType StrEnum） ----
const (
	assetTypeCharacter       = "character"
	assetTypeScene           = "scene"
	assetTypeProp            = "prop"
	assetTypeMusic           = "music"
	assetTypeVoiceProfile    = "voice_profile"
	assetTypeStoryboard      = "storyboard"
	assetTypeStoryboardImage = "storyboard_image"
	assetTypeStoryboardVideo = "storyboard_video"
	assetTypeEffect          = "effect"
	assetTypeFinalVideo      = "final_video"
)

// assetTypeDisplayCodes 对齐 ASSET_TYPE_DISPLAY_CODES。
var assetTypeDisplayCodes = map[string]string{
	assetTypeCharacter:       "CHAR",
	assetTypeScene:           "SCENE",
	assetTypeProp:            "PROP",
	assetTypeMusic:           "MUSIC",
	assetTypeVoiceProfile:    "VOICE",
	assetTypeStoryboard:      "STORYBOARD",
	assetTypeStoryboardImage: "KEYFRAME",
	assetTypeStoryboardVideo: "VIDEO",
	assetTypeEffect:          "EFFECT",
	assetTypeFinalVideo:      "FINAL",
}

// ---- operation_logs.action（VERBATIM 值） ----
const (
	opLogTaskSubmissionCreated              = "task_submission_created"
	opLogTaskBatchSubmitted                 = "task_batch_submitted"
	opLogTaskBatchReviewed                  = "task_batch_reviewed"
	opLogTaskApprovalReopened               = "task_approval_reopened"
	opLogCharacterDependentViewsInvalidated = "character_dependent_views_invalidated"
	opLogTaskDraftSubmissionRemoved         = "task_draft_submission_removed"
	opLogTaskSubmissionArchived             = "task_submission_archived"
	opLogTaskSubmissionRestored             = "task_submission_restored"
	opLogTaskPrimarySubmissionChanged       = "task_primary_submission_changed"
	opLogTaskUpdated                       = "task_updated"
	opLogTaskPromptSaved                    = "task_prompt_saved"
	opLogAssetTasksGenerated                = "asset_tasks_generated"
	opLogStoryboardTasksGenerated           = "storyboard_tasks_generated"
	opLogTasksBulkAssigned                  = "tasks_bulk_assigned"
)

// ---- operation_logs.target_type ----
const (
	targetTypeSubmission         = "submission"
	targetTypeTaskSubmissionBatch = "task_submission_batch"
	targetTypeCharacterAsset      = "character_asset"
	targetTypeTask                = "task"
)

// ---- notifications.type（VERBATIM 值） ----
const (
	notificationTypeTaskRework   = "task_rework"
	notificationTypeTaskApproved = "task_approved"
	notificationTypeTaskUnlocked = "task_unlocked"
)