export type PageKey =
  | "adminDashboard"
  | "projectManagement"
  | "projectOverview"
  | "projectDetail"
  | "myTasks"
  | "makerTrack"
  | "taskCanvas"
  | "groupCanvas"
  | "creationCenter"
  | "reviewCenter"
  | "reviewWorkbench"
  | "assets"
  | "agentAdmin"
  | "teamMembers"
  | "systemLogs"
  | "queryAgent"
  | "canvas"
  | "workbench"
  | "assetLibrary";

export type ProjectStage = "draft" | "breakdown_review" | "asset_locking" | "task_assignment" | "production" | "completed";
export type BackendProjectStatus =
  | ProjectStage
  | "reading"
  | "locked"
  | "assembly"
  | "archived";
export type ProjectDetailModule = "import" | "revision" | "assignment" | "progress" | "archive";
export type ProjectEntryMode =
  | { type: "create_project" }
  | { type: "project_overview"; projectId: string; module?: ProjectDetailModule; episodeId?: string; from?: string }
  | { type: "add_episode"; projectId: string }
  | { type: "add_script_version"; projectId: string; episodeId: string };
export type UserRole = "director" | "script_editor" | "artist" | "editor" | "admin";

export type Project = {
  id: string;
  project_no?: string | null;
  project_prefix?: string | null;
  name?: string | null;
  title: string;
  genre?: string | null;
  status: BackendProjectStatus | string;
  current_stage?: BackendProjectStatus | string | null;
  manager_id?: string | null;
  script_text?: string | null;
  production_brief?: ProjectProductionBrief | null;
  archived_at?: string | null;
  deleted_at?: string | null;
  storyboard_count: number;
  task_count: number;
  latest_import?: ProjectImportContext | null;
  created_at: string;
  updated_at: string;
  // TODO(总控看板-后端接口需求文档-1 需求2): 后端需在 /projects 与 /projects/:id 返回中补充以下字段：
  //   - cover_image: string | null          项目封面图 URL（空时前端用默认图）
  //   - episode_count: number               项目总集数（独立于 storyboard_count，不能互用）
  //   - completed_episode_count?: number    已完成集数，默认 0；前端展示 `${completed}/${total}集`
  //   - progress_percent?: number | null    项目整体进度百分比（0-100），后端统一口径计算
  //   - members?: ProjectMember[]           可选嵌入项目成员（最多 4 个），避免首屏逐项目请求
  // 当前缺失：前端用 storyboard_count 冒充集数（可能出现 1024集 异常值），所有项目同一封面，进度按 tasks 估算口径不一致。
};

export type ProjectCreatePayload = {
  title: string;
  project_prefix: string;
  genre?: string;
  script_text?: string;
  manager_id?: string | null;
  production_brief: ProjectProductionBrief;
};

export type ProjectUpdatePayload = ProjectCreatePayload;

export type ProjectImportPayload = Pick<ProjectCreatePayload, "title" | "project_prefix" | "genre"> & {
  project_id?: string;
  production_brief?: ProjectProductionBrief;
  episode_brief?: EpisodeProductionBrief;
  episode_no?: number;
  confirm_existing_episode_version?: boolean;
};

export type ProjectImportContext = {
  episode_id: string;
  episode_no: number;
  episode_code: string;
  script_id: string;
  script_version_id: string;
  script_version_no: number;
  created_episode: boolean;
  created_script: boolean;
  replaced_existing_episode: boolean;
  content_hash: string;
};

export type ProjectEpisode = {
  id: string;
  project_id: string;
  episode_no: number;
  episode_code: string;
  title?: string | null;
  script_id?: string | null;
  current_version_id?: string | null;
  current_version_no?: number | null;
  version_count: number;
  current_content_hash?: string | null;
  current_original_filename?: string | null;
  summary?: string | null;
  breakdown_status?: string | null;
  production_brief?: EpisodeProductionBrief | null;
  versions?: ProjectEpisodeVersion[];
  updated_at: string;
};

export type ProjectEpisodeVersion = {
  id: string;
  version_no: number;
  original_filename?: string | null;
  content_hash?: string | null;
  is_current?: boolean;
  created_at?: string | null;
};

export type CulturalContext = {
  context_code: string;
  name: string;
  region: string;
  era: string;
  world_type: "real_world" | "fictional_history" | "fictional_world";
  timeline: string;
  narrative_grammar?: string | null;
};

export type ProductionContentType = "animated_drama" | "live_action_drama" | "hybrid_drama";

