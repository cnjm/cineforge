-- ============================================================
-- CineForge 单库 baseline（单库 cineforge）
-- 决策已确认：users 唯一身份主；授权基线走 sys_ 前缀（sys_role / sys_casbin_rule）；
-- 画布域表统一以 canvas_ 前缀命名，与业务域区分。
-- 生成方式：schema 定稿后 pg_dump schema-only --no-owner --no-privileges 构建。
-- +goose Up

--
-- Name: agent_kind; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.agent_kind AS ENUM (
    'script_reading',
    'script_segmentation',
    'script_breakdown',
    'content_compliance_review',
    'asset_extract',
    'relation_check',
    'character_design_prompt',
    'scene_design_prompt',
    'prop_design_prompt',
    'text_to_image_prompt',
    'image_to_video_prompt',
    'asset_match',
    'asset_confirm',
    'video_prompt',
    'review',
    'query'
);


--
-- Name: agent_run_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.agent_run_status AS ENUM (
    'queued',
    'running',
    'succeeded',
    'failed'
);


--
-- Name: asset_list_item_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.asset_list_item_type AS ENUM (
    'character',
    'scene',
    'prop',
    'music',
    'voice_profile',
    'storyboard',
    'storyboard_image',
    'storyboard_video',
    'effect',
    'final_video'
);


--
-- Name: asset_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.asset_type AS ENUM (
    'character',
    'scene',
    'prop',
    'music',
    'voice_profile',
    'storyboard',
    'storyboard_image',
    'storyboard_video',
    'effect',
    'final_video'
);


--
-- Name: project_stage; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.project_stage AS ENUM (
    'draft',
    'reading',
    'breakdown_review',
    'asset_locking',
    'task_assignment',
    'locked',
    'production',
    'assembly',
    'completed',
    'archived'
);


--
-- Name: project_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.project_status AS ENUM (
    'draft',
    'reading',
    'breakdown_review',
    'asset_locking',
    'task_assignment',
    'locked',
    'production',
    'assembly',
    'completed',
    'archived'
);


--
-- Name: storyboard_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.storyboard_status AS ENUM (
    'todo',
    'in_progress',
    'submitted',
    'reviewing',
    'completed',
    'rejected',
    'overdue'
);


--
-- Name: task_status; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.task_status AS ENUM (
    'todo',
    'in_progress',
    'submitted',
    'reviewing',
    'completed',
    'rejected',
    'overdue'
);


--
-- Name: task_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.task_type AS ENUM (
    'script_breakdown',
    'asset_confirm',
    'asset',
    'audio',
    'text_to_image',
    'image_to_video',
    'storyboard_shot',
    'video_generation',
    'assembly',
    'final_output'
);


--
-- Name: user_role; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.user_role AS ENUM (
    'director',
    'script_editor',
    'artist',
    'editor',
    'admin'
);


SET default_table_access_method = heap;

