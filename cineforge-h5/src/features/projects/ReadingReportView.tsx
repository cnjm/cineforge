import React from "react";
import { readingLabel, readingLabels } from "../../readingLabels";
import type { AgentRun } from "../../types";

type ReadingReportViewProps = {
  report?: Record<string, unknown>;
  confirmed?: boolean;
  latestRun?: AgentRun;
  roleNames?: Record<string, string>;
};

type BriefRow = [label: string, value: React.ReactNode];

export function ReadingReportView({ report, confirmed = false, latestRun, roleNames = {} }: ReadingReportViewProps) {
  if (!report || !Object.keys(report).length) {
    return <div className="empty-hint reading-empty-hint">暂无围读报告。点击上方「开始剧本围读」后，结果将在此展示。</div>;
  }

  // roleNames 保留用于后续角色名解析（当前视图未直接渲染角色表，但接口保持兼容）
  void roleNames;

  const story = record(report.story_overview);
  const platform = record(report.platform_strategy);
  const worldview = record(report.worldview);
  const cultural = record(report.cultural_origin);
  const recommendedStyle = record(report.recommended_visual_style);
  const visualStyle = record(report.visual_style);
  const segmentation = record(report.segmentation_guidance);
  const emotionBeats = records(report.emotion_curve);
  const risks = records(report.production_risks);
  const reviews = records(report.manual_review_items);
  const genreTags = strings(story.genre_tags);
  const palette = strings(visualStyle.color_palette);
  const cultureAgentLabel = text(
    cultural.source_agent
    || cultural.agent_name
    || cultural.verification_status
    || cultural.context_source,
  );
  const duration = latestRun?.duration_ms ? `${Math.round(latestRun.duration_ms / 1000)} 秒` : "";
  const source = record(report.source);

  const storyHighlights: BriefRow[] = [
    ["开场钩子", text(story.main_hook)],
    ["观众期待", text(story.audience_promise)],
    ["类型", genreTags.join("、")],
  ].filter((row): row is BriefRow => Boolean(row[1]));

  const platformRows: BriefRow[] = [
    ["节奏策略", text(platform.rhythm_strategy)],
    ["构图策略", text(platform.composition_strategy)],
    ["字幕策略", text(platform.subtitle_strategy)],
  ].filter((row): row is BriefRow => Boolean(row[1]));

  const eraRows: BriefRow[] = [
    ["故事时代", text(worldview.time_period)],
    ["文化时代", readingLabel("culture", cultural.era)],
    ["地域", readingLabel("culture", cultural.region)],
    ["文化背景", readingLabel("culture", cultural.classification)],
  ].filter((row): row is BriefRow => Boolean(row[1]));

  const worldviewRows: BriefRow[] = [
    ["力量体系", text(worldview.power_system)],
    ["社会秩序", text(worldview.social_order)],
    ["世界规则", strings(worldview.rules).join("；")],
    ["禁忌误读", strings(worldview.forbidden_misreads).join("；")],
  ].filter((row): row is BriefRow => Boolean(row[1]));

  const paletteNote = text(visualStyle.palette_note || visualStyle.color_direction);
  const paletteSwatches = palette.filter((color) => color.length <= 12);
  const paletteFallbackNote = palette.filter((color) => color.length > 12).join("；");
  const colorDirection = paletteNote || paletteFallbackNote;

  const visualRows: BriefRow[] = [
    ["主风格", readingLabel("visual_style", recommendedStyle.primary_style_label || recommendedStyle.primary_style_id)],
    ["风格修饰", readingLabels("visual_style", strings(recommendedStyle.modifier_ids)).join("、")],
    ["风格理由", text(recommendedStyle.reason)],
    ["光线规则", strings(visualStyle.lighting_rules).join("；")],
    ["镜头语言", strings(visualStyle.camera_language).join("；")],
    ["禁止风格", readingLabels("visual_style", strings(visualStyle.forbidden_styles)).join("；")],
  ].filter((row): row is BriefRow => Boolean(row[1]));

  const segmentationRows: BriefRow[] = [
    ["拆段策略", highlightSegmentBeatTags(text(segmentation.segment_strategy))],
    ["开场要求", text(segmentation.opening_hook_requirement)],
    ["拆段密度", text(segmentation.segment_density)],
    ["必须连续保留", strings(segmentation.must_keep_together).join("；")],
    ["必须提前切分", strings(segmentation.must_split_before).join("；")],
  ].filter((row): row is BriefRow => Boolean(row[1]));

  return (
    <div className={`reading-brief ${confirmed ? "is-confirmed" : "is-pending"}`}>
      <span className={`reading-brief-corner-badge ${confirmed ? "is-confirmed" : "is-pending"}`}>
        {confirmed ? "已确认" : "待确认"}
      </span>
      <header className="reading-brief-header">
        <div className="reading-brief-title">
          <span>围读结论</span>
          <div>{text(story.logline) || "围读报告"}</div>
          {text(story.core_conflict) ? <p>{text(story.core_conflict)}</p> : null}
        </div>
        {duration ? (
          <div className="reading-brief-status">
            <span className="badge sky">耗时 {duration}</span>
          </div>
        ) : null}
      </header>

      <div className="reading-brief-meta" aria-label="制作策略摘要">
        <BriefMeta label="平台" value={text(platform.primary_platform)} />
        <BriefMeta label="画幅" value={text(platform.aspect_ratio)} />
        <BriefMeta label="目标时长" value={seconds(platform.target_duration_seconds)} />
        <BriefMeta label="镜头密度" value={readingLabel("shot_density", platform.shot_density)} />
        <BriefMeta label="文化" value={readingLabel("culture", cultural.classification)} />
        <BriefMeta label="风格" value={readingLabel("visual_style", recommendedStyle.primary_style_label || recommendedStyle.primary_style_id)} />
      </div>

      <BriefSection title="故事主线" className="reading-story-band">
        {text(story.synopsis) ? (
          <div className="reading-synopsis">
            <strong>剧情梗概</strong>
            <p>{text(story.synopsis)}</p>
          </div>
        ) : null}
        {storyHighlights.length ? <BriefDefinitionList rows={storyHighlights} /> : null}
      </BriefSection>

      {eraRows.length ? (
        <BriefSection title="时代背景" className="reading-era-band">
          <div className="reading-era-summary">
            {eraRows.map(([label, value]) => <BriefMeta key={label} label={label} value={String(value)} />)}
          </div>
        </BriefSection>
      ) : null}

      {platformRows.length ? (
        <BriefSection title="平台制作策略">
          <BriefDefinitionList rows={platformRows} />
        </BriefSection>
      ) : null}

      {worldviewRows.length ? (
        <BriefSection title="世界观">
          <div className="reading-worldview-list"><BriefDefinitionList rows={worldviewRows} /></div>
        </BriefSection>
      ) : null}

      {emotionBeats.length ? (
        <BriefSection title="情绪与节奏">
          <ol className="reading-emotion-timeline">
            {emotionBeats.map((item, index) => (
              <li key={`${text(item.beat) || text(item.story_position)}-${index}`}>
                <span className="reading-emotion-index">{index + 1}</span>
                <div>
                  <header><strong>{text(item.beat) || text(item.story_position) || `节拍 ${index + 1}`}</strong>{text(item.intensity) ? <em>强度 {readingLabel("emotion", item.intensity)}</em> : null}</header>
                  {text(item.emotion) ? <b>{readingLabel("emotion", item.emotion)}</b> : null}
                  {text(item.visual_strategy) ? <p>{text(item.visual_strategy)}</p> : null}
                </div>
              </li>
            ))}
          </ol>
        </BriefSection>
      ) : null}

      {(text(cultural.classification) || visualRows.length || paletteSwatches.length || colorDirection) ? (
        <BriefSection title="文化与视觉方向" className="reading-visual-band">
          <div className="reading-visual-board">
            <div className="reading-culture-bar">
              <div className="reading-culture-chips">
                {[readingLabel("culture", cultural.classification), readingLabel("culture", cultural.region), readingLabel("culture", cultural.era)]
                  .filter(Boolean)
                  .map((chip) => <span key={chip}>{chip}</span>)}
              </div>
              <div className="reading-culture-meta">
                {cultureAgentLabel ? <em>{cultureAgentLabel}</em> : null}
                {text(cultural.confidence) ? <em>置信度 {text(cultural.confidence)}</em> : null}
              </div>
            </div>

            {(paletteSwatches.length || colorDirection || visualRows.length) ? (
              <div className="reading-visual-panels">
                {(paletteSwatches.length || colorDirection) ? (
                  <article className="reading-visual-panel">
                    <header>色调方向</header>
                    {paletteSwatches.length ? (
                      <div className="reading-palette-swatches" aria-label="色彩方案">
                        {paletteSwatches.map((color, index) => (
                          <span key={`${color}-${index}`}>
                            <i style={{ backgroundColor: swatchColor(color, index) }} aria-hidden="true" />
                            <b>{color}</b>
                          </span>
                        ))}
                      </div>
                    ) : null}
                    {colorDirection ? <p>{colorDirection}</p> : null}
                  </article>
                ) : null}

                {visualRows.length ? (
                  <article className="reading-visual-panel">
                    <header>视觉规范</header>
                    <BriefDefinitionList rows={visualRows} />
                  </article>
                ) : null}
              </div>
            ) : null}
          </div>
        </BriefSection>
      ) : null}

      {segmentationRows.length || segmentation.recommended_episode_count || segmentation.target_duration_seconds ? (
        <BriefSection title="拆段建议">
          <div className="reading-segmentation-summary">
            {text(segmentation.recommended_episode_count) ? <BriefMeta label="推荐集数" value={`${text(segmentation.recommended_episode_count)} 集`} /> : null}
            {text(segmentation.target_duration_seconds) ? <BriefMeta label="单集时长" value={seconds(segmentation.target_duration_seconds)} /> : null}
            {text(platform.rhythm_strategy) ? <BriefMeta label="节奏" value={text(platform.rhythm_strategy)} /> : null}
          </div>
          {segmentationRows.length ? <BriefDefinitionList rows={segmentationRows} /> : null}
        </BriefSection>
      ) : null}

      {risks.length || reviews.length ? (
        <BriefSection title="风险与复核">
          <div className="reading-risk-list">
            {[...risks.map((item) => ({ ...item, kind: "risk" })), ...reviews.map((item) => ({ ...item, kind: "review" }))]
              .sort((left, right) => severityRank(right.severity) - severityRank(left.severity))
              .map((item, index) => (
                <article className={`reading-risk-item ${severityTone(item.severity)}`} key={`${text(item.risk_type) || text(item.field)}-${index}`}>
                  <header><strong>{readingLabel("risk", item.risk_type || item.field) || (item.kind === "risk" ? "生产风险" : "人工复核")}</strong>{text(item.severity) ? <span>{severityLabel(item.severity)}</span> : null}</header>
                  <p>{text(item.description) || text(item.question) || text(item.item)}</p>
                  {text(item.suggestion) ? <em>{text(item.suggestion)}</em> : null}
                </article>
              ))}
          </div>
        </BriefSection>
      ) : null}

      {(latestRun || Object.keys(source).length) ? (
        <details className="reading-runtime-details">
          <summary>运行信息</summary>
          <div>
            <BriefMeta label="Skill" value={text(source.skill) || "script-reading"} />
            <BriefMeta label="版本" value={text(source.skill_version || source.contract_version)} />
            <BriefMeta label="模型" value={text(source.model) || text(report.llm_model)} />
            <BriefMeta label="运行时间" value={duration} />
          </div>
        </details>
      ) : null}
    </div>
  );
}