export type ProjectProductionBrief = {
  schema_version: "ProjectProductionBrief.v1";
  content_type: ProductionContentType;
  delivery_aspect_ratio: "9:16" | "16:9";
  primary_style_id: string;
  style_catalog_version: string;
  cultural_contexts: CulturalContext[];
  primary_cultural_context_code: string;
  field_sources?: Record<string, string>;
};

export type EpisodeProductionBrief = {
  schema_version: "EpisodeProductionBrief.v1";
  target_duration_seconds: number;
  field_sources?: Record<string, string>;
};

export type ProductionBriefCatalog = {
  schema_version: string;
  catalog_version: string;
  style_catalog_version: string;
  content_types: Array<{ value: ProductionContentType; label: string; default_style_id?: string | null }>;
  delivery_aspect_ratios: Array<{
    value: "9:16" | "16:9";
    label: string;
    suggested_duration_seconds: number;
    opening_hook_seconds: number;
    rhythm_multiplier: number;
  }>;
  rhythm_profiles: Array<{ profile_id: string; label: string; base_shot_seconds: number; camera_rules: string[] }>;
  cultural_contexts: CulturalContext[];
  styles: ProductionStyleEntry[];
};

export type ProductionBriefImpact = {
  schema_version: string;
  skills: Array<{
    skill: string;
    skill_version: string;
    task: string;
    consumed_brief_fields: string[];
    prompt_sections: Record<string, string[]>;
    affected_output_paths: Record<string, string[]>;
    output_json_schema: Record<string, unknown>;
  }>;
};

export type ProductionStyleEntry = {
  style_id: string;
  label: string;
  medium: string;
  rendering: string;
  realism_level: string;
  description: string;
  prompt_tokens: string[];
  negative_tokens: string[];
};

export type ProductionStyleModifier = {
  modifier_id: string;
  label: string;
  group: string;
  prompt_tokens: string[];
};

export type ProductionStyleCatalog = {
  schema_version: string;
  catalog_version: string;
  max_modifiers: number;
  primary_styles: ProductionStyleEntry[];
  modifiers: ProductionStyleModifier[];
};

export type Task = {
  id: string;
  project_id: string;
  episode_id?: string | null;
  script_id?: string | null;
  script_version_id?: string | null;
  storyboard_id?: string | null;
  asset_id?: string | null;
  script_segment_id?: string | null;
  episode_code?: string | null;
  storyboard_code?: string | null;
  storyboard_dialogue?: string | null;
  storyboard_dialogue_back_translation?: string | null;
  storyboard_mirror_shots?: BreakdownMirrorShot[];
  media_type?: string | null;
  scene_code?: string | null;
  scene_name?: string | null;
  title: string;
  task_type: string;
  status: string;
  assignee_id?: string | null;
  assignee_name?: string | null;
  prompt_text?: string | null;
  latest_prompt_text?: string | null;
  due_at?: string | null;
  completed_at?: string | null;
  production_model?: string | null;
  asset_context_outdated?: boolean;
  task_variant?: string | null;
  age_stage_code?: string | null;
  costume_variant_code?: string | null;
  variant_plan_id?: string | null;
  variant_kind?: "scene_view" | "prop_state" | string | null;
  variant_title_zh?: string | null;
  variant_description_zh?: string | null;
  linked_storyboard_ids?: string[];
  depends_on_task_id?: string | null;
  is_retired?: boolean;
  retired_at?: string | null;
  retired_by?: string | null;
  retired_reason?: string | null;
  dependency_task_ids?: string[];
  dependency_asset_codes?: string[];
  dependency_summary?: {
    required: number;
    ready: number;
    pending: number;
  };
  dependency_assets?: DependencyAsset[];
  storyboard_duration_seconds?: number | null;
  keyframe_done?: boolean;
  video_done?: boolean;
  locked?: boolean;
  submissions: Submission[];
  submission_batches?: SubmissionBatch[];
  created_at?: string;
  updated_at?: string;
};

export type SceneGating = {
  scene_code?: string | null;
  scene_name?: string | null;
  required_asset_codes: string[];
  pending_asset_codes: string[];
  locked: boolean;
};

export type SubmissionCreatePayload = {
  file_id?: string | null;
  file_path: string;
  file_type: string;
  step?: string | null;
  prompt_text?: string | null;
  original_prompt_text?: string | null;
  revised_prompt_text?: string | null;
  view_label?: string | null;
  state_label?: string | null;
  description?: string | null;
  model_name?: string | null;
  tool_names: string[];
  submitted_by_id?: string | null;
};

