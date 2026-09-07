package api

// P4e Agent 定义与 stage-map（对齐 legacy app/api/routes/agents.py 的静态端点）：
//
//   - GET /agents              → list[AgentDefinitionRead]（注册插入顺序）
//   - GET /agents/stage-map    → list[{stage, agents[]}]（AGENT_STAGE_MAP 顺序）
//   - GET /agents/{agent_type} → AgentDefinitionRead（非法类型 422；未注册 404 "Agent not found"）
//
// 静态数据在 agents_registry_data.go（loadAgentDefinitions / agentRegistryStageKeys），
// 由 legacy registry/skill_contracts/display_contracts 移植生成。

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AgentDefinitionRead 对齐 legacy AgentDefinitionRead（schemas/agents.py）。
type AgentDefinitionRead struct {
	Key           string         `json:"key"`
	Name          string         `json:"name"`
	Skill         string         `json:"skill"`
	Description   string         `json:"description"`
	Stage         string         `json:"stage"`
	Capabilities  []string       `json:"capabilities"`
	InputSchema   map[string]any `json:"input_schema"`
	OutputSchema  map[string]any `json:"output_schema"`
	RuntimePolicy map[string]any `json:"runtime_policy"`
	DisplaySchema map[string]any `json:"display_schema"`
	Version       string         `json:"version"`
	Enabled       bool           `json:"enabled"`
}

// AgentStageMapItem 对齐 legacy AgentStageMapItem。
type AgentStageMapItem struct {
	Stage  string                `json:"stage"`
	Agents []AgentDefinitionRead `json:"agents"`
}

// agentStageMapEntry 是 agentRegistryStageKeys() 返回的元素（stage → agent keys）。
type agentStageMapEntry struct {
	Stage string   `json:"stage"`
	Keys  []string `json:"keys"`
}

// handleListAgents 对齐 legacy GET /agents。
func (s *Server) handleListAgents(c *gin.Context) {
	c.JSON(http.StatusOK, loadAgentDefinitions())
}

// handleAgentStageMap 对齐 legacy GET /agents/stage-map。
func (s *Server) handleAgentStageMap(c *gin.Context) {
	defs := loadAgentDefinitions()
	byKey := make(map[string]AgentDefinitionRead, len(defs))
	for _, d := range defs {
		byKey[d.Key] = d
	}
	var items []AgentStageMapItem
	for _, entry := range agentRegistryStageKeys() {
		item := AgentStageMapItem{Stage: entry.Stage, Agents: make([]AgentDefinitionRead, 0, len(entry.Keys))}
		for _, key := range entry.Keys {
			if def, ok := byKey[key]; ok {
				item.Agents = append(item.Agents, def)
			}
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, items)
}

// handleGetAgent 对齐 legacy GET /agents/{agent_type}。
// 路径参数在 FastAPI 侧为 ProductionAgentKind 枚举：非法值 → 422；
// 生产类型但 registry 未注册（理论上不可能，11 个全注册）→ 404 "Agent not found"。
func (s *Server) handleGetAgent(c *gin.Context) {
	key := c.Param("agent_type")
	defs := loadAgentDefinitions()
	for _, d := range defs {
		if d.Key == key {
			c.JSON(http.StatusOK, d)
			return
		}
	}
	unprocessableEntity(c, "Unknown agent type: "+key)
}

// agentDefinitionExists 是否注册的 Agent 类型（POST /agent-jobs 的 404 "Agent not found" 依据；
// legacy 在 orchestrator.submit_background 里 registry.get(AgentKind) 抛 KeyError → 404）。
func agentDefinitionExists(key string) bool {
	for _, d := range loadAgentDefinitions() {
		if d.Key == key {
			return true
		}
	}
	return false
}

// agentStageOK 返回 "某 stage 是否放行该 agent_type" 回调，供 SubmitProjectAgentJob 校验。
// 对齐 legacy create_project_agent_job 的 `agent_type in AGENT_STAGE_MAP.get(stage, [])`。
func agentStageOK(agentType string) func(stage string) bool {
	allowed := map[string]bool{}
	for _, entry := range agentRegistryStageKeys() {
		for _, k := range entry.Keys {
			if k == agentType {
				allowed[entry.Stage] = true
			}
		}
	}
	return func(stage string) bool { return allowed[stage] }
}

// agentKindEnum 对齐 legacy AgentKind（含 DB 兼容历史值与 5 个不可执行兼容值）。
var agentKindEnum = map[string]bool{
	"script_reading": true, "script_segmentation": true, "script_breakdown": true,
	"content_compliance_review": true, "asset_extract": true, "relation_check": true,
	"character_design_prompt": true, "scene_design_prompt": true, "prop_design_prompt": true,
	"text_to_image_prompt": true, "image_to_video_prompt": true,
	"asset_match": true, "asset_confirm": true, "video_prompt": true, "review": true, "query": true,
}

// productionAgentKindEnum 对齐 legacy ProductionAgentKind（当前生产 API 接受的 11 个类型）。
var productionAgentKindEnum = map[string]bool{
	"script_reading": true, "script_segmentation": true, "script_breakdown": true,
	"content_compliance_review": true, "asset_extract": true, "relation_check": true,
	"character_design_prompt": true, "scene_design_prompt": true, "prop_design_prompt": true,
	"text_to_image_prompt": true, "image_to_video_prompt": true,
}

// validAgentKind 判断 agent_type 是否为合法 AgentKind（GET /agent-training-samples 的 422 依据）。
func validAgentKind(key string) bool { return agentKindEnum[key] }

// validProductionAgentKind 判断 agent_type 是否为合法 ProductionAgentKind
// （POST /agent-training-samples 的 422 依据，legacy Pydantic 枚举）。
func validProductionAgentKind(key string) bool { return productionAgentKindEnum[key] }