function BriefSection({ title, className = "", children }: { title: string; className?: string; children: React.ReactNode }) {
  return <section className={`reading-brief-section ${className}`.trim()}><h4>{title}</h4>{children}</section>;
}

function BriefMeta({ label, value }: { label: string; value: string }) {
  if (!value) return null;
  return <div className="reading-brief-meta-item"><span>{label}</span><strong>{value}</strong></div>;
}

function BriefDefinitionList({ rows }: { rows: BriefRow[] }) {
  return <dl className="reading-brief-definitions">{rows.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>;
}

const SEGMENT_BEAT_TAGS = ["压抑高潮", "过渡低谷", "过度低谷", "转折高潮", "悬念结尾"];

function highlightSegmentBeatTags(value: string) {
  if (!value) return "";
  const pattern = new RegExp(`(${SEGMENT_BEAT_TAGS.map(escapeRegExp).join("|")})`, "g");
  const parts = value.split(pattern);
  return parts.map((part, index) => (
    SEGMENT_BEAT_TAGS.includes(part)
      ? <span className="reading-beat-tag" key={`${part}-${index}`}>{part}</span>
      : <React.Fragment key={`${part}-${index}`}>{part}</React.Fragment>
  ));
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function records(value: unknown): Record<string, unknown>[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (item && typeof item === "object" && !Array.isArray(item)) return [item as Record<string, unknown>];
    const itemText = text(item);
    return itemText ? [{ item: itemText }] : [];
  });
}

function strings(value: unknown): string[] {
  if (Array.isArray(value)) return value.map((item) => {
    if (item && typeof item === "object") {
      const source = item as Record<string, unknown>;
      return text(source.label || source.name || source.value || source.color || source.hex);
    }
    return text(item);
  }).filter(Boolean);
  return text(value).split(/[、，,；;\/]+/).map((item) => item.trim()).filter(Boolean);
}

function text(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (Array.isArray(value)) return value.map(text).filter(Boolean).join("、");
  if (typeof value === "object") return "";
  return String(value).trim();
}

function seconds(value: unknown) {
  const output = text(value);
  return output ? `${output} 秒` : "";
}

const FALLBACK_SWATCHES = ["#b42318", "#1d4ed8", "#0f766e", "#a16207", "#7e22ce", "#475569"];
const NAMED_SWATCHES: Array<[RegExp, string]> = [
  [/冷蓝/i, "#3b82f6"],
  [/琥珀|amber/i, "#f59e0b"],
  [/阴影|shadow/i, "#1e3a5f"],
  [/金高光|金|gold/i, "#ca8a04"],
  [/朱红|猩红|scarlet/i, "#b42318"],
  [/红|red|crimson/i, "#dc2626"],
  [/黄|yellow/i, "#eab308"],
  [/青|cyan|teal/i, "#0f766e"],
  [/绿|green/i, "#15803d"],
  [/蓝|blue/i, "#2563eb"],
  [/橙|orange/i, "#ea580c"],
  [/紫|purple/i, "#7e22ce"],
  [/黑|black/i, "#18181b"],
  [/白|white/i, "#f8fafc"],
  [/灰|gray|grey/i, "#64748b"],
];

function swatchColor(value: string, index: number) {
  if (/^#[0-9a-f]{3,8}$/i.test(value) || /^rgb/i.test(value)) return value;
  return NAMED_SWATCHES.find(([pattern]) => pattern.test(value))?.[1] || FALLBACK_SWATCHES[index % FALLBACK_SWATCHES.length];
}

function severityRank(value: unknown) {
  const normalized = text(value).toLowerCase();
  return normalized === "critical" || normalized === "严重" ? 4 : normalized === "high" || normalized === "高" ? 3 : normalized === "medium" || normalized === "中" ? 2 : 1;
}

function severityTone(value: unknown) {
  const rank = severityRank(value);
  return rank >= 4 ? "critical" : rank === 3 ? "high" : rank === 2 ? "medium" : "low";
}

function severityLabel(value: unknown) {
  const rank = severityRank(value);
  return rank >= 4 ? "严重" : rank === 3 ? "高" : rank === 2 ? "中" : "低";
}