export type Submission = SubmissionCreatePayload & {
  id: string;
  task_id: string;
  batch_id?: string | null;
  storyboard_id?: string | null;
  status: string;
  is_selected: boolean;
  is_primary: boolean;
  is_archived: boolean;
  source_master_submission_id?: string | null;
  is_invalidated?: boolean;
  invalidated_at?: string | null;
  invalidated_by_id?: string | null;
  invalidated_reason?: string | null;
  invalidated_source_id?: string | null;
  purged_at?: string | null;
  archived_by_id?: string | null;
  archived_at?: string | null;
  file_name?: string | null;
  mime_type?: string | null;
  file_size?: number | null;
  version_no?: number | null;
  media_type?: string | null;
  created_at?: string;
};

export type DependencyFile = {
  file_id: string;
  asset_version_id?: string | null;
  download_name?: string | null;
  canonical_display_name?: string | null;
  file_name?: string | null;
  mime_type?: string | null;
  file_size?: number | null;
  view_label?: string | null;
  submission_id?: string | null;
  step?: string | null;
};

export type DependencyAsset = {
  asset_id?: string | null;
  asset_code: string;
  asset_name?: string | null;
  asset_type?: string | null;
  required_for?: string | null;
  production_status?: string | null;
  approval_status?: string | null;
  ready: boolean;
  reason?: string | null;
  files: DependencyFile[];
};

export type AssetVariantPlan = {
  id: string;
  project_id: string;
  episode_id?: string | null;
  script_version_id?: string | null;
  asset_id: string;
  asset_code?: string | null;
  asset_name?: string | null;
  variant_code: string;
  task_code?: string | null;
  variant_kind: "scene_view" | "prop_state" | string;
  title_zh: string;
  description_zh: string;
  source: string;
  confidence?: number | null;
  status: "suggested" | "confirmed" | "retired" | string;
  task_id?: string | null;
  storyboard_ids: string[];
  storyboard_codes: string[];
  created_at?: string;
  updated_at?: string;
};

export type SubmissionBatch = {
  id: string;
  task_id: string;
  step: string;
  version_no: number;
  status: string;
  submitted_by_id?: string | null;
  submitted_at?: string | null;
  reviewed_by_id?: string | null;
  reviewed_at?: string | null;
  review_comment?: string | null;
  created_at?: string;
  updated_at?: string;
};

export type FileUploadResponse = {
  id: string;
  file_code: string;
  bucket: string;
  object_key: string;
  file_name: string;
  mime_type?: string;
  file_size?: number;
  url: string;
};

export type FilePlaybackResponse = {
  url: string;
  expires_at: string;
  expires_in_seconds: number;
  file_name?: string | null;
};

export type FileDownloadResponse = FilePlaybackResponse;

export type TaskCandidateUploadStatus = "queued" | "uploading" | "registering" | "success" | "failed";

export type TaskCandidateUploadUpdate = {
  file: File;
  progress: number;
  status: TaskCandidateUploadStatus;
  error?: string;
  uploaded?: FileUploadResponse;
  submission?: Submission;
};

export type TaskCandidateUploadResult = TaskCandidateUploadUpdate;

export type Asset = {
  id: string;
  project_id?: string | null;
  project_code?: string | null;
  episode_id?: string | null;
  episode_code?: string | null;
  scene_code?: string | null;
  scene_name?: string | null;
  storyboard_id?: string | null;
  storyboard_code?: string | null;
  media_type?: string | null;
  asset_code?: string | null;
  asset_type: string;
  name: string;
  description?: string | null;
  tags: string[];
  preview_path?: string | null;
  file_path?: string | null;
  prompt_text?: string | null;
  base_model?: string | null;
  metadata?: Record<string, unknown>;
  version: number;
  current_version_id?: string | null;
  versions?: AssetMediaVersion[];
  created_by_id?: string | null;
  created_at?: string;
  updated_at?: string;
};

export type ScriptSegment = {
  id: string;
  project_id: string;
  episode_id?: string | null;
  script_segment_code: string;
  episode_code?: string | null;
  order_no: number;
  title?: string | null;
  source_text: string;
  summary?: string | null;
  story_function?: string | null;
  dominant_emotion?: string | null;
  rhythm?: string | null;
  viewpoint?: string | null;
  context_code?: string | null;
  render_mode?: "animated" | "live_action" | string | null;
  metadata_json?: Record<string, unknown>;
  current_version_id?: string | null;
  created_at?: string;
  updated_at?: string;
};

