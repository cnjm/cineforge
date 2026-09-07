package service

// P4e project-query（对齐 legacy app/services/query_index.py）：
// 本地向量检索（token_hash_v1），文档来源 = 项目 + 脚本段 + 分镜 + 任务（含 submissions）。
// 非 director 用户仅检索自己可见任务及关联分镜；可见任务为空 → 整个文档集为空。
//
// 契约：
//   - POST /projects/{project_id}/query-agent（any authenticated）
//   - question 1..1000 字符，limit 1..10；404/403 与 run-events 一致（Project not found / Project permission denied）
//   - retrieval 固定常量 {mode:"local_vector", embedding:"token_hash_v1", service:"project_query", agent:false}
//   - score round(score,4)；answer 模板字符串与 legacy 逐字一致

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"cineforge/server/internal/model"
)

// QueryAgentRequestItem 对齐 QueryAgentRequest。
type QueryAgentRequestItem struct {
	Question string `json:"question"`
	Limit    int    `json:"limit"`
}

// QueryAgentReferenceItem 对齐 QueryAgentReference。
type QueryAgentReferenceItem struct {
	SourceType string         `json:"source_type"`
	SourceID   string         `json:"source_id"`
	Title      string         `json:"title"`
	Score      float64        `json:"score"`
	Excerpt    string         `json:"excerpt"`
	Metadata   map[string]any `json:"metadata"`
}

// QueryAgentResponseItem 对齐 QueryAgentResponse。
type QueryAgentResponseItem struct {
	Question      string                       `json:"question"`
	Answer        string                       `json:"answer"`
	References    []QueryAgentReferenceItem    `json:"references"`
	DocumentCount int                          `json:"document_count"`
	Retrieval     map[string]any               `json:"retrieval"`
}

var (
	queryLatinTokenRe = regexp.MustCompile(`[a-z0-9_:\-/]{2,}`)
	queryCJKTokenRe   = regexp.MustCompile(`[一-龥]{2,}`)
)

const (
	queryExcerptMaxLen    = 180
	queryExcerptRadius    = 60
	queryKeywordBonusCap  = 0.2
	queryAnswerEmptyReply = "当前项目上下文中没有检索到足够相关的信息。建议补充更具体的项目、分镜、角色、资产或任务关键词。"
	queryAnswerFooter     = "以上结果来自结构化项目数据检索，仍需导演按实际剧本和资产状态确认。"
)

// queryIndexDocument 检索文档（vector 为内部字段，不序列化）。
type queryIndexDocument struct {
	sourceType string
	sourceID   string
	title      string
	content    string
	metadata   map[string]any
	vector     map[string]float64
}

// queryScoreDoc 匹配文档 + score（已 round4），answer/references 共用。
type queryScoreDoc struct {
	doc   queryIndexDocument
	score float64
}

func queryAnswerIntro(question string) string {
	return fmt.Sprintf("根据当前项目上下文，和“%s”最相关的信息如下：", question)
}