--
-- Name: agent_feedback; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_feedback (
    id uuid NOT NULL,
    run_id uuid,
    task_id uuid,
    asset_id uuid,
    before_text text,
    after_text text,
    feedback_type character varying(60) NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: agent_issues; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_issues (
    id uuid NOT NULL,
    run_id uuid NOT NULL,
    level character varying(40) NOT NULL,
    code character varying(80) NOT NULL,
    message text NOT NULL,
    target_path character varying(255),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: agent_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_runs (
    id uuid NOT NULL,
    project_id uuid,
    parent_run_id uuid,
    workflow_run_id uuid,
    node_key character varying(120),
    episode_id uuid,
    script_id uuid,
    storyboard_id uuid,
    task_id uuid,
    agent_type public.agent_kind NOT NULL,
    status public.agent_run_status NOT NULL,
    input_json jsonb NOT NULL,
    output_json jsonb,
    summary_json jsonb,
    payload_ref character varying(512),
    payload_size_bytes integer,
    payload_sha256 character varying(64),
    error_message text,
    token_usage jsonb,
    duration_ms integer,
    feedback_json jsonb,
    version_no integer NOT NULL,
    model character varying(120),
    skill_name character varying(120),
    skill_version character varying(80),
    contract_version character varying(80),
    contract_hash character varying(64),
    prompt_version character varying(80),
    input_hash character varying(64),
    data_state character varying(40) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: agent_training_samples; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_training_samples (
    id uuid NOT NULL,
    project_id uuid,
    agent_type public.agent_kind,
    sample_type character varying(80) NOT NULL,
    entity_type character varying(80),
    entity_code character varying(120),
    input_json jsonb NOT NULL,
    agent_output_json jsonb NOT NULL,
    human_modified_output_json jsonb NOT NULL,
    confirmed_output_json jsonb NOT NULL,
    change_summary jsonb NOT NULL,
    quality_score integer,
    training_tags jsonb NOT NULL,
    training_ready boolean NOT NULL,
    created_by uuid,
    source_run_id uuid,
    payload_ref character varying(512),
    payload_size_bytes integer,
    payload_sha256 character varying(64),
    data_state character varying(40) NOT NULL,
    lineage_key character varying(200),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: artifact_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.artifact_revisions (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    artifact_type character varying(80) NOT NULL,
    artifact_id uuid NOT NULL,
    version_no integer NOT NULL,
    parent_revision_id uuid,
    source_type character varying(40) NOT NULL,
    source_run_id uuid,
    skill_name character varying(120),
    skill_version character varying(80),
    contract_version character varying(80),
    contract_hash character varying(64),
    model_name character varying(120),
    prompt_version character varying(80),
    input_hash character varying(64),
    input_snapshot jsonb NOT NULL,
    raw_output jsonb NOT NULL,
    normalized_content jsonb NOT NULL,
    change_diff jsonb NOT NULL,
    content_hash character varying(64) NOT NULL,
    status character varying(40) NOT NULL,
    created_by uuid,
    data_state character varying(40) NOT NULL,
    payload_ref character varying(512),
    payload_size_bytes integer,
    payload_sha256 character varying(64),
    idempotency_key character varying(64),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: asset_completion_records; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.asset_completion_records (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    asset_code character varying(80),
    age_stage_code character varying(20),
    costume_variant_code character varying(20),
    target_field character varying(120) NOT NULL,
    missing_reason character varying(120),
    original_uncertainty text,
    ai_suggestion text,
    human_description text,
    selected_scenes jsonb NOT NULL,
    correction_type character varying(60) NOT NULL,
    resolution_mode character varying(40) NOT NULL,
    status character varying(40) NOT NULL,
    source_agent_revision_id uuid,
    base_asset_revision_id uuid,
    applied_asset_revision_id uuid,
    downstream_status character varying(40) NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: asset_relations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.asset_relations (
    id uuid NOT NULL,
    asset_id uuid NOT NULL,
    project_id uuid,
    episode_id uuid,
    script_id uuid,
    storyboard_id uuid,
    image_output_id uuid,
    video_output_id uuid,
    final_output_id uuid,
    relation_type character varying(80) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: asset_storage_locations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.asset_storage_locations (
    id text NOT NULL,
    asset_id text NOT NULL,
    storage_type text NOT NULL,
    object_key text NOT NULL,
    url text,
    expires_at text,
    status text DEFAULT 'ready'::text NOT NULL,
    content_type text,
    size_bytes bigint,
    etag text,
    created_at text NOT NULL,
    updated_at text NOT NULL
);


--
-- Name: asset_variant_plans; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.asset_variant_plans (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    script_version_id uuid,
    asset_id uuid NOT NULL,
    variant_code character varying(20) NOT NULL,
    variant_kind character varying(40) NOT NULL,
    title_zh character varying(160) NOT NULL,
    description_zh text NOT NULL,
    source character varying(40) NOT NULL,
    confidence double precision,
    status character varying(40) NOT NULL,
    task_id uuid,
    created_by uuid,
    metadata_json jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: asset_variant_storyboard_links; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.asset_variant_storyboard_links (
    id uuid NOT NULL,
    variant_plan_id uuid NOT NULL,
    storyboard_id uuid NOT NULL,
    source character varying(40) NOT NULL,
    confidence double precision,
    status character varying(40) NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: asset_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.asset_versions (
    id uuid NOT NULL,
    asset_id uuid NOT NULL,
    version_no integer NOT NULL,
    asset_version_code character varying(100) NOT NULL,
    file_id uuid,
    preview_file_id uuid,
    prompt_text text,
    negative_prompt_text text,
    base_model character varying(120),
    tool_name character varying(120),
    metadata_json jsonb NOT NULL,
    is_current boolean NOT NULL,
    source_task_id uuid,
    source_agent_run_id uuid,
    created_by uuid,
    source_submission_id uuid,
    source_master_submission_id uuid,
    is_invalidated boolean NOT NULL,
    invalidated_at timestamp with time zone,
    invalidated_by_id uuid,
    invalidated_reason text,
    invalidated_source_id uuid,
    purged_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: assets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.assets (
    id uuid NOT NULL,
    project_id uuid,
    asset_code character varying(80),
    asset_type public.asset_type NOT NULL,
    name character varying(160) NOT NULL,
    description text,
    status character varying(40) NOT NULL,
    tags jsonb NOT NULL,
    preview_path character varying(512),
    file_path character varying(512),
    prompt_text text,
    base_model character varying(120),
    metadata_json jsonb NOT NULL,
    version integer NOT NULL,
    current_version_id uuid,
    current_revision_id uuid,
    is_locked boolean NOT NULL,
    locked_by uuid,
    locked_at timestamp with time zone,
    created_by_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: canvas_assets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canvas_assets (
    id text NOT NULL,
    name text NOT NULL,
    data text,
    owner_id text NOT NULL,
    project_id text,
    created_at text NOT NULL,
    updated_at text NOT NULL
);


--
-- Name: canvas_node_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canvas_node_outputs (
    id text NOT NULL,
    task_record_id text NOT NULL,
    flow_id text NOT NULL,
    node_id text NOT NULL,
    asset_id text NOT NULL,
    task_id text NOT NULL,
    media_type text NOT NULL,
    output_index integer NOT NULL,
    asset_name text,
    created_at text NOT NULL
);


--
-- Name: canvas_projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.canvas_projects (
    id text NOT NULL,
    name text NOT NULL,
    team_id text,
    owner_id text,
    managed_externally integer DEFAULT 0 NOT NULL,
    managed_by text,
    source_system text,
    external_project_id text,
    external_updated_at text,
    created_at text NOT NULL,
    updated_at text NOT NULL
);


--
-- Name: entity_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.entity_versions (
    id uuid NOT NULL,
    project_id uuid,
    entity_type character varying(80) NOT NULL,
    entity_id uuid,
    entity_code character varying(120) NOT NULL,
    version_no integer NOT NULL,
    version_code character varying(140) NOT NULL,
    source character varying(40) NOT NULL,
    status character varying(40) NOT NULL,
    content_json jsonb NOT NULL,
    change_summary text,
    changed_fields jsonb NOT NULL,
    base_version_id uuid,
    agent_run_id uuid,
    created_by uuid,
    is_current boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: episode_asset_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.episode_asset_bindings (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid NOT NULL,
    script_version_id uuid NOT NULL,
    confirmed_reading_revision_id uuid NOT NULL,
    application_revision_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    asset_revision_id uuid,
    proposal_index integer NOT NULL,
    action character varying(40) NOT NULL,
    status character varying(40) NOT NULL,
    age_stage_code character varying(20),
    costume_variant_code character varying(20),
    proposal_json jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: files; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.files (
    id uuid NOT NULL,
    file_code character varying(80),
    bucket character varying(120) NOT NULL,
    object_key character varying(512) NOT NULL,
    file_name character varying(255) NOT NULL,
    mime_type character varying(120),
    file_size integer,
    checksum character varying(128),
    uploaded_by uuid,
    project_id uuid,
    episode_id uuid,
    task_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: final_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.final_outputs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    task_id uuid,
    asset_id uuid,
    final_code character varying(100) NOT NULL,
    name character varying(160) NOT NULL,
    file_id uuid,
    tool_names jsonb NOT NULL,
    status character varying(40) NOT NULL,
    version_no integer NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: flows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.flows (
    id text NOT NULL,
    name text NOT NULL,
    data text NOT NULL,
    owner_id text NOT NULL,
    project_id text NOT NULL,
    episode_id text,
    revision integer DEFAULT 1 NOT NULL,
    managed_by text,
    deleted_at text,
    created_at text NOT NULL,
    updated_at text NOT NULL
);


--
-- Name: image_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.image_outputs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    script_id uuid,
    storyboard_id uuid,
    task_id uuid,
    asset_id uuid,
    image_code character varying(100) NOT NULL,
    file_id uuid,
    prompt_text text,
    model_name character varying(120),
    status character varying(40) NOT NULL,
    version_no integer NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: notifications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notifications (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    title character varying(160) NOT NULL,
    content text NOT NULL,
    type character varying(60) NOT NULL,
    is_read boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: operation_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.operation_logs (
    id uuid NOT NULL,
    operator_id uuid,
    project_id uuid,
    target_type character varying(80) NOT NULL,
    target_id uuid,
    action character varying(80) NOT NULL,
    detail jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: permissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.permissions (
    id uuid NOT NULL,
    user_id uuid NOT NULL,
    project_id uuid,
    permission_type character varying(80) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: project_asset_list_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_asset_list_items (
    id uuid NOT NULL,
    asset_list_id uuid NOT NULL,
    project_id uuid NOT NULL,
    asset_id uuid,
    asset_type public.asset_list_item_type NOT NULL,
    asset_code character varying(80),
    name character varying(160) NOT NULL,
    description text,
    metadata_json jsonb NOT NULL,
    source_text text,
    episode_id uuid,
    script_id uuid,
    script_segment_id uuid,
    storyboard_id uuid,
    status character varying(40) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: project_asset_lists; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_asset_lists (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    source_script_id uuid,
    source_agent_run_id uuid,
    version_no integer NOT NULL,
    status character varying(40) NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: project_assets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_assets (
    project_id uuid NOT NULL,
    asset_id uuid NOT NULL,
    relation character varying(80) NOT NULL,
    storyboard_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: project_episodes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.project_episodes (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_no integer NOT NULL,
    episode_code character varying(16) NOT NULL,
    title character varying(160),
    summary text,
    production_brief jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projects (
    id uuid NOT NULL,
    project_no character varying(32),
    project_prefix character varying(5),
    name character varying(160),
    title character varying(160) NOT NULL,
    genre character varying(80),
    style character varying(120),
    status public.project_status NOT NULL,
    current_stage public.project_stage NOT NULL,
    manager_id uuid,
    created_by_id uuid,
    script_text text,
    production_brief jsonb,
    locked_at timestamp with time zone,
    archived_at timestamp with time zone,
    deleted_at timestamp with time zone,
    deleted_by_id uuid,
    delete_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: cineforge_task_canvas_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cineforge_task_canvas_bindings (
    id text NOT NULL,
    cineforge_project_id text NOT NULL,
    cineforge_task_id text NOT NULL,
    cineforge_episode_id text,
    flow_id text NOT NULL,
    created_by_user_id text NOT NULL,
    task_title_snapshot text NOT NULL,
    task_status_snapshot text NOT NULL,
    task_updated_at text,
    group_key text DEFAULT ''::text NOT NULL,
    group_type text DEFAULT ''::text NOT NULL,
    created_at text NOT NULL,
    updated_at text NOT NULL
);


--
-- Name: cineforge_task_canvas_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cineforge_task_canvas_members (
    group_key text NOT NULL,
    cineforge_task_id text NOT NULL,
    role text NOT NULL,
    created_at text NOT NULL,
    updated_at text NOT NULL
);


--
-- Name: script_breakdowns; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.script_breakdowns (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    version integer NOT NULL,
    content_json jsonb NOT NULL,
    agent_run_id uuid,
    edited_by_id uuid,
    data_state character varying(40) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: script_segments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.script_segments (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    script_segment_code character varying(80) NOT NULL,
    episode_code character varying(32),
    order_no integer NOT NULL,
    title character varying(160),
    source_text text NOT NULL,
    summary text,
    story_function text,
    dominant_emotion text,
    rhythm text,
    viewpoint text,
    context_code character varying(48),
    render_mode character varying(48),
    metadata_json jsonb NOT NULL,
    current_version_id uuid,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: script_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.script_versions (
    id uuid NOT NULL,
    script_id uuid NOT NULL,
    version_no integer NOT NULL,
    source character varying(40) NOT NULL,
    content text NOT NULL,
    content_hash character varying(64),
    source_file_id uuid,
    original_filename character varying(255),
    parser_name character varying(80),
    language character varying(20),
    source_agent_run_id uuid,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: scripts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.scripts (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    script_code character varying(80) NOT NULL,
    title character varying(160),
    content text NOT NULL,
    status character varying(40) NOT NULL,
    current_version_id uuid,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: storyboard_versions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.storyboard_versions (
    id uuid NOT NULL,
    storyboard_id uuid NOT NULL,
    version_no integer NOT NULL,
    source character varying(40) NOT NULL,
    source_text text,
    description text,
    dialogue text,
    narration text,
    camera text,
    shot_type character varying(80),
    duration_seconds integer,
    asset_snapshot jsonb NOT NULL,
    source_agent_run_id uuid,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: storyboards; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.storyboards (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_num integer NOT NULL,
    order_num integer NOT NULL,
    title character varying(160),
    description text NOT NULL,
    dialogue text,
    camera text,
    duration_seconds integer,
    characters jsonb NOT NULL,
    keyframes jsonb NOT NULL,
    mirror_shots jsonb NOT NULL,
    status public.storyboard_status NOT NULL,
    storyboard_code character varying(64),
    scene_code character varying(80),
    scene_name character varying(160),
    context_code character varying(48),
    render_mode character varying(48),
    script_id uuid,
    script_segment_id uuid,
    narration text,
    shot_type character varying(80),
    current_version_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: submissions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.submissions (
    id uuid NOT NULL,
    task_id uuid NOT NULL,
    batch_id uuid,
    file_id uuid,
    storyboard_id uuid,
    file_path character varying(512) NOT NULL,
    file_type character varying(40) NOT NULL,
    step character varying(20),
    prompt_text text,
    original_prompt_text text,
    revised_prompt_text text,
    view_label character varying(120),
    state_label character varying(120),
    description text,
    model_name character varying(120),
    tool_names jsonb NOT NULL,
    submitted_by_id uuid,
    status character varying(30) NOT NULL,
    is_selected boolean NOT NULL,
    is_primary boolean NOT NULL,
    is_archived boolean NOT NULL,
    archived_by_id uuid,
    archived_at timestamp with time zone,
    source_master_submission_id uuid,
    is_invalidated boolean NOT NULL,
    invalidated_at timestamp with time zone,
    invalidated_by_id uuid,
    invalidated_reason text,
    invalidated_source_id uuid,
    purged_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sys_casbin_rule; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sys_casbin_rule (
    id bigint NOT NULL,
    ptype character varying(512) DEFAULT ''::character varying NOT NULL,
    v0 character varying(512) DEFAULT ''::character varying NOT NULL,
    v1 character varying(512) DEFAULT ''::character varying NOT NULL,
    v2 character varying(512) DEFAULT ''::character varying NOT NULL,
    v3 character varying(512) DEFAULT ''::character varying NOT NULL,
    v4 character varying(512) DEFAULT ''::character varying NOT NULL,
    v5 character varying(512) DEFAULT ''::character varying NOT NULL
);


--
-- Name: sys_casbin_rule_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sys_casbin_rule_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sys_casbin_rule_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sys_casbin_rule_id_seq OWNED BY public.sys_casbin_rule.id;


--
-- Name: sys_role; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sys_role (
    role_id bigint NOT NULL,
    role_name character varying(128) NOT NULL,
    status character varying(4) NOT NULL,
    role_key character varying(128) NOT NULL,
    role_sort integer DEFAULT 0 NOT NULL,
    flag character varying(128) DEFAULT ''::character varying NOT NULL,
    remark character varying(255) DEFAULT ''::character varying NOT NULL,
    admin boolean DEFAULT false NOT NULL,
    data_scope character varying(128) DEFAULT ''::character varying NOT NULL,
    create_by bigint DEFAULT 0 NOT NULL,
    update_by bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: sys_role_role_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sys_role_role_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sys_role_role_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sys_role_role_id_seq OWNED BY public.sys_role.role_id;


--
-- Name: task_candidate_imports; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_candidate_imports (
    id uuid NOT NULL,
    idempotency_key uuid NOT NULL,
    task_id uuid NOT NULL,
    submission_id uuid,
    source_system character varying(32) DEFAULT 'react-flow'::character varying NOT NULL,
    source_project_id character varying(128) NOT NULL,
    source_flow_id character varying(128) NOT NULL,
    source_node_id character varying(128) NOT NULL,
    source_asset_id character varying(128) NOT NULL,
    source_output_index integer NOT NULL,
    source_sha256 character varying(64),
    source_content_type character varying(120),
    source_size_bytes bigint,
    status character varying(20) DEFAULT 'importing'::character varying NOT NULL,
    error_code character varying(120),
    created_by uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    step character varying(20) DEFAULT 'result'::character varying NOT NULL,
    CONSTRAINT ck_task_candidate_imports_completed_metadata CHECK ((((status)::text <> 'completed'::text) OR ((source_sha256 IS NOT NULL) AND (source_content_type IS NOT NULL) AND (source_size_bytes IS NOT NULL))))
);


--
-- Name: task_dependencies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_dependencies (
    id uuid NOT NULL,
    task_id uuid NOT NULL,
    depends_on_task_id uuid NOT NULL,
    dependency_type character varying(40) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: task_prompts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_prompts (
    id uuid NOT NULL,
    task_id uuid NOT NULL,
    prompt_type character varying(60) NOT NULL,
    prompt_text text NOT NULL,
    version_no integer NOT NULL,
    source character varying(40) NOT NULL,
    copied_at timestamp with time zone,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: task_submission_batches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_submission_batches (
    id uuid NOT NULL,
    task_id uuid NOT NULL,
    step character varying(20) NOT NULL,
    version_no integer NOT NULL,
    status character varying(30) NOT NULL,
    submitted_by_id uuid,
    submitted_at timestamp with time zone,
    reviewed_by_id uuid,
    reviewed_at timestamp with time zone,
    review_comment text,
    submit_request_id uuid,
    review_request_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tasks (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    script_id uuid,
    script_version_id uuid,
    idempotency_key character varying(64),
    script_segment_id uuid,
    storyboard_id uuid,
    asset_id uuid,
    scene_code character varying(80),
    scene_name character varying(160),
    task_type public.task_type NOT NULL,
    title character varying(160) NOT NULL,
    assignee_id uuid,
    assigned_by uuid,
    assigned_at timestamp with time zone,
    status public.task_status NOT NULL,
    prompt_text text,
    latest_prompt_text text,
    due_at timestamp with time zone,
    completed_at timestamp with time zone,
    visible_until timestamp with time zone,
    production_model character varying(120),
    media_type character varying(40),
    task_variant character varying(20),
    age_stage_code character varying(20),
    costume_variant_code character varying(20),
    variant_plan_id uuid,
    variant_kind character varying(40),
    variant_title_zh character varying(160),
    variant_description_zh text,
    prompt_revision_id uuid,
    asset_context_outdated boolean NOT NULL,
    depends_on_task_id uuid,
    is_retired boolean NOT NULL,
    retired_at timestamp with time zone,
    retired_by uuid,
    retired_reason text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid NOT NULL,
    username character varying(64) NOT NULL,
    phone character varying(20),
    name character varying(80),
    display_name character varying(80) NOT NULL,
    role public.user_role NOT NULL,
    password_hash character varying(255),
    is_active boolean NOT NULL,
    sync_version bigint DEFAULT '0'::bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: video_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.video_outputs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    episode_id uuid,
    script_id uuid,
    storyboard_id uuid,
    task_id uuid,
    asset_id uuid,
    video_code character varying(100) NOT NULL,
    video_type character varying(40) NOT NULL,
    file_id uuid,
    prompt_text text,
    model_name character varying(120),
    tool_name character varying(120),
    status character varying(40) NOT NULL,
    version_no integer NOT NULL,
    created_by uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: workflow_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workflow_runs (
    id uuid NOT NULL,
    project_id uuid NOT NULL,
    agent_run_id uuid,
    workflow_type character varying(80) NOT NULL,
    status character varying(40) NOT NULL,
    node_status_json jsonb NOT NULL,
    result_json jsonb NOT NULL,
    summary_json jsonb,
    payload_ref character varying(512),
    payload_size_bytes integer,
    payload_sha256 character varying(64),
    error_json jsonb NOT NULL,
    input_json jsonb NOT NULL,
    created_by uuid,
    lock_key character varying(240),
    lock_token character varying(120),
    idempotency_key character varying(64),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sys_casbin_rule id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sys_casbin_rule ALTER COLUMN id SET DEFAULT nextval('public.sys_casbin_rule_id_seq'::regclass);


--
-- Name: sys_role role_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sys_role ALTER COLUMN role_id SET DEFAULT nextval('public.sys_role_role_id_seq'::regclass);


--
-- Name: agent_feedback agent_feedback_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_feedback
    ADD CONSTRAINT agent_feedback_pkey PRIMARY KEY (id);


--
-- Name: agent_issues agent_issues_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_issues
    ADD CONSTRAINT agent_issues_pkey PRIMARY KEY (id);


--
-- Name: agent_runs agent_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT agent_runs_pkey PRIMARY KEY (id);


--
-- Name: agent_training_samples agent_training_samples_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_training_samples
    ADD CONSTRAINT agent_training_samples_pkey PRIMARY KEY (id);


--
-- Name: artifact_revisions artifact_revisions_artifact_type_artifact_id_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_artifact_type_artifact_id_version_no_key UNIQUE (artifact_type, artifact_id, version_no);


--
-- Name: artifact_revisions artifact_revisions_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_idempotency_key_key UNIQUE (idempotency_key);


--
-- Name: artifact_revisions artifact_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_pkey PRIMARY KEY (id);


--
-- Name: asset_completion_records asset_completion_records_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_pkey PRIMARY KEY (id);


--
-- Name: asset_relations asset_relations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_pkey PRIMARY KEY (id);


--
-- Name: asset_storage_locations asset_storage_locations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_storage_locations
    ADD CONSTRAINT asset_storage_locations_pkey PRIMARY KEY (id);


--
-- Name: asset_variant_plans asset_variant_plans_asset_id_episode_id_variant_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_asset_id_episode_id_variant_code_key UNIQUE (asset_id, episode_id, variant_code);


--
-- Name: asset_variant_plans asset_variant_plans_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_pkey PRIMARY KEY (id);


--
-- Name: asset_variant_storyboard_links asset_variant_storyboard_link_variant_plan_id_storyboard_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_storyboard_links
    ADD CONSTRAINT asset_variant_storyboard_link_variant_plan_id_storyboard_id_key UNIQUE (variant_plan_id, storyboard_id);


--
-- Name: asset_variant_storyboard_links asset_variant_storyboard_links_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_storyboard_links
    ADD CONSTRAINT asset_variant_storyboard_links_pkey PRIMARY KEY (id);


--
-- Name: asset_versions asset_versions_asset_id_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_asset_id_version_no_key UNIQUE (asset_id, version_no);


--
-- Name: asset_versions asset_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_pkey PRIMARY KEY (id);


--
-- Name: assets assets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assets
    ADD CONSTRAINT assets_pkey PRIMARY KEY (id);


--
-- Name: canvas_assets canvas_assets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canvas_assets
    ADD CONSTRAINT canvas_assets_pkey PRIMARY KEY (id);


--
-- Name: canvas_node_outputs canvas_node_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canvas_node_outputs
    ADD CONSTRAINT canvas_node_outputs_pkey PRIMARY KEY (id);


--
-- Name: canvas_projects canvas_projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.canvas_projects
    ADD CONSTRAINT canvas_projects_pkey PRIMARY KEY (id);


--
-- Name: entity_versions entity_versions_entity_type_entity_code_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_versions
    ADD CONSTRAINT entity_versions_entity_type_entity_code_version_no_key UNIQUE (entity_type, entity_code, version_no);


--
-- Name: entity_versions entity_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_versions
    ADD CONSTRAINT entity_versions_pkey PRIMARY KEY (id);


--
-- Name: episode_asset_bindings episode_asset_bindings_application_revision_id_proposal_ind_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_application_revision_id_proposal_ind_key UNIQUE (application_revision_id, proposal_index);


--
-- Name: episode_asset_bindings episode_asset_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_pkey PRIMARY KEY (id);


--
-- Name: files files_object_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.files
    ADD CONSTRAINT files_object_key_key UNIQUE (object_key);


--
-- Name: files files_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.files
    ADD CONSTRAINT files_pkey PRIMARY KEY (id);


--
-- Name: final_outputs final_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_pkey PRIMARY KEY (id);


--
-- Name: flows flows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.flows
    ADD CONSTRAINT flows_pkey PRIMARY KEY (id);


--
-- Name: image_outputs image_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_pkey PRIMARY KEY (id);


--
-- Name: notifications notifications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_pkey PRIMARY KEY (id);


--
-- Name: operation_logs operation_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operation_logs
    ADD CONSTRAINT operation_logs_pkey PRIMARY KEY (id);


--
-- Name: permissions permissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_pkey PRIMARY KEY (id);


--
-- Name: permissions permissions_user_id_project_id_permission_type_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_user_id_project_id_permission_type_key UNIQUE (user_id, project_id, permission_type);


--
-- Name: project_asset_list_items project_asset_list_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_pkey PRIMARY KEY (id);


--
-- Name: project_asset_lists project_asset_lists_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_lists
    ADD CONSTRAINT project_asset_lists_pkey PRIMARY KEY (id);


--
-- Name: project_asset_lists project_asset_lists_project_id_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_lists
    ADD CONSTRAINT project_asset_lists_project_id_version_no_key UNIQUE (project_id, version_no);


--
-- Name: project_assets project_assets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_assets
    ADD CONSTRAINT project_assets_pkey PRIMARY KEY (project_id, asset_id);


--
-- Name: project_assets project_assets_project_id_asset_id_relation_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_assets
    ADD CONSTRAINT project_assets_project_id_asset_id_relation_key UNIQUE (project_id, asset_id, relation);


--
-- Name: project_episodes project_episodes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_episodes
    ADD CONSTRAINT project_episodes_pkey PRIMARY KEY (id);


--
-- Name: project_episodes project_episodes_project_id_episode_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_episodes
    ADD CONSTRAINT project_episodes_project_id_episode_no_key UNIQUE (project_id, episode_no);


--
-- Name: projects projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (id);


--
-- Name: cineforge_task_canvas_bindings cineforge_task_canvas_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cineforge_task_canvas_bindings
    ADD CONSTRAINT cineforge_task_canvas_bindings_pkey PRIMARY KEY (id);


--
-- Name: cineforge_task_canvas_members cineforge_task_canvas_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cineforge_task_canvas_members
    ADD CONSTRAINT cineforge_task_canvas_members_pkey PRIMARY KEY (group_key, cineforge_task_id);


--
-- Name: script_breakdowns script_breakdowns_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_breakdowns
    ADD CONSTRAINT script_breakdowns_pkey PRIMARY KEY (id);


--
-- Name: script_breakdowns script_breakdowns_project_id_version_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_breakdowns
    ADD CONSTRAINT script_breakdowns_project_id_version_key UNIQUE (project_id, version);


--
-- Name: script_segments script_segments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_segments
    ADD CONSTRAINT script_segments_pkey PRIMARY KEY (id);


--
-- Name: script_segments script_segments_project_id_script_segment_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_segments
    ADD CONSTRAINT script_segments_project_id_script_segment_code_key UNIQUE (project_id, script_segment_code);


--
-- Name: script_versions script_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_versions
    ADD CONSTRAINT script_versions_pkey PRIMARY KEY (id);


--
-- Name: script_versions script_versions_script_id_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_versions
    ADD CONSTRAINT script_versions_script_id_version_no_key UNIQUE (script_id, version_no);


--
-- Name: scripts scripts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scripts
    ADD CONSTRAINT scripts_pkey PRIMARY KEY (id);


--
-- Name: storyboard_versions storyboard_versions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboard_versions
    ADD CONSTRAINT storyboard_versions_pkey PRIMARY KEY (id);


--
-- Name: storyboard_versions storyboard_versions_storyboard_id_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboard_versions
    ADD CONSTRAINT storyboard_versions_storyboard_id_version_no_key UNIQUE (storyboard_id, version_no);


--
-- Name: storyboards storyboards_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboards
    ADD CONSTRAINT storyboards_pkey PRIMARY KEY (id);


--
-- Name: storyboards storyboards_project_id_episode_num_order_num_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboards
    ADD CONSTRAINT storyboards_project_id_episode_num_order_num_key UNIQUE (project_id, episode_num, order_num);


--
-- Name: submissions submissions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_pkey PRIMARY KEY (id);


--
-- Name: sys_casbin_rule sys_casbin_rule_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sys_casbin_rule
    ADD CONSTRAINT sys_casbin_rule_pkey PRIMARY KEY (id);


--
-- Name: sys_role sys_role_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sys_role
    ADD CONSTRAINT sys_role_pkey PRIMARY KEY (role_id);


--
-- Name: task_candidate_imports task_candidate_imports_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_candidate_imports
    ADD CONSTRAINT task_candidate_imports_idempotency_key_key UNIQUE (idempotency_key);


--
-- Name: task_candidate_imports task_candidate_imports_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_candidate_imports
    ADD CONSTRAINT task_candidate_imports_pkey PRIMARY KEY (id);


--
-- Name: task_candidate_imports task_candidate_imports_submission_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_candidate_imports
    ADD CONSTRAINT task_candidate_imports_submission_id_key UNIQUE (submission_id);


--
-- Name: task_dependencies task_dependencies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_dependencies
    ADD CONSTRAINT task_dependencies_pkey PRIMARY KEY (id);


--
-- Name: task_dependencies task_dependencies_task_id_depends_on_task_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_dependencies
    ADD CONSTRAINT task_dependencies_task_id_depends_on_task_id_key UNIQUE (task_id, depends_on_task_id);


--
-- Name: task_prompts task_prompts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_prompts
    ADD CONSTRAINT task_prompts_pkey PRIMARY KEY (id);


--
-- Name: task_prompts task_prompts_task_id_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_prompts
    ADD CONSTRAINT task_prompts_task_id_version_no_key UNIQUE (task_id, version_no);


--
-- Name: task_submission_batches task_submission_batches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_submission_batches
    ADD CONSTRAINT task_submission_batches_pkey PRIMARY KEY (id);


--
-- Name: task_submission_batches task_submission_batches_task_id_step_version_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_submission_batches
    ADD CONSTRAINT task_submission_batches_task_id_step_version_no_key UNIQUE (task_id, step, version_no);


--
-- Name: tasks tasks_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_idempotency_key_key UNIQUE (idempotency_key);


--
-- Name: tasks tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: video_outputs video_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_pkey PRIMARY KEY (id);


--
-- Name: workflow_runs workflow_runs_idempotency_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_runs
    ADD CONSTRAINT workflow_runs_idempotency_key_key UNIQUE (idempotency_key);


--
-- Name: workflow_runs workflow_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_runs
    ADD CONSTRAINT workflow_runs_pkey PRIMARY KEY (id);


--
-- Name: idx_cineforge_bindings_flow_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_cineforge_bindings_flow_unique ON public.cineforge_task_canvas_bindings USING btree (flow_id);


--
-- Name: idx_cineforge_bindings_task_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_cineforge_bindings_task_unique ON public.cineforge_task_canvas_bindings USING btree (cineforge_task_id);


--
-- Name: ix_agent_feedback_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_feedback_run_id ON public.agent_feedback USING btree (run_id);


--
-- Name: ix_agent_feedback_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_feedback_task_id ON public.agent_feedback USING btree (task_id);


--
-- Name: ix_agent_issues_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_issues_run_id ON public.agent_issues USING btree (run_id);


--
-- Name: ix_agent_runs_agent_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_agent_type ON public.agent_runs USING btree (agent_type);


--
-- Name: ix_agent_runs_contract_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_contract_hash ON public.agent_runs USING btree (contract_hash);


--
-- Name: ix_agent_runs_data_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_data_state ON public.agent_runs USING btree (data_state);


--
-- Name: ix_agent_runs_input_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_input_hash ON public.agent_runs USING btree (input_hash);


--
-- Name: ix_agent_runs_node_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_node_key ON public.agent_runs USING btree (node_key);


--
-- Name: ix_agent_runs_parent_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_parent_run_id ON public.agent_runs USING btree (parent_run_id);


--
-- Name: ix_agent_runs_project_created_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_project_created_id ON public.agent_runs USING btree (project_id, created_at, id);


--
-- Name: ix_agent_runs_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_project_id ON public.agent_runs USING btree (project_id);


--
-- Name: ix_agent_runs_skill_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_skill_name ON public.agent_runs USING btree (skill_name);


--
-- Name: ix_agent_runs_workflow_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_runs_workflow_run_id ON public.agent_runs USING btree (workflow_run_id);


--
-- Name: ix_agent_training_samples_agent_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_agent_type ON public.agent_training_samples USING btree (agent_type);


--
-- Name: ix_agent_training_samples_data_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_data_state ON public.agent_training_samples USING btree (data_state);


--
-- Name: ix_agent_training_samples_entity_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_entity_code ON public.agent_training_samples USING btree (entity_code);


--
-- Name: ix_agent_training_samples_entity_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_entity_type ON public.agent_training_samples USING btree (entity_type);


--
-- Name: ix_agent_training_samples_lineage_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_lineage_key ON public.agent_training_samples USING btree (lineage_key);


--
-- Name: ix_agent_training_samples_payload_sha256; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_payload_sha256 ON public.agent_training_samples USING btree (payload_sha256);


--
-- Name: ix_agent_training_samples_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_project_id ON public.agent_training_samples USING btree (project_id);


--
-- Name: ix_agent_training_samples_sample_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_sample_type ON public.agent_training_samples USING btree (sample_type);


--
-- Name: ix_agent_training_samples_source_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_agent_training_samples_source_run_id ON public.agent_training_samples USING btree (source_run_id);


--
-- Name: ix_artifact_revisions_artifact; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_artifact ON public.artifact_revisions USING btree (artifact_type, artifact_id);


--
-- Name: ix_artifact_revisions_artifact_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_artifact_id ON public.artifact_revisions USING btree (artifact_id);


--
-- Name: ix_artifact_revisions_artifact_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_artifact_type ON public.artifact_revisions USING btree (artifact_type);


--
-- Name: ix_artifact_revisions_content_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_content_hash ON public.artifact_revisions USING btree (content_hash);


--
-- Name: ix_artifact_revisions_contract_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_contract_hash ON public.artifact_revisions USING btree (contract_hash);


--
-- Name: ix_artifact_revisions_data_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_data_state ON public.artifact_revisions USING btree (data_state);


--
-- Name: ix_artifact_revisions_input_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_input_hash ON public.artifact_revisions USING btree (input_hash);


--
-- Name: ix_artifact_revisions_payload_sha256; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_payload_sha256 ON public.artifact_revisions USING btree (payload_sha256);


--
-- Name: ix_artifact_revisions_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_project_id ON public.artifact_revisions USING btree (project_id);


--
-- Name: ix_artifact_revisions_source_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_source_type ON public.artifact_revisions USING btree (source_type);


--
-- Name: ix_artifact_revisions_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_artifact_revisions_status ON public.artifact_revisions USING btree (status);


--
-- Name: ix_asset_completion_records_age_stage_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_age_stage_code ON public.asset_completion_records USING btree (age_stage_code);


--
-- Name: ix_asset_completion_records_asset_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_asset_code ON public.asset_completion_records USING btree (asset_code);


--
-- Name: ix_asset_completion_records_asset_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_asset_id ON public.asset_completion_records USING btree (asset_id);


--
-- Name: ix_asset_completion_records_costume_variant_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_costume_variant_code ON public.asset_completion_records USING btree (costume_variant_code);


--
-- Name: ix_asset_completion_records_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_project_id ON public.asset_completion_records USING btree (project_id);


--
-- Name: ix_asset_completion_records_resolution_mode; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_resolution_mode ON public.asset_completion_records USING btree (resolution_mode);


--
-- Name: ix_asset_completion_records_scope; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_scope ON public.asset_completion_records USING btree (asset_code, age_stage_code, costume_variant_code);


--
-- Name: ix_asset_completion_records_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_status ON public.asset_completion_records USING btree (status);


--
-- Name: ix_asset_completion_records_target_field; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_completion_records_target_field ON public.asset_completion_records USING btree (target_field);


--
-- Name: ix_asset_relations_asset_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_relations_asset_id ON public.asset_relations USING btree (asset_id);


--
-- Name: ix_asset_relations_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_relations_project_id ON public.asset_relations USING btree (project_id);


--
-- Name: ix_asset_variant_plans_asset_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_asset_id ON public.asset_variant_plans USING btree (asset_id);


--
-- Name: ix_asset_variant_plans_episode_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_episode_id ON public.asset_variant_plans USING btree (episode_id);


--
-- Name: ix_asset_variant_plans_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_project_id ON public.asset_variant_plans USING btree (project_id);


--
-- Name: ix_asset_variant_plans_script_version_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_script_version_id ON public.asset_variant_plans USING btree (script_version_id);


--
-- Name: ix_asset_variant_plans_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_source ON public.asset_variant_plans USING btree (source);


--
-- Name: ix_asset_variant_plans_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_status ON public.asset_variant_plans USING btree (status);


--
-- Name: ix_asset_variant_plans_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_task_id ON public.asset_variant_plans USING btree (task_id);


--
-- Name: ix_asset_variant_plans_variant_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_variant_code ON public.asset_variant_plans USING btree (variant_code);


--
-- Name: ix_asset_variant_plans_variant_kind; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_plans_variant_kind ON public.asset_variant_plans USING btree (variant_kind);


--
-- Name: ix_asset_variant_storyboard_links_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_storyboard_links_source ON public.asset_variant_storyboard_links USING btree (source);


--
-- Name: ix_asset_variant_storyboard_links_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_storyboard_links_status ON public.asset_variant_storyboard_links USING btree (status);


--
-- Name: ix_asset_variant_storyboard_links_storyboard_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_storyboard_links_storyboard_id ON public.asset_variant_storyboard_links USING btree (storyboard_id);


--
-- Name: ix_asset_variant_storyboard_links_variant_plan_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_variant_storyboard_links_variant_plan_id ON public.asset_variant_storyboard_links USING btree (variant_plan_id);


--
-- Name: ix_asset_versions_asset_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_versions_asset_id ON public.asset_versions USING btree (asset_id);


--
-- Name: ix_asset_versions_asset_version_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_asset_versions_asset_version_code ON public.asset_versions USING btree (asset_version_code);


--
-- Name: ix_asset_versions_is_invalidated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_versions_is_invalidated ON public.asset_versions USING btree (is_invalidated);


--
-- Name: ix_asset_versions_source_master_submission_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_asset_versions_source_master_submission_id ON public.asset_versions USING btree (source_master_submission_id);


--
-- Name: ix_asset_versions_source_submission_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_asset_versions_source_submission_id ON public.asset_versions USING btree (source_submission_id);


--
-- Name: ix_assets_asset_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_assets_asset_code ON public.assets USING btree (asset_code);


--
-- Name: ix_assets_asset_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_assets_asset_type ON public.assets USING btree (asset_type);


--
-- Name: ix_assets_current_revision_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_assets_current_revision_id ON public.assets USING btree (current_revision_id);


--
-- Name: ix_assets_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_assets_name ON public.assets USING btree (name);


--
-- Name: ix_assets_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_assets_project_id ON public.assets USING btree (project_id);


--
-- Name: ix_entity_versions_entity_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_entity_versions_entity_code ON public.entity_versions USING btree (entity_code);


--
-- Name: ix_entity_versions_entity_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_entity_versions_entity_id ON public.entity_versions USING btree (entity_id);


--
-- Name: ix_entity_versions_entity_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_entity_versions_entity_type ON public.entity_versions USING btree (entity_type);


--
-- Name: ix_entity_versions_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_entity_versions_project_id ON public.entity_versions USING btree (project_id);


--
-- Name: ix_entity_versions_version_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_entity_versions_version_code ON public.entity_versions USING btree (version_code);


--
-- Name: ix_episode_asset_bindings_action; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_action ON public.episode_asset_bindings USING btree (action);


--
-- Name: ix_episode_asset_bindings_age_stage_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_age_stage_code ON public.episode_asset_bindings USING btree (age_stage_code);


--
-- Name: ix_episode_asset_bindings_application_revision_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_application_revision_id ON public.episode_asset_bindings USING btree (application_revision_id);


--
-- Name: ix_episode_asset_bindings_asset_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_asset_id ON public.episode_asset_bindings USING btree (asset_id);


--
-- Name: ix_episode_asset_bindings_asset_revision_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_asset_revision_id ON public.episode_asset_bindings USING btree (asset_revision_id);


--
-- Name: ix_episode_asset_bindings_confirmed_reading_revision_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_confirmed_reading_revision_id ON public.episode_asset_bindings USING btree (confirmed_reading_revision_id);


--
-- Name: ix_episode_asset_bindings_costume_variant_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_costume_variant_code ON public.episode_asset_bindings USING btree (costume_variant_code);


--
-- Name: ix_episode_asset_bindings_episode_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_episode_id ON public.episode_asset_bindings USING btree (episode_id);


--
-- Name: ix_episode_asset_bindings_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_project_id ON public.episode_asset_bindings USING btree (project_id);


--
-- Name: ix_episode_asset_bindings_script_version_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_script_version_id ON public.episode_asset_bindings USING btree (script_version_id);


--
-- Name: ix_episode_asset_bindings_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_episode_asset_bindings_status ON public.episode_asset_bindings USING btree (status);


--
-- Name: ix_files_episode_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_files_episode_id ON public.files USING btree (episode_id);


--
-- Name: ix_files_file_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_files_file_code ON public.files USING btree (file_code);


--
-- Name: ix_files_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_files_project_id ON public.files USING btree (project_id);


--
-- Name: ix_files_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_files_task_id ON public.files USING btree (task_id);


--
-- Name: ix_final_outputs_final_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_final_outputs_final_code ON public.final_outputs USING btree (final_code);


--
-- Name: ix_final_outputs_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_final_outputs_project_id ON public.final_outputs USING btree (project_id);


--
-- Name: ix_image_outputs_image_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_image_outputs_image_code ON public.image_outputs USING btree (image_code);


--
-- Name: ix_image_outputs_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_image_outputs_project_id ON public.image_outputs USING btree (project_id);


--
-- Name: ix_notifications_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_notifications_user_id ON public.notifications USING btree (user_id);


--
-- Name: ix_operation_logs_operator_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_operation_logs_operator_id ON public.operation_logs USING btree (operator_id);


--
-- Name: ix_operation_logs_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_operation_logs_project_id ON public.operation_logs USING btree (project_id);


--
-- Name: ix_permissions_permission_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_permissions_permission_type ON public.permissions USING btree (permission_type);


--
-- Name: ix_permissions_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_permissions_project_id ON public.permissions USING btree (project_id);


--
-- Name: ix_permissions_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_permissions_user_id ON public.permissions USING btree (user_id);


--
-- Name: ix_project_asset_list_items_asset_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_asset_list_items_asset_code ON public.project_asset_list_items USING btree (asset_code);


--
-- Name: ix_project_asset_list_items_asset_list_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_asset_list_items_asset_list_id ON public.project_asset_list_items USING btree (asset_list_id);


--
-- Name: ix_project_asset_list_items_asset_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_asset_list_items_asset_type ON public.project_asset_list_items USING btree (asset_type);


--
-- Name: ix_project_asset_list_items_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_asset_list_items_project_id ON public.project_asset_list_items USING btree (project_id);


--
-- Name: ix_project_asset_lists_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_asset_lists_project_id ON public.project_asset_lists USING btree (project_id);


--
-- Name: ix_project_episodes_episode_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_episodes_episode_code ON public.project_episodes USING btree (episode_code);


--
-- Name: ix_project_episodes_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_project_episodes_project_id ON public.project_episodes USING btree (project_id);


--
-- Name: ix_projects_deleted_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_projects_deleted_at ON public.projects USING btree (deleted_at);


--
-- Name: ix_projects_project_no; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_projects_project_no ON public.projects USING btree (project_no);


--
-- Name: ix_projects_project_prefix; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_projects_project_prefix ON public.projects USING btree (project_prefix);


--
-- Name: ix_projects_title; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_projects_title ON public.projects USING btree (title);


--
-- Name: ix_script_breakdowns_data_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_breakdowns_data_state ON public.script_breakdowns USING btree (data_state);


--
-- Name: ix_script_breakdowns_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_breakdowns_project_id ON public.script_breakdowns USING btree (project_id);


--
-- Name: ix_script_segments_context_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_segments_context_code ON public.script_segments USING btree (context_code);


--
-- Name: ix_script_segments_episode_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_segments_episode_code ON public.script_segments USING btree (episode_code);


--
-- Name: ix_script_segments_episode_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_segments_episode_id ON public.script_segments USING btree (episode_id);


--
-- Name: ix_script_segments_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_segments_project_id ON public.script_segments USING btree (project_id);


--
-- Name: ix_script_segments_render_mode; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_segments_render_mode ON public.script_segments USING btree (render_mode);


--
-- Name: ix_script_segments_script_segment_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_segments_script_segment_code ON public.script_segments USING btree (script_segment_code);


--
-- Name: ix_script_versions_content_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_versions_content_hash ON public.script_versions USING btree (content_hash);


--
-- Name: ix_script_versions_script_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_script_versions_script_id ON public.script_versions USING btree (script_id);


--
-- Name: ix_scripts_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_scripts_project_id ON public.scripts USING btree (project_id);


--
-- Name: ix_scripts_script_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_scripts_script_code ON public.scripts USING btree (script_code);


--
-- Name: ix_storyboard_versions_storyboard_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboard_versions_storyboard_id ON public.storyboard_versions USING btree (storyboard_id);


--
-- Name: ix_storyboards_context_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboards_context_code ON public.storyboards USING btree (context_code);


--
-- Name: ix_storyboards_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboards_project_id ON public.storyboards USING btree (project_id);


--
-- Name: ix_storyboards_render_mode; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboards_render_mode ON public.storyboards USING btree (render_mode);


--
-- Name: ix_storyboards_scene_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboards_scene_code ON public.storyboards USING btree (scene_code);


--
-- Name: ix_storyboards_script_segment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboards_script_segment_id ON public.storyboards USING btree (script_segment_id);


--
-- Name: ix_storyboards_storyboard_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_storyboards_storyboard_code ON public.storyboards USING btree (storyboard_code);


--
-- Name: ix_submissions_batch_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_batch_id ON public.submissions USING btree (batch_id);


--
-- Name: ix_submissions_file_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_file_id ON public.submissions USING btree (file_id);


--
-- Name: ix_submissions_is_archived; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_is_archived ON public.submissions USING btree (is_archived);


--
-- Name: ix_submissions_is_invalidated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_is_invalidated ON public.submissions USING btree (is_invalidated);


--
-- Name: ix_submissions_source_master_submission_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_source_master_submission_id ON public.submissions USING btree (source_master_submission_id);


--
-- Name: ix_submissions_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_status ON public.submissions USING btree (status);


--
-- Name: ix_submissions_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_submissions_task_id ON public.submissions USING btree (task_id);


--
-- Name: ix_task_candidate_imports_created_by; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_candidate_imports_created_by ON public.task_candidate_imports USING btree (created_by);


--
-- Name: ix_task_candidate_imports_source_asset; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_candidate_imports_source_asset ON public.task_candidate_imports USING btree (source_asset_id);


--
-- Name: ix_task_candidate_imports_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_candidate_imports_status ON public.task_candidate_imports USING btree (status);


--
-- Name: ix_task_candidate_imports_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_candidate_imports_task_id ON public.task_candidate_imports USING btree (task_id);


--
-- Name: ix_task_dependencies_dependency_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_dependencies_dependency_type ON public.task_dependencies USING btree (dependency_type);


--
-- Name: ix_task_dependencies_depends_on_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_dependencies_depends_on_task_id ON public.task_dependencies USING btree (depends_on_task_id);


--
-- Name: ix_task_dependencies_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_dependencies_task_id ON public.task_dependencies USING btree (task_id);


--
-- Name: ix_task_prompts_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_prompts_task_id ON public.task_prompts USING btree (task_id);


--
-- Name: ix_task_submission_batches_review_request_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_task_submission_batches_review_request_id ON public.task_submission_batches USING btree (review_request_id);


--
-- Name: ix_task_submission_batches_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_submission_batches_status ON public.task_submission_batches USING btree (status);


--
-- Name: ix_task_submission_batches_step; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_submission_batches_step ON public.task_submission_batches USING btree (step);


--
-- Name: ix_task_submission_batches_submit_request_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_task_submission_batches_submit_request_id ON public.task_submission_batches USING btree (submit_request_id);


--
-- Name: ix_task_submission_batches_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_task_submission_batches_task_id ON public.task_submission_batches USING btree (task_id);


--
-- Name: ix_tasks_age_stage_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_age_stage_code ON public.tasks USING btree (age_stage_code);


--
-- Name: ix_tasks_costume_variant_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_costume_variant_code ON public.tasks USING btree (costume_variant_code);


--
-- Name: ix_tasks_depends_on_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_depends_on_task_id ON public.tasks USING btree (depends_on_task_id);


--
-- Name: ix_tasks_is_retired; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_is_retired ON public.tasks USING btree (is_retired);


--
-- Name: ix_tasks_media_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_media_type ON public.tasks USING btree (media_type);


--
-- Name: ix_tasks_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_project_id ON public.tasks USING btree (project_id);


--
-- Name: ix_tasks_project_status_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_project_status_due ON public.tasks USING btree (project_id, status, due_at);


--
-- Name: ix_tasks_prompt_revision_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_prompt_revision_id ON public.tasks USING btree (prompt_revision_id);


--
-- Name: ix_tasks_scene_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_scene_code ON public.tasks USING btree (scene_code);


--
-- Name: ix_tasks_script_version_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_script_version_id ON public.tasks USING btree (script_version_id);


--
-- Name: ix_tasks_task_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_task_type ON public.tasks USING btree (task_type);


--
-- Name: ix_tasks_task_variant; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_task_variant ON public.tasks USING btree (task_variant);


--
-- Name: ix_tasks_variant_kind; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_variant_kind ON public.tasks USING btree (variant_kind);


--
-- Name: ix_tasks_variant_plan_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_tasks_variant_plan_id ON public.tasks USING btree (variant_plan_id);


--
-- Name: ix_users_phone; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_users_phone ON public.users USING btree (phone);


--
-- Name: ix_users_username; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_users_username ON public.users USING btree (username);


--
-- Name: ix_video_outputs_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_video_outputs_project_id ON public.video_outputs USING btree (project_id);


--
-- Name: ix_video_outputs_video_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_video_outputs_video_code ON public.video_outputs USING btree (video_code);


--
-- Name: ix_workflow_runs_agent_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workflow_runs_agent_run_id ON public.workflow_runs USING btree (agent_run_id);


--
-- Name: ix_workflow_runs_lock_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workflow_runs_lock_key ON public.workflow_runs USING btree (lock_key);


--
-- Name: ix_workflow_runs_project_created_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workflow_runs_project_created_id ON public.workflow_runs USING btree (project_id, created_at, id);


--
-- Name: ix_workflow_runs_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workflow_runs_project_id ON public.workflow_runs USING btree (project_id);


--
-- Name: ix_workflow_runs_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workflow_runs_status ON public.workflow_runs USING btree (status);


--
-- Name: ix_workflow_runs_workflow_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_workflow_runs_workflow_type ON public.workflow_runs USING btree (workflow_type);


--
-- Name: sys_casbin_rule_unique_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX sys_casbin_rule_unique_idx ON public.sys_casbin_rule USING btree (ptype, v0, v1, v2, v3, v4, v5);


--
-- Name: uq_agent_runs_workflow_node; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_agent_runs_workflow_node ON public.agent_runs USING btree (workflow_run_id, node_key) WHERE ((workflow_run_id IS NOT NULL) AND (node_key IS NOT NULL));


--
-- Name: uq_artifact_revisions_idempotency_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_artifact_revisions_idempotency_key ON public.artifact_revisions USING btree (idempotency_key) WHERE (idempotency_key IS NOT NULL);


--
-- Name: uq_tasks_audio_asset; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_tasks_audio_asset ON public.tasks USING btree (project_id, asset_id, task_type) WHERE (task_type = 'audio'::public.task_type);


--
-- Name: uq_tasks_idempotency_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_tasks_idempotency_key ON public.tasks USING btree (idempotency_key) WHERE (idempotency_key IS NOT NULL);


--
-- Name: uq_workflow_runs_idempotency_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_workflow_runs_idempotency_key ON public.workflow_runs USING btree (idempotency_key) WHERE (idempotency_key IS NOT NULL);


--
-- Name: agent_feedback agent_feedback_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_feedback
    ADD CONSTRAINT agent_feedback_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: agent_feedback agent_feedback_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_feedback
    ADD CONSTRAINT agent_feedback_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: agent_feedback agent_feedback_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_feedback
    ADD CONSTRAINT agent_feedback_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.agent_runs(id);


--
-- Name: agent_feedback agent_feedback_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_feedback
    ADD CONSTRAINT agent_feedback_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: agent_issues agent_issues_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_issues
    ADD CONSTRAINT agent_issues_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.agent_runs(id);


--
-- Name: agent_runs agent_runs_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT agent_runs_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: agent_runs agent_runs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT agent_runs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: agent_runs agent_runs_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT agent_runs_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: agent_runs agent_runs_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT agent_runs_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: agent_runs agent_runs_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT agent_runs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: agent_training_samples agent_training_samples_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_training_samples
    ADD CONSTRAINT agent_training_samples_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: agent_training_samples agent_training_samples_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_training_samples
    ADD CONSTRAINT agent_training_samples_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: agent_training_samples agent_training_samples_source_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_training_samples
    ADD CONSTRAINT agent_training_samples_source_run_id_fkey FOREIGN KEY (source_run_id) REFERENCES public.agent_runs(id);


--
-- Name: artifact_revisions artifact_revisions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: artifact_revisions artifact_revisions_parent_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_parent_revision_id_fkey FOREIGN KEY (parent_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: artifact_revisions artifact_revisions_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: artifact_revisions artifact_revisions_source_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.artifact_revisions
    ADD CONSTRAINT artifact_revisions_source_run_id_fkey FOREIGN KEY (source_run_id) REFERENCES public.agent_runs(id);


--
-- Name: asset_completion_records asset_completion_records_applied_asset_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_applied_asset_revision_id_fkey FOREIGN KEY (applied_asset_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: asset_completion_records asset_completion_records_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: asset_completion_records asset_completion_records_base_asset_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_base_asset_revision_id_fkey FOREIGN KEY (base_asset_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: asset_completion_records asset_completion_records_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: asset_completion_records asset_completion_records_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: asset_completion_records asset_completion_records_source_agent_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_completion_records
    ADD CONSTRAINT asset_completion_records_source_agent_revision_id_fkey FOREIGN KEY (source_agent_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: asset_relations asset_relations_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: asset_relations asset_relations_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: asset_relations asset_relations_final_output_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_final_output_id_fkey FOREIGN KEY (final_output_id) REFERENCES public.final_outputs(id);


--
-- Name: asset_relations asset_relations_image_output_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_image_output_id_fkey FOREIGN KEY (image_output_id) REFERENCES public.image_outputs(id);


--
-- Name: asset_relations asset_relations_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: asset_relations asset_relations_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: asset_relations asset_relations_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: asset_relations asset_relations_video_output_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_relations
    ADD CONSTRAINT asset_relations_video_output_id_fkey FOREIGN KEY (video_output_id) REFERENCES public.video_outputs(id);


--
-- Name: asset_variant_plans asset_variant_plans_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id) ON DELETE CASCADE;


--
-- Name: asset_variant_plans asset_variant_plans_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: asset_variant_plans asset_variant_plans_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id) ON DELETE CASCADE;


--
-- Name: asset_variant_plans asset_variant_plans_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id) ON DELETE CASCADE;


--
-- Name: asset_variant_plans asset_variant_plans_script_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_script_version_id_fkey FOREIGN KEY (script_version_id) REFERENCES public.script_versions(id);


--
-- Name: asset_variant_plans asset_variant_plans_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_plans
    ADD CONSTRAINT asset_variant_plans_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE SET NULL;


--
-- Name: asset_variant_storyboard_links asset_variant_storyboard_links_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_storyboard_links
    ADD CONSTRAINT asset_variant_storyboard_links_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: asset_variant_storyboard_links asset_variant_storyboard_links_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_storyboard_links
    ADD CONSTRAINT asset_variant_storyboard_links_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id) ON DELETE CASCADE;


--
-- Name: asset_variant_storyboard_links asset_variant_storyboard_links_variant_plan_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_variant_storyboard_links
    ADD CONSTRAINT asset_variant_storyboard_links_variant_plan_id_fkey FOREIGN KEY (variant_plan_id) REFERENCES public.asset_variant_plans(id) ON DELETE CASCADE;


--
-- Name: asset_versions asset_versions_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: asset_versions asset_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: asset_versions asset_versions_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.files(id);


--
-- Name: asset_versions asset_versions_invalidated_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_invalidated_by_id_fkey FOREIGN KEY (invalidated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: asset_versions asset_versions_invalidated_source_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_invalidated_source_id_fkey FOREIGN KEY (invalidated_source_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: asset_versions asset_versions_preview_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_preview_file_id_fkey FOREIGN KEY (preview_file_id) REFERENCES public.files(id);


--
-- Name: asset_versions asset_versions_source_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_source_agent_run_id_fkey FOREIGN KEY (source_agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: asset_versions asset_versions_source_master_submission_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_source_master_submission_id_fkey FOREIGN KEY (source_master_submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: asset_versions asset_versions_source_submission_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_source_submission_id_fkey FOREIGN KEY (source_submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: asset_versions asset_versions_source_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT asset_versions_source_task_id_fkey FOREIGN KEY (source_task_id) REFERENCES public.tasks(id);


--
-- Name: assets assets_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assets
    ADD CONSTRAINT assets_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id);


--
-- Name: assets assets_locked_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assets
    ADD CONSTRAINT assets_locked_by_fkey FOREIGN KEY (locked_by) REFERENCES public.users(id);


--
-- Name: assets assets_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assets
    ADD CONSTRAINT assets_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: entity_versions entity_versions_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_versions
    ADD CONSTRAINT entity_versions_agent_run_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: entity_versions entity_versions_base_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_versions
    ADD CONSTRAINT entity_versions_base_version_id_fkey FOREIGN KEY (base_version_id) REFERENCES public.entity_versions(id);


--
-- Name: entity_versions entity_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_versions
    ADD CONSTRAINT entity_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: entity_versions entity_versions_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_versions
    ADD CONSTRAINT entity_versions_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_application_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_application_revision_id_fkey FOREIGN KEY (application_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_asset_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_asset_revision_id_fkey FOREIGN KEY (asset_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_confirmed_reading_revision_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_confirmed_reading_revision_id_fkey FOREIGN KEY (confirmed_reading_revision_id) REFERENCES public.artifact_revisions(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: episode_asset_bindings episode_asset_bindings_script_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.episode_asset_bindings
    ADD CONSTRAINT episode_asset_bindings_script_version_id_fkey FOREIGN KEY (script_version_id) REFERENCES public.script_versions(id);


--
-- Name: files files_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.files
    ADD CONSTRAINT files_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: files files_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.files
    ADD CONSTRAINT files_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: files files_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.files
    ADD CONSTRAINT files_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: files files_uploaded_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.files
    ADD CONSTRAINT files_uploaded_by_fkey FOREIGN KEY (uploaded_by) REFERENCES public.users(id);


--
-- Name: final_outputs final_outputs_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: final_outputs final_outputs_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: final_outputs final_outputs_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: final_outputs final_outputs_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.files(id);


--
-- Name: final_outputs final_outputs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: final_outputs final_outputs_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.final_outputs
    ADD CONSTRAINT final_outputs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: agent_runs fk_agent_runs_parent_run_id; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT fk_agent_runs_parent_run_id FOREIGN KEY (parent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: agent_runs fk_agent_runs_workflow_run_id; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_runs
    ADD CONSTRAINT fk_agent_runs_workflow_run_id FOREIGN KEY (workflow_run_id) REFERENCES public.workflow_runs(id);


--
-- Name: asset_versions fk_asset_versions_invalidated_by_id_users; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT fk_asset_versions_invalidated_by_id_users FOREIGN KEY (invalidated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: asset_versions fk_asset_versions_invalidated_source_id_submissions; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT fk_asset_versions_invalidated_source_id_submissions FOREIGN KEY (invalidated_source_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: asset_versions fk_asset_versions_source_master_submission_id_submissions; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT fk_asset_versions_source_master_submission_id_submissions FOREIGN KEY (source_master_submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: asset_versions fk_asset_versions_source_submission_id_submissions; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.asset_versions
    ADD CONSTRAINT fk_asset_versions_source_submission_id_submissions FOREIGN KEY (source_submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: submissions fk_submissions_invalidated_by_id_users; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT fk_submissions_invalidated_by_id_users FOREIGN KEY (invalidated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: submissions fk_submissions_invalidated_source_id_submissions; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT fk_submissions_invalidated_source_id_submissions FOREIGN KEY (invalidated_source_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: submissions fk_submissions_source_master_submission_id_submissions; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT fk_submissions_source_master_submission_id_submissions FOREIGN KEY (source_master_submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: tasks fk_tasks_retired_by_users; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT fk_tasks_retired_by_users FOREIGN KEY (retired_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: image_outputs image_outputs_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: image_outputs image_outputs_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: image_outputs image_outputs_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: image_outputs image_outputs_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.files(id);


--
-- Name: image_outputs image_outputs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: image_outputs image_outputs_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: image_outputs image_outputs_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: image_outputs image_outputs_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.image_outputs
    ADD CONSTRAINT image_outputs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: notifications notifications_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notifications
    ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: operation_logs operation_logs_operator_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operation_logs
    ADD CONSTRAINT operation_logs_operator_id_fkey FOREIGN KEY (operator_id) REFERENCES public.users(id);


--
-- Name: operation_logs operation_logs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.operation_logs
    ADD CONSTRAINT operation_logs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: permissions permissions_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: permissions permissions_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.permissions
    ADD CONSTRAINT permissions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);


--
-- Name: project_asset_list_items project_asset_list_items_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: project_asset_list_items project_asset_list_items_asset_list_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_asset_list_id_fkey FOREIGN KEY (asset_list_id) REFERENCES public.project_asset_lists(id);


--
-- Name: project_asset_list_items project_asset_list_items_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: project_asset_list_items project_asset_list_items_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: project_asset_list_items project_asset_list_items_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: project_asset_list_items project_asset_list_items_script_segment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_script_segment_id_fkey FOREIGN KEY (script_segment_id) REFERENCES public.script_segments(id);


--
-- Name: project_asset_list_items project_asset_list_items_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_list_items
    ADD CONSTRAINT project_asset_list_items_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: project_asset_lists project_asset_lists_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_lists
    ADD CONSTRAINT project_asset_lists_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: project_asset_lists project_asset_lists_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_lists
    ADD CONSTRAINT project_asset_lists_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: project_asset_lists project_asset_lists_source_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_lists
    ADD CONSTRAINT project_asset_lists_source_agent_run_id_fkey FOREIGN KEY (source_agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: project_asset_lists project_asset_lists_source_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_asset_lists
    ADD CONSTRAINT project_asset_lists_source_script_id_fkey FOREIGN KEY (source_script_id) REFERENCES public.scripts(id);


--
-- Name: project_assets project_assets_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_assets
    ADD CONSTRAINT project_assets_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: project_assets project_assets_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_assets
    ADD CONSTRAINT project_assets_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: project_assets project_assets_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_assets
    ADD CONSTRAINT project_assets_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: project_episodes project_episodes_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.project_episodes
    ADD CONSTRAINT project_episodes_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: projects projects_created_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_created_by_id_fkey FOREIGN KEY (created_by_id) REFERENCES public.users(id);


--
-- Name: projects projects_deleted_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_deleted_by_id_fkey FOREIGN KEY (deleted_by_id) REFERENCES public.users(id);


--
-- Name: projects projects_manager_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_manager_id_fkey FOREIGN KEY (manager_id) REFERENCES public.users(id);


--
-- Name: script_breakdowns script_breakdowns_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_breakdowns
    ADD CONSTRAINT script_breakdowns_agent_run_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: script_breakdowns script_breakdowns_edited_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_breakdowns
    ADD CONSTRAINT script_breakdowns_edited_by_id_fkey FOREIGN KEY (edited_by_id) REFERENCES public.users(id);


--
-- Name: script_breakdowns script_breakdowns_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_breakdowns
    ADD CONSTRAINT script_breakdowns_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: script_segments script_segments_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_segments
    ADD CONSTRAINT script_segments_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: script_segments script_segments_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_segments
    ADD CONSTRAINT script_segments_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: script_segments script_segments_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_segments
    ADD CONSTRAINT script_segments_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: script_versions script_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_versions
    ADD CONSTRAINT script_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: script_versions script_versions_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_versions
    ADD CONSTRAINT script_versions_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: script_versions script_versions_source_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_versions
    ADD CONSTRAINT script_versions_source_agent_run_id_fkey FOREIGN KEY (source_agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: script_versions script_versions_source_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.script_versions
    ADD CONSTRAINT script_versions_source_file_id_fkey FOREIGN KEY (source_file_id) REFERENCES public.files(id);


--
-- Name: scripts scripts_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scripts
    ADD CONSTRAINT scripts_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: scripts scripts_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scripts
    ADD CONSTRAINT scripts_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: scripts scripts_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scripts
    ADD CONSTRAINT scripts_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: storyboard_versions storyboard_versions_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboard_versions
    ADD CONSTRAINT storyboard_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: storyboard_versions storyboard_versions_source_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboard_versions
    ADD CONSTRAINT storyboard_versions_source_agent_run_id_fkey FOREIGN KEY (source_agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: storyboard_versions storyboard_versions_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboard_versions
    ADD CONSTRAINT storyboard_versions_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: storyboards storyboards_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboards
    ADD CONSTRAINT storyboards_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: storyboards storyboards_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboards
    ADD CONSTRAINT storyboards_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: storyboards storyboards_script_segment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.storyboards
    ADD CONSTRAINT storyboards_script_segment_id_fkey FOREIGN KEY (script_segment_id) REFERENCES public.script_segments(id);


--
-- Name: submissions submissions_archived_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_archived_by_id_fkey FOREIGN KEY (archived_by_id) REFERENCES public.users(id);


--
-- Name: submissions submissions_batch_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_batch_id_fkey FOREIGN KEY (batch_id) REFERENCES public.task_submission_batches(id) ON DELETE SET NULL;


--
-- Name: submissions submissions_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.files(id) ON DELETE SET NULL;


--
-- Name: submissions submissions_invalidated_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_invalidated_by_id_fkey FOREIGN KEY (invalidated_by_id) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: submissions submissions_invalidated_source_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_invalidated_source_id_fkey FOREIGN KEY (invalidated_source_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: submissions submissions_source_master_submission_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_source_master_submission_id_fkey FOREIGN KEY (source_master_submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: submissions submissions_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: submissions submissions_submitted_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_submitted_by_id_fkey FOREIGN KEY (submitted_by_id) REFERENCES public.users(id);


--
-- Name: submissions submissions_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.submissions
    ADD CONSTRAINT submissions_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: task_candidate_imports task_candidate_imports_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_candidate_imports
    ADD CONSTRAINT task_candidate_imports_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: task_candidate_imports task_candidate_imports_submission_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_candidate_imports
    ADD CONSTRAINT task_candidate_imports_submission_id_fkey FOREIGN KEY (submission_id) REFERENCES public.submissions(id) ON DELETE SET NULL;


--
-- Name: task_candidate_imports task_candidate_imports_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_candidate_imports
    ADD CONSTRAINT task_candidate_imports_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;


--
-- Name: task_dependencies task_dependencies_depends_on_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_dependencies
    ADD CONSTRAINT task_dependencies_depends_on_task_id_fkey FOREIGN KEY (depends_on_task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;


--
-- Name: task_dependencies task_dependencies_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_dependencies
    ADD CONSTRAINT task_dependencies_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;


--
-- Name: task_prompts task_prompts_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_prompts
    ADD CONSTRAINT task_prompts_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: task_prompts task_prompts_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_prompts
    ADD CONSTRAINT task_prompts_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: task_submission_batches task_submission_batches_reviewed_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_submission_batches
    ADD CONSTRAINT task_submission_batches_reviewed_by_id_fkey FOREIGN KEY (reviewed_by_id) REFERENCES public.users(id);


--
-- Name: task_submission_batches task_submission_batches_submitted_by_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_submission_batches
    ADD CONSTRAINT task_submission_batches_submitted_by_id_fkey FOREIGN KEY (submitted_by_id) REFERENCES public.users(id);


--
-- Name: task_submission_batches task_submission_batches_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_submission_batches
    ADD CONSTRAINT task_submission_batches_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id) ON DELETE CASCADE;


--
-- Name: tasks tasks_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: tasks tasks_assigned_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_assigned_by_fkey FOREIGN KEY (assigned_by) REFERENCES public.users(id);


--
-- Name: tasks tasks_assignee_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_assignee_id_fkey FOREIGN KEY (assignee_id) REFERENCES public.users(id);


--
-- Name: tasks tasks_depends_on_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_depends_on_task_id_fkey FOREIGN KEY (depends_on_task_id) REFERENCES public.tasks(id);


--
-- Name: tasks tasks_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: tasks tasks_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: tasks tasks_retired_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_retired_by_fkey FOREIGN KEY (retired_by) REFERENCES public.users(id) ON DELETE SET NULL;


--
-- Name: tasks tasks_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: tasks tasks_script_segment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_script_segment_id_fkey FOREIGN KEY (script_segment_id) REFERENCES public.script_segments(id);


--
-- Name: tasks tasks_script_version_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_script_version_id_fkey FOREIGN KEY (script_version_id) REFERENCES public.script_versions(id);


--
-- Name: tasks tasks_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: tasks tasks_variant_plan_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_variant_plan_id_fkey FOREIGN KEY (variant_plan_id) REFERENCES public.asset_variant_plans(id) ON DELETE SET NULL;


--
-- Name: video_outputs video_outputs_asset_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_asset_id_fkey FOREIGN KEY (asset_id) REFERENCES public.assets(id);


--
-- Name: video_outputs video_outputs_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: video_outputs video_outputs_episode_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES public.project_episodes(id);


--
-- Name: video_outputs video_outputs_file_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_file_id_fkey FOREIGN KEY (file_id) REFERENCES public.files(id);


--
-- Name: video_outputs video_outputs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
-- Name: video_outputs video_outputs_script_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_script_id_fkey FOREIGN KEY (script_id) REFERENCES public.scripts(id);


--
-- Name: video_outputs video_outputs_storyboard_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_storyboard_id_fkey FOREIGN KEY (storyboard_id) REFERENCES public.storyboards(id);


--
-- Name: video_outputs video_outputs_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.video_outputs
    ADD CONSTRAINT video_outputs_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.tasks(id);


--
-- Name: workflow_runs workflow_runs_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_runs
    ADD CONSTRAINT workflow_runs_agent_run_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_runs(id);


--
-- Name: workflow_runs workflow_runs_created_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_runs
    ADD CONSTRAINT workflow_runs_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id);


--
-- Name: workflow_runs workflow_runs_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflow_runs
    ADD CONSTRAINT workflow_runs_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(id);


--
--



-- +goose Down
DROP TABLE IF EXISTS public.agent_feedback CASCADE;
DROP TABLE IF EXISTS public.agent_issues CASCADE;
DROP TABLE IF EXISTS public.agent_runs CASCADE;
DROP TABLE IF EXISTS public.agent_training_samples CASCADE;
DROP TABLE IF EXISTS public.artifact_revisions CASCADE;
DROP TABLE IF EXISTS public.asset_completion_records CASCADE;
DROP TABLE IF EXISTS public.asset_relations CASCADE;
DROP TABLE IF EXISTS public.asset_storage_locations CASCADE;
DROP TABLE IF EXISTS public.asset_variant_plans CASCADE;
DROP TABLE IF EXISTS public.asset_variant_storyboard_links CASCADE;
DROP TABLE IF EXISTS public.asset_versions CASCADE;
DROP TABLE IF EXISTS public.assets CASCADE;
DROP TABLE IF EXISTS public.canvas_assets CASCADE;
DROP TABLE IF EXISTS public.canvas_node_outputs CASCADE;
DROP TABLE IF EXISTS public.canvas_projects CASCADE;
DROP TABLE IF EXISTS public.entity_versions CASCADE;
DROP TABLE IF EXISTS public.episode_asset_bindings CASCADE;
DROP TABLE IF EXISTS public.files CASCADE;
DROP TABLE IF EXISTS public.final_outputs CASCADE;
DROP TABLE IF EXISTS public.flows CASCADE;
DROP TABLE IF EXISTS public.image_outputs CASCADE;
DROP TABLE IF EXISTS public.notifications CASCADE;
DROP TABLE IF EXISTS public.operation_logs CASCADE;
DROP TABLE IF EXISTS public.permissions CASCADE;
DROP TABLE IF EXISTS public.project_asset_list_items CASCADE;
DROP TABLE IF EXISTS public.project_asset_lists CASCADE;
DROP TABLE IF EXISTS public.project_assets CASCADE;
DROP TABLE IF EXISTS public.project_episodes CASCADE;
DROP TABLE IF EXISTS public.projects CASCADE;
DROP TABLE IF EXISTS public.cineforge_task_canvas_bindings CASCADE;
DROP TABLE IF EXISTS public.cineforge_task_canvas_members CASCADE;
DROP TABLE IF EXISTS public.script_breakdowns CASCADE;
DROP TABLE IF EXISTS public.script_segments CASCADE;
DROP TABLE IF EXISTS public.script_versions CASCADE;
DROP TABLE IF EXISTS public.scripts CASCADE;
DROP TABLE IF EXISTS public.storyboard_versions CASCADE;
DROP TABLE IF EXISTS public.storyboards CASCADE;
DROP TABLE IF EXISTS public.submissions CASCADE;
DROP TABLE IF EXISTS public.sys_casbin_rule CASCADE;
DROP TABLE IF EXISTS public.sys_role CASCADE;
DROP TABLE IF EXISTS public.task_candidate_imports CASCADE;
DROP TABLE IF EXISTS public.task_dependencies CASCADE;
DROP TABLE IF EXISTS public.task_prompts CASCADE;
DROP TABLE IF EXISTS public.task_submission_batches CASCADE;
DROP TABLE IF EXISTS public.tasks CASCADE;
DROP TABLE IF EXISTS public.users CASCADE;
DROP TABLE IF EXISTS public.video_outputs CASCADE;
DROP TABLE IF EXISTS public.workflow_runs CASCADE;
DROP TYPE IF EXISTS public.user_role CASCADE;
DROP TYPE IF EXISTS public.agent_kind CASCADE;
DROP TYPE IF EXISTS public.asset_type CASCADE;
DROP TYPE IF EXISTS public.asset_list_item_type CASCADE;
DROP TYPE IF EXISTS public.project_stage CASCADE;
DROP TYPE IF EXISTS public.project_status CASCADE;
DROP TYPE IF EXISTS public.task_type CASCADE;
DROP TYPE IF EXISTS public.task_status CASCADE;
DROP TYPE IF EXISTS public.storyboard_status CASCADE;
DROP TYPE IF EXISTS public.agent_run_status CASCADE;