export type BreakdownMirrorShot = {
  id?: string;
  label?: string;
  description?: string;
  camera?: string;
  shot_size?: string;
  duration_seconds?: number | null;
  dialogue?: string;
  dialogue_back_translation?: string;
  dialogue_language?: string;
  spoken_duration_seconds?: number | null;
  translation_status?: string;
  characters?: string[];
  shot_function?: "Establish" | "Reveal" | "Power" | "Pressure" | "Detail" | "Reaction" | "Shift" | "Impact" | "Aftermath" | "Exit" | string;
  movement_reason?: string;
  environmental_pressure?: string;
  micro_action?: string;
  sound_or_motif?: string;
  agent_id?: string;
  source_type?: string;
};

export type TemporaryAssetProductionPayload = {
  request_id: string;
  project_id: string;
  episode_id: string;
  script_version_id: string;
  asset_type: "character" | "scene" | "prop" | "music" | "voice_profile";
  name: string;
  description?: string | null;
  reason: string;
  production_requirement: string;
  task_variant: string;
};

export type TemporaryAssetProductionResponse = {
  asset: Asset;
  task: Task;
};

export type AssetMediaVersion = {
  id?: string;
  version_no?: number;
  batch_version_no?: number | null;
  file_id?: string | null;
  file_path?: string | null;
  file_name?: string | null;
  file_type?: string | null;
  media_type?: string | null;
  mime_type?: string | null;
  is_primary?: boolean;
  status?: string | null;
  model_name?: string | null;
  created_at?: string | null;
};

export type AssetLibraryHierarchy = {
  project_id?: string | null;
  project_prefix?: string | null;
  episode_id?: string | null;
  episode_code?: string | null;
  scene_code?: string | null;
  scene_name?: string | null;
  storyboard_id?: string | null;
  storyboard_code?: string | null;
  script_segment_id?: string | null;
  script_segment_code?: string | null;
  mirror_shot_code?: string | null;
  asset_id?: string | null;
  asset_code?: string | null;
  asset_type?: string | null;
  output_type: string;
  artifact_code: string;
  task_id?: string | null;
  depends_on_task_id?: string | null;
  task_type?: string | null;
  task_variant?: string | null;
  age_stage_code?: string | null;
  costume_variant_code?: string | null;
};

export type AssetLibraryVersion = {
  id: string;
  source_type: string;
  version_no: number;
  context_version_no?: number | null;
  standard_filename?: string | null;
  display_name?: string | null;
  canonical_display_name?: string | null;
  original_file_name?: string | null;
  file_id?: string | null;
  file_path?: string | null;
  mime_type?: string | null;
  file_size?: number | null;
  model_name?: string | null;
  tool_name?: string | null;
  batch_id?: string | null;
  batch_version_no?: number | null;
  status: string;
  is_primary: boolean;
  is_current: boolean;
  is_context_current: boolean;
  is_card_master: boolean;
  hierarchy: AssetLibraryHierarchy;
  created_at: string;
};

export type AssetLibraryItemResponse = {
  library_key: string;
  source_type: string;
  source_id: string;
  project_id?: string | null;
  project_code?: string | null;
  project_name?: string | null;
  episode_codes?: string[];
  source_label?: string | null;
  name: string;
  display_name?: string | null;
  description?: string | null;
  asset_type?: string | null;
  output_type: string;
  current_master?: AssetLibraryVersion | null;
  version_count: number;
  latest_version_at?: string | null;
  versions: AssetLibraryVersion[];
};

export type Storyboard = {
  id: string;
  project_id: string;
  script_segment_id?: string | null;
  episode_num: number;
  order_num: number;
  storyboard_code?: string | null;
  title?: string | null;
  description: string;
  dialogue?: string | null;
  dialogue_back_translation?: string | null;
  camera?: string | null;
  shot_type?: string | null;
  duration_seconds?: number | null;
  characters: string[];
  keyframes: Array<Record<string, unknown>>;
  mirror_shots?: BreakdownMirrorShot[];
  status: string;
  scene_code?: string | null;
  scene_name?: string | null;
  context_code?: string | null;
  render_mode?: "animated" | "live_action" | string | null;
};

export type User = {
  id: string;
  username: string;
  phone?: string | null;
  name?: string | null;
  display_name: string;
  role: UserRole;
  is_active?: boolean;
  avatar_url?: string | null;
  // TODO(总控看板-后端接口需求文档-1 需求4): 后端需在 /users 与 /users/:id 返回中补充 avatar_url: string | null。
  // 当前缺失：所有用户头像回退到 /admin-avatar.png，无法区分用户。
};