// ProjectQuery 执行项目检索（对齐 legacy query-agent 端点）。
func (s *ProjectService) ProjectQuery(ctx context.Context, userID, role, projectID, question string, limit int) (*QueryAgentResponseItem, error) {
	if question == "" || len(question) > 1000 {
		return nil, badRequest400("question 长度需在 1..1000 之间")
	}
	if limit < 1 || limit > 10 {
		return nil, badRequest400("limit 需在 1..10 之间")
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	docs, err := s.buildProjectQueryDocuments(ctx, project, userID, role)
	if err != nil {
		return nil, err
	}
	matched := searchProjectQueryDocs(question, docs, limit)
	return &QueryAgentResponseItem{
		Question:      question,
		Answer:        answerProjectQuery(question, matched),
		References:    projectQueryReferences(question, matched),
		DocumentCount: len(docs),
		Retrieval: map[string]any{
			"mode":      "local_vector",
			"embedding": "token_hash_v1",
			"service":   "project_query",
			"agent":     false,
		},
	}, nil
}

// buildProjectQueryDocuments 构建项目检索文档集（对齐 build_project_query_documents）。
func (s *ProjectService) buildProjectQueryDocuments(ctx context.Context, project *model.Projects, userID, role string) ([]queryIndexDocument, error) {
	restrict := !isDirectorOrAdminRole(role)

	var taskIDs []string
	var err error
	if restrict {
		taskIDs, err = s.projects.ListVisibleTaskIDs(ctx, project.ID, userID)
		if err != nil {
			return nil, err
		}
		if len(taskIDs) == 0 {
			return []queryIndexDocument{}, nil // 非 director 无可见任务：整个文档集为空
		}
	}

	docs := []queryIndexDocument{projectQueryDoc(project)}

	segments, err := s.projects.ListScriptSegments(ctx, project.ID, nil, "")
	if err != nil {
		return nil, err
	}
	for i := range segments {
		docs = append(docs, scriptSegmentQueryDoc(&segments[i]))
	}

	var storyboardIDs []string
	if restrict {
		storyboardIDs, err = s.projects.ListStoryboardIDsForTasks(ctx, taskIDs)
		if err != nil {
			return nil, err
		}
	}
	storyboards, err := s.projects.ListStoryboardsForIndex(ctx, project.ID, storyboardIDs, restrict)
	if err != nil {
		return nil, err
	}
	for i := range storyboards {
		docs = append(docs, storyboardQueryDoc(&storyboards[i]))
	}

	tasks, submissions, err := s.projects.ListTasksForIndex(ctx, project.ID, taskIDs, restrict)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		docs = append(docs, taskQueryDoc(t, submissions[t.ID]))
	}
	return docs, nil
}

func projectQueryDoc(p *model.Projects) queryIndexDocument {
	var contentLines []string
	contentLines = appendNonEmpty(contentLines, p.Title)
	contentLines = appendNonEmpty(contentLines, ptrStr(p.Genre))
	contentLines = appendNonEmpty(contentLines, ptrStr(p.ScriptText))
	contentLines = appendNonEmpty(contentLines, p.Status)
	content := strings.Join(contentLines, "\n")
	return queryIndexDocument{
		sourceType: "project",
		sourceID:   p.ID,
		title:      p.Title,
		content:    content,
		metadata: map[string]any{
			"project_id":     p.ID,
			"project_prefix": ptrStr(p.ProjectPrefix),
			"project_no":     ptrStr(p.ProjectNo),
		},
		vector: projectQueryEmbed(projectQueryTokens(content)),
	}
}

func scriptSegmentQueryDoc(seg *model.ScriptSegments) queryIndexDocument {
	title := seg.Title
	if title == nil || *title == "" {
		t := seg.ScriptSegmentCode
		title = &t
	}
	var contentLines []string
	contentLines = appendNonEmpty(contentLines, seg.ScriptSegmentCode)
	contentLines = appendNonEmpty(contentLines, strOfNil(seg.Title))
	contentLines = appendNonEmpty(contentLines, seg.SourceText)
	contentLines = appendNonEmpty(contentLines, strOfNil(seg.Summary))
	contentLines = appendNonEmpty(contentLines, strOfNil(seg.StoryFunction))
	contentLines = appendNonEmpty(contentLines, strOfNil(seg.DominantEmotion))
	content := strings.Join(contentLines, "\n")
	return queryIndexDocument{
		sourceType: "script_segment",
		sourceID:   seg.ID,
		title:      *title,
		content:    content,
		metadata: map[string]any{
			"project_id":          seg.ProjectId,
			"episode_code":        ptrStr(seg.EpisodeCode),
			"script_segment_code": seg.ScriptSegmentCode,
		},
		vector: projectQueryEmbed(projectQueryTokens(content)),
	}
}