export type AgentRun = {
  id: string;
  project_id?: string | null;
  parent_run_id?: string | null;
  storyboard_id?: string | null;
  task_id?: string | null;
  agent_type: string;
  status: string;
  input?: Record<string, unknown>;
  duration_ms?: number;
  output?: Record<string, unknown> | null;
  error_message?: string | null;
  token_usage?: Record<string, unknown> | null;
  feedback_json?: Record<string, unknown> | null;
  model?: string | null;
  skill_name?: string | null;
  skill_version?: string | null;
  contract_version?: string | null;
  contract_hash?: string | null;
  prompt_version?: string | null;
  input_hash?: string | null;
  node_key?: string | null;
  workflow_run_id?: string | null;
  episode_id?: string | null;
  episode_code?: string | null;
  script_version_id?: string | null;
  summary?: Record<string, unknown>;
  payload_ref?: string | null;
  payload_size_bytes?: number | null;
  payload_sha256?: string | null;
  created_at?: string;
  updated_at?: string;
};

/** Summary pages omit input/output payloads; detail reads hydrate those fields. */
export type RunListPage<T> = {
  items: T[];
  next_cursor?: string | null;
  has_more?: boolean;
  limit?: number;
  total?: number | null;
};

export type AgentRunListResponse = RunListPage<AgentRun> | AgentRun[];

export type WorkflowProgressNode = {
  key: string;
  label: string;
  status: string;
  started_at?: string | null;
  finished_at?: string | null;
  duration_seconds?: number;
};

export type WorkflowProgress = {
  current_node?: string;
  current_label?: string;
  message?: string;
  completed?: number;
  total?: number;
  percent?: number;
  phase?: string;
  nodes?: WorkflowProgressNode[];
  updated_at?: string;
  current_duration_seconds?: number;
  elapsed_seconds?: number;
};

export type BreakdownDraftStoryboard = {
  storyboard_code?: string;
  script_segment_code?: string;
  script_segment_title?: string;
  script_segment_source_text?: string;
  script_segment_estimated_duration_seconds?: number;
  segment_order_num?: number;
  episode_code?: string;
  title?: string;
  description?: string;
  camera?: string;
  dialogue?: string;
  dialogue_back_translation?: string;
  dialogue_language?: string;
  spoken_duration_seconds?: number;
  translation_status?: string;
  characters?: string[];
  duration_seconds?: number;
  order_no?: number;
  order_num?: number;
  shot_no?: string;
  shot_size?: string;
  space?: string;
  scene_name?: string;
  scene_code?: string;
  task_type?: string;
  keyframe_code?: string;
  video_code?: string;
  mirror_shots?: BreakdownMirrorShot[];
};

export type BreakdownDraftScriptSegment = {
  id?: string;
  script_segment_code?: string;
  episode_code?: string | null;
  order_no?: number;
  title?: string | null;
  source_text?: string;
  summary?: string | null;
  story_function?: string | null;
  dominant_emotion?: string | null;
  rhythm?: string | null;
  viewpoint?: string | null;
  estimated_duration_seconds?: number | null;
  duration_seconds?: number | null;
  role_refs?: string[];
  scene_refs?: string[];
  prop_refs?: string[];
  characters?: string[];
  locations?: string[];
  production_notes?: string[];
  dialogue_lines?: string[];
  metadata?: Record<string, unknown>;
  status?: string;
};

export type AssetLifecycleStatus = "needs_completion" | "prompt_pending" | "pending_confirmation" | "confirmed";

export type BreakdownReviewState = {
  status: "needs_review" | "pending_confirmation" | "confirmed";
  confirmed_revision_id?: string | null;
  confirmation_mode?: string | null;
};

export type AssetNormalizationVersion = "AssetNormalization.v1";

export type AssetCodeState = "candidate" | "formal";

export type AssetPriority = "S" | "A" | "B" | "C";

export type AssetTypeV1 = "character" | "scene" | "prop";

export type CharacterAttributesV1 = {
  character_type: string | null;
  visual_presence: "on_screen" | "voice_only" | "mentioned_only";
  role: string | null;
  gender: string | null;
  age: string | null;
  appearance: string | null;
  core_requirement: string | null;
  personality: string | null;
  background: string | null;
  has_dialogue: boolean;
  dialogue_evidence: string[];
  context_codes: string[];
};

export type SceneAttributesV1 = {
  context_code: string | null;
  render_mode: "animated" | "live_action" | null;
  scene_no: string | null;
  interior_exterior: string | null;
  time: string | null;
  location: string | null;
  atmosphere: string | null;
  narrative_function: string | null;
  spatial_zones: string[];
  visual_goal: string | null;
  key_props: string[];
  key_prop_codes: string[];
  continuity_risks: string[];
  episode_code: string | null;
};

export type PropAttributesV1 = {
  level: string | null;
  prop_type: string | null;
  appearance: string | null;
  source: string | null;
  status_change: string | null;
  variant_states: string[];
  usage_scene: string | null;
  scene_description: string | null;
  context_codes: string[];
};

export type AssetAttributesV1 = CharacterAttributesV1 | SceneAttributesV1 | PropAttributesV1;

export type AssetRelationTarget = {
  target_asset_key: string;
  target_asset_code: string;
  target_display_code: string;
  target_display_label: string;
};

export type AssetNormalizationRelations = {
  relationship_text: string | null;
  scene_links: AssetRelationTarget[];
  owner_character: AssetRelationTarget | null;
  storyboard_codes: string[];
  script_segment_codes: string[];
  evidence: Record<string, unknown>[];
  confidence: "high" | "medium" | "low" | "unknown";
};

export type AssetNormalizationReviewItem = {
  review_id: string;
  asset_key: string;
  target_field: string;
  issue: string;
  suggestion: string | null;
  status: string;
  severity: "warning" | "error";
  requirement_level: "required" | "optional";
  source: string;
  source_path: string | null;
  human_input: string | null;
};

export type AssetNormalizationGlobalReviewItem = {
  review_id: string;
  target_scope: "inventory";
  target_field: string;
  issue: string;
  suggestion: string | null;
  status: string;
  severity: "warning" | "error";
  requirement_level: "required" | "optional";
  source: string;
  source_path: string | null;
  human_input: string | null;
};

export type AssetNormalizationSource = {
  source_kind: "agent" | "human" | "confirmed_inventory";
  agent_run_id: string | null;
  skill_name: string | null;
  raw_group: "assets" | "characters" | "scenes" | "props";
  raw_index: number;
};

export type AssetNormalizationRepair = {
  field: string;
  action: string;
  before: unknown;
  after: unknown;
  reason: string;
};

type AssetNormalizationBaseV1 = {
  normalization_version: AssetNormalizationVersion;
  client_asset_key: string;
  asset_code: string;
  display_code: string;
  display_label: string;
  code_state: AssetCodeState;
  name: string;
  description: string | null;
  priority: AssetPriority;
  relations: AssetNormalizationRelations;
  review_items: AssetNormalizationReviewItem[];
  source: AssetNormalizationSource;
  repairs: AssetNormalizationRepair[];
};

export type AssetNormalizationV1 = AssetNormalizationBaseV1 & (
  | { asset_type: "character"; attributes: CharacterAttributesV1 }
  | { asset_type: "scene"; attributes: SceneAttributesV1 }
  | { asset_type: "prop"; attributes: PropAttributesV1 }
);

export type BreakdownDraftAsset = AssetNormalizationV1;

export type AssetPromptRevision = {
  id: string;
  asset_id: string;
  version_no: number;
  context_key?: string;
  asset_prompt_input_fingerprint?: string;
  skill?: string;
  skill_version?: string;
  contract_version?: string;
  input_hash?: string;
  brief_trace?: BriefTrace;
  input_fields?: BriefTraceInputField[];
  resolved_production_brief?: Record<string, unknown>;
  production_context?: Record<string, unknown>;
  parent_revision_id?: string | null;
  supersedes_revision_id?: string | null;
  rerun_of_revision_id?: string | null;
  is_current?: boolean;
  prompt?: string;
  negative_prompt?: string;
  aspect_ratio?: string;
  view_prompts?: ProductionViewPrompt[];
  state_prompts?: AssetStatePrompt[];
  spatial_bible?: Record<string, unknown>;
  material_bible?: Record<string, unknown>;
  status?: string;
  created_at?: string;
};

export type AssetStatePrompt = {
  code: string;
  title?: string;
  prompt: string;
  negative_prompt?: string;
  description?: string;
  reference_requirement?: string;
};

export type BriefTrace = {
  schema_version?: string;
  skill?: string;
  consumed_fields?: string[];
  input_projection?: Record<string, unknown>;
  input_fields?: BriefTraceInputField[];
  prompt_sections?: Record<string, string[]>;
  affected_output_paths?: Record<string, string[]>;
  [key: string]: unknown;
};

export type BriefTraceInputField = {
  path: string;
  label?: string;
  source?: string;
  value?: unknown;
  prompt_sections?: string[];
  affected_output_paths?: string[];
};

export type ProductionViewPrompt = {
  code: string;
  title?: string;
  prompt: string;
  negative_prompt: string;
  pose?: string;
  description?: string;
  reference_requirement?: string;
  source_type?: "agent" | "backend_normalized" | "frontend_normalized";
  status?: "generated" | "deferred" | "available" | "locked" | string;
  generation_message?: string;
};

export type CharacterCostumeVariant = {
  variant_code: string;
  name: string;
  description?: string;
  costume_prop_codes?: string[];
  costume_prop_ids?: string[];
  costume_prop_names?: string[];
  costume_prop_snapshot?: string;
  scene_ids?: string[];
  scene_revision_ids?: string[];
  scene_names?: string[];
  prompt?: string;
  view_prompts?: ProductionViewPrompt[];
  generated_output_spec?: string | null;
  generated_view_codes?: string[];
  deferred_view_codes?: string[];
  generation_strategy?: string | null;
  status?: AssetLifecycleStatus;
};

export type CharacterAgeStage = {
  stage_code: string;
  name: string;
  age_range?: string;
  timeline?: string;
  scene_ids?: string[];
  scene_revision_ids?: string[];
  scene_names?: string[];
  face_changes?: string;
  body_changes?: string;
  hair_skin_changes?: string;
  identity_anchors?: string[];
  forbidden_changes?: string[];
  output_spec?: "A" | "A-E" | string;
  view_prompts?: ProductionViewPrompt[];
  generated_output_spec?: string | null;
  generated_view_codes?: string[];
  deferred_view_codes?: string[];
  generation_strategy?: string | null;
  status?: AssetLifecycleStatus;
  costume_variants?: CharacterCostumeVariant[];
};

export type SceneAssetOption = {
  scene_asset_id: string;
  scene_code: string;
  scene_name: string;
  episode_code?: string | null;
  revision_id?: string | null;
  status: string;
};

export type BreakdownManualReviewItem = {
  item: string;
  detail?: string;
  status?: string;
  severity?: string;
  source?: string;
  path?: string;
  code?: string;
  asset_code?: string;
  asset_name?: string;
  issue_type?: string;
  uncertainty?: string;
  suggestion?: string;
  target?: string;
  target_field?: string;
  missing_reason?: string;
  requirement_level?: "optional" | "required" | string;
  human_input?: string;
  applied_at?: string;
  match_status?: "exact" | "probable" | "conflict" | "not_found" | string;
  matched_prop_code?: string;
  matched_prop_name?: string;
  match_action?: "link_existing" | "manual_confirm" | "create_candidate" | string;
  match_confidence?: string;
  age_stage_code?: string;
  costume_variant_code?: string;
  selected_scene_ids?: string[];
  selected_scene_revision_ids?: string[];
  selected_scene_names?: string[];
  scope_type?: "storyboard" | "script_segment" | "episode" | string;
  scope_code?: string;
};

export type BreakdownDraftData = {
  scriptSegments: BreakdownDraftScriptSegment[];
  storyboards: BreakdownDraftStoryboard[];
  assets: BreakdownDraftAsset[];
  globalAssetReviewItems: AssetNormalizationGlobalReviewItem[];
  readingReport?: Record<string, unknown>;
  readingReviewState: BreakdownReviewState;
  assetReviewState: BreakdownReviewState;
  costumeDesigns: Record<string, unknown>[];
  costumeProcessingSummary: Record<string, unknown>[];
  assetPromptDesigns: Record<string, unknown>[];
  assetPromptProcessingSummary: Record<string, unknown>[];
  manualReviewItems: BreakdownManualReviewItem[];
  notes: string[];
  readingRevisionContext?: Record<string, unknown>;
  assetPromptFinalization?: Record<string, unknown>;
  // Optimistic-lock token: the ScriptBreakdown.version this draft was loaded from.
  // Sent back as expected_version on save so the backend can reject stale writes.
  version?: number;
};

export type AssetChangeApplicationItem = {
  proposal_index: number;
  candidate_code?: string | null;
  candidate_name?: string | null;
  action: string;
  decision: string;
  application_status: "created" | "updated" | "reused" | "rejected" | string;
  asset_id?: string | null;
  asset_code?: string | null;
  asset_revision_id?: string | null;
  message?: string | null;
};

export type AssetChangeApplication = {
  project_id: string;
  confirmed_reading_revision_id: string;
  application_revision_id: string;
  status: string;
  created_assets: number;
  updated_assets: number;
  reused_assets: number;
  rejected_proposals: number;
  items: AssetChangeApplicationItem[];
};

export type BreakdownDataState = "agent_raw" | "normalized" | "human_revision" | "final";

export type BreakdownView = Record<string, unknown> & {
  normalization_version: AssetNormalizationVersion;
  assets: AssetNormalizationV1[];
  global_review_items: AssetNormalizationGlobalReviewItem[];
};

export type BreakdownRead = {
  project_id: string;
  version: number;
  data_state: BreakdownDataState;
  view: BreakdownView;
  agent_run_id: string | null;
  updated_at: string;
};

export type AgentDefinition = {
  key: string;
  name: string;
  skill: string;
  description: string;
  stage: string;
  capabilities: string[];
  input_schema: Record<string, unknown>;
  output_schema: Record<string, unknown>;
  runtime_policy?: Record<string, unknown>;
  display_schema?: Record<string, unknown>;
  version?: string;
  enabled?: boolean;
};

export type AgentStageMapItem = {
  stage: string;
  agents: AgentDefinition[];
};

export type AgentMaterializeResponse = {
  run_id?: string;
  project_id?: string;
  agent_type?: string;
  message: string;
  created_script_segments: number;
  created_entity_versions: number;
  created_training_samples: number;
};

export type ScriptSegmentsConfirmResponse = {
  project_id: string;
  episode_code?: string | null;
  script_segment_count: number;
  breakdown_version: number;
  next_stage: string;
};

export type QueryAgentResponse = {
  question: string;
  answer: string;
  references: Array<{
    source_type: string;
    source_id: string;
    title: string;
    score: number;
    excerpt: string;
    metadata: Record<string, unknown>;
  }>;
  document_count: number;
  retrieval: Record<string, unknown>;
};

export type AuthSession = {
  token: string;
  user: User;
  tapcanvas_token: string;
  platform_token: string;
  session_expires_at: number;
};

export type LoginResponse = {
  access_token: string;
  user: User;
  tapcanvas_token: string;
  platform_token: string;
  session_expires_at: number;
};

export type NewUserForm = {
  display_name: string;
  phone: string;
  role: UserRole;
  password: string;
};

export type DashboardSummary = {
  project_id?: string;
  storyboard_count: number;
  asset_count: number;
  task_count: number;
  completed_task_count: number;
  overdue_task_count: number;
  completion_rate: number;
  recent_activity: string[];
};

export type WorkspaceResponse = {
  stats: { todo: number; completed: number; overdue: number; completion_rate: number };
  groups: { todo: Task[]; completed: Task[] };
};

export type AdminRecentReading = {
  id: string;
  name: string;
  meta: string;
  projectId: string;
  title: string;
  genre: string;
  episodeCode: string;
  status: string;
  created_at: string;
};

export type AdminOverview = {
  stats: Record<string, number>;
  user_count?: number;
  active_project_count?: number;
  total_task_count?: number;
  overdue_task_count?: number;
  asset_count?: number;
  submission_count?: number;
  agent_run_count?: number;
  overdue_tasks?: Array<Record<string, unknown>>;
  recent_activities?: string[];
  recent_readings?: AdminRecentReading[];
  // TODO(总控看板-后端接口需求文档-1 需求1): 后端补齐以下字段后收紧类型定义
  //   - monthly_completed_episodes / pending_items_count / team_creator_count
  //   - pending_items: AdminPendingItem[]
};

export type HermesConfig = {
  enabled: boolean;
  base_url: string;
  health_url: string;
  model: string;
  dashboard_url: string;
  webui_url: string;
  api_key_configured: boolean;
};

export type HermesHealth = {
  status: string;
  enabled: boolean;
  base_url: string;
  health_url: string;
  model: string;
  dashboard_url: string;
  webui_url: string;
  error?: string;
  models_error?: string;
};

export type HermesAuthPrompt = {
  tone: "ok" | "bad" | "warn";
  title: string;
  desc: string;
  action: string;
};

export type TaskPhaseKey = "asset_production" | "storyboard_production" | "editing";

export type NavGroup = {
  title: string;
  variant?: "submenu";
  showReviewBadge?: boolean;
  items: Array<{ key: PageKey; label: string; badge?: string; reviewOnly?: boolean }>;
};

export type UserNotification = {
  id: string;
  title: string;
  content: string;
  type: string;
  is_read: boolean;
  created_at: string;
};