func storyboardQueryDoc(sb *model.Storyboards) queryIndexDocument {
	title := sb.Title
	if title == nil || *title == "" {
		t := fmt.Sprintf("分镜 %03d", sb.OrderNum)
		title = &t
	}
	var contentLines []string
	contentLines = appendNonEmpty(contentLines, strOfNil(sb.Title))
	contentLines = appendNonEmpty(contentLines, sb.Description)
	contentLines = appendNonEmpty(contentLines, strOfNil(sb.Dialogue))
	contentLines = appendNonEmpty(contentLines, strOfNil(sb.Camera))
	if chars := storyboardCharacters(sb.Characters); len(chars) > 0 {
		contentLines = appendNonEmpty(contentLines, strings.Join(chars, " "))
	}
	content := strings.Join(contentLines, "\n")
	metadata := map[string]any{
		"project_id": sb.ProjectId,
		"episode_num": sb.EpisodeNum,
		"order_num":   sb.OrderNum,
		"script_segment_id": storyboardScriptSegmentID(sb.ScriptSegmentId),
	}
	if sb.DurationSeconds != nil {
		metadata["duration_seconds"] = *sb.DurationSeconds
	}
	return queryIndexDocument{
		sourceType: "storyboard",
		sourceID:   sb.ID,
		title:      *title,
		content:    content,
		metadata:   metadata,
		vector:     projectQueryEmbed(projectQueryTokens(content)),
	}
}

func taskQueryDoc(t *model.Tasks, subs []*model.Submissions) queryIndexDocument {
	var contentLines []string
	contentLines = appendNonEmpty(contentLines, t.Title)
	contentLines = appendNonEmpty(contentLines, t.TaskType)
	contentLines = appendNonEmpty(contentLines, t.Status)
	contentLines = appendNonEmpty(contentLines, strOfNil(t.PromptText))
	contentLines = appendNonEmpty(contentLines, strOfNil(t.LatestPromptText))
	contentLines = appendNonEmpty(contentLines, strOfNil(t.ProductionModel))
	if subText := submissionsQueryText(subs); subText != "" {
		contentLines = appendNonEmpty(contentLines, subText)
	}
	content := strings.Join(contentLines, "\n")
	return queryIndexDocument{
		sourceType: "task",
		sourceID:   t.ID,
		title:      t.Title,
		content:    content,
		metadata: map[string]any{
			"project_id":           t.ProjectId,
			"task_type":            t.TaskType,
			"status":               t.Status,
			"storyboard_id":        ptrStr(t.StoryboardId),
			"script_segment_id":    ptrStr(t.ScriptSegmentId),
			"submission_count":     len(subs),
		},
		vector: projectQueryEmbed(projectQueryTokens(content)),
	}
}

// submissionsQueryText 对齐 legacy submission 文档文本拼装。
func submissionsQueryText(subs []*model.Submissions) string {
	var out []string
	for _, s := range subs {
		var parts []string
		parts = append(parts, s.FileType)
		parts = append(parts, s.FilePath)
		parts = append(parts, strOfNil(s.PromptText))
		parts = append(parts, strOfNil(s.RevisedPromptText))
		parts = append(parts, strOfNil(s.ModelName))
		if tools := stringSliceJSON(s.ToolNames); len(tools) > 0 {
			parts = append(parts, strings.Join(tools, " "))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return strings.Join(out, "\n")
}

// searchProjectQueryDocs 对齐 search_documents：cosine + 关键词加成，score>0 保留，按 score 降序稳定排序，取前 limit。
func searchProjectQueryDocs(question string, docs []queryIndexDocument, limit int) []queryScoreDoc {
	qvec := projectQueryEmbed(projectQueryTokens(question))
	var out []queryScoreDoc
	for _, d := range docs {
		score := queryCosine(qvec, d.vector) + queryKeywordBonus(question, d.content)
		if score <= 0 {
			continue
		}
		out = append(out, queryScoreDoc{doc: d, score: round4(score)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// projectQueryReferences 对齐 search_documents 的返回映射：{source_type, source_id, title, score, excerpt, metadata}。
func projectQueryReferences(question string, matched []queryScoreDoc) []QueryAgentReferenceItem {
	out := make([]QueryAgentReferenceItem, 0, len(matched))
	for _, m := range matched {
		out = append(out, QueryAgentReferenceItem{
			SourceType: m.doc.sourceType,
			SourceID:   m.doc.sourceID,
			Title:      m.doc.title,
			Score:      m.score,
			Excerpt:    queryExcerpt(m.doc.content, question),
			Metadata:   m.doc.metadata,
		})
	}
	return out
}

func answerProjectQuery(question string, matched []queryScoreDoc) string {
	if len(matched) == 0 {
		return queryAnswerEmptyReply
	}
	lines := []string{queryAnswerIntro(question)}
	for i, m := range matched {
		lines = append(lines, fmt.Sprintf("%d. %s（%s，相关度 %s）：%s",
			i+1, m.doc.title, m.doc.sourceType, strconv.FormatFloat(m.score, 'g', -1, 64), queryExcerpt(m.doc.content, question)))
	}
	lines = append(lines, queryAnswerFooter)
	return strings.Join(lines, "\n")
}

// projectQueryTokens 对齐 legacy _tokens：小写后提取拉丁 token（len≥2）与 CJK bigram/trigram。
func projectQueryTokens(text string) []string {
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	var out []string
	for _, m := range queryLatinTokenRe.FindAllString(lower, -1) {
		out = append(out, m)
	}
	for _, run := range queryCJKTokenRe.FindAllString(lower, -1) {
		runes := []rune(run)
		n := len(runes)
		for i := 0; i+1 < n; i++ {
			out = append(out, string(runes[i:i+2]))
		}
		if n >= 3 {
			for i := 0; i+2 < n; i++ {
				out = append(out, string(runes[i:i+3]))
			}
		}
	}
	return out
}

// projectQueryEmbed 对齐 legacy _embed：TF 向量 → L2 归一化；空 token → 空向量。
func projectQueryEmbed(tokens []string) map[string]float64 {
	v := make(map[string]float64)
	for _, t := range tokens {
		v[t]++
	}
	var norm float64
	for _, c := range v {
		norm += c * c
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return map[string]float64{}
	}
	for k, c := range v {
		v[k] = c / norm
	}
	return v
}

// queryCosine 对齐 legacy _cosine：在较小 map 的键上做点积（归一化后即余弦）。
func queryCosine(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot float64
	if len(a) <= len(b) {
		for k, va := range a {
			dot += va * b[k]
		}
	} else {
		for k, vb := range b {
			dot += vb * a[k]
		}
	}
	return dot
}

// queryKeywordBonus 对齐 legacy _keyword_bonus：min(0.2, overlap/|tokens(q)|*0.2)。
func queryKeywordBonus(question, content string) float64 {
	qt := projectQueryTokens(question)
	if len(qt) == 0 {
		return 0
	}
	qset := make(map[string]bool, len(qt))
	for _, t := range qt {
		qset[t] = true
	}
	cset := make(map[string]bool)
	for _, t := range projectQueryTokens(content) {
		cset[t] = true
	}
	overlap := 0
	for t := range qset {
		if cset[t] {
			overlap++
		}
	}
	bonus := float64(overlap) / float64(len(qset)) * queryKeywordBonusCap
	if bonus > queryKeywordBonusCap {
		bonus = queryKeywordBonusCap
	}
	return bonus
}

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// queryExcerpt 对齐 legacy _excerpt：空白折叠，命中位置 ±60，前后补 "..."，截断 180。
func queryExcerpt(content, question string) string {
	compact := strings.Join(strings.Fields(content), " ")
	if len(compact) <= queryExcerptMaxLen {
		return compact
	}
	lower := strings.ToLower(compact)
	hit := -1
	for _, tok := range projectQueryTokens(question) {
		if idx := strings.Index(lower, tok); idx >= 0 && (hit < 0 || idx < hit) {
			hit = idx
		}
	}
	if hit < 0 {
		return compact[:queryExcerptMaxLen] + "..."
	}
	start := hit - queryExcerptRadius
	if start < 0 {
		start = 0
	}
	out := ""
	if start > 0 {
		out += "..."
	}
	end := start + queryExcerptMaxLen
	if end > len(compact) {
		end = len(compact)
	}
	return out + compact[start:end] + "..."
}

func appendNonEmpty(lines []string, s string) []string {
	if s != "" {
		return append(lines, s)
	}
	return lines
}

func strOfNil(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func storyboardCharacters(raw json.RawMessage) []string {
	var chars []string
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &chars); err != nil {
		return nil
	}
	return chars
}

func stringSliceJSON(raw json.RawMessage) []string {
	var out []string
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func storyboardScriptSegmentID(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}