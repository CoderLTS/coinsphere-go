// nodes_agent.go —— 把"智能体"接进工作流编排。
//
// 助手页是"人和智能体对话";这里是"工作流让智能体干活":一个 assistant.agent 节点
// 按配置挑一个智能体、拼好提示词、调一次模型,把整段回复写进节点输出,下游节点即可引用。
// 编排场景不需要逐字流式,所以走 runAgentOnce 收完再返回(见 assistant.go)。
//
// 加这个节点没有改引擎、没有改校验器:它只是往节点注册表里登记一项 —— 这正是
// 上一轮把 Kind / Branches / ConfigSchema 提到注册表上想达到的效果。

package service

import (
	"context"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "assistant.agent", Label: "智能体",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"agentCode": M{"type": "string", "title": "智能体编码"},
				"promptTemplate": M{
					"type": "string", "title": "提示词",
					"description": "支持 {{路径}} 引用共享状态,例如 {{taskResult.insertedItems.0.title}}",
				},
				"analyze": M{
					"type": "boolean", "title": "使用数据源的结构化分析模板", "default": false,
					"description": "开启后忽略上面的提示词,改用智能体数据源自带的分析指令(仅新闻这类支持分析的数据源可用)",
				},
				"refIdPath": M{
					"type": "string", "title": "关联数据 id 路径",
					"description": "数据源需要外部 id 时(如新闻上下文)从共享状态的这个路径取,例如 currentItem.id",
				},
				"modelConfigId": M{
					"type": "integer", "title": "指定模型配置 id",
					"description": "留空则用该智能体绑定的模型;都没有就用工作流创建者启用的第一个模型",
				},
				"outputKey": M{
					"type": "string", "title": "结果写入共享状态的键名", "default": "agentResult",
				},
			},
			"required": []string{"agentCode"},
		},
		Execute: agentNodeExecute,
	})
}

// agentNodeExecute 执行一次智能体调用。
//
// 输出:{content, reasoning, agentCode, modelConfigId, promptTokens, completionTokens, totalTokens}。
// 同时按 outputKey 写进共享状态,方便下游节点直接引用(比如把 content 丢给 notify 节点发出去)。
func agentNodeExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	agentCode := cfgStr(config, "agentCode", "")
	if agentCode == "" {
		return nil, bizErr("智能体节点缺少 agentCode")
	}
	agent, err := ctx.App.requireEnabledAgent(agentCode)
	if err != nil {
		return nil, err
	}

	// 数据源需要外部 id 时,从共享状态里按配置的路径取。
	source := getAgentDataSource(agent.DataSourceType)
	if source == nil {
		return nil, bizErr("智能体 %s 配置了未知的数据源类型: %s", agent.Code, agent.DataSourceType)
	}
	var refID *int64
	if source.RequiresRefID {
		refPath := cfgStr(config, "refIdPath", "")
		if refPath == "" {
			return nil, bizErr("智能体 %s 需要关联数据,请配置 refIdPath", agent.Code)
		}
		value := asInt64(ctx.State.get(refPath))
		if value <= 0 {
			return nil, bizErr("按路径 %s 没取到有效的关联数据 id", refPath)
		}
		refID = &value
	}
	agentCtx, err := ctx.App.resolveAgentContext(agent, refID)
	if err != nil {
		return nil, err
	}

	// analyze 走数据源自带的分析模板;否则用节点上配的提示词(已由 nodeConfig 渲染过 {{路径}})。
	analyze := isTruthy(config["analyze"]) && source.supportsAnalyze()
	prompt := cfgStr(config, "promptTemplate", "")
	if !analyze && prompt == "" {
		return nil, bizErr("智能体节点需要填写提示词,或改用数据源的结构化分析模板")
	}
	// 工作流里没有"对话历史"这个概念,每个节点都是一次独立调用。
	messages := buildAgentMessages(agent, nil, analyze, prompt, agentCtx, false)

	runtimeConfig, err := ctx.App.resolveWorkflowAgentModel(ctx.Definition, agent, asInt64(config["modelConfigId"]))
	if err != nil {
		return nil, err
	}

	// 单个节点也要有自己的超时:整图的 ctx 可能很宽松,但一次模型调用不该无限期挂着。
	timeout := time.Duration(ctx.App.Cfg.Assistant.AgentNodeTimeoutMs) * time.Millisecond
	callCtx, cancel := context.WithTimeout(ctx.Ctx, timeout)
	defer cancel()

	result, err := ctx.App.runAgentOnce(callCtx, runtimeConfig, messages)
	if err != nil {
		return nil, err
	}
	if result.Content == "" {
		return nil, bizErr("智能体 %s 没有返回任何内容", agent.Code)
	}

	output := M{
		"agentCode": agent.Code, "agentName": agent.DisplayName,
		"modelConfigId": runtimeConfig.ConfigID, "modelDisplayName": runtimeConfig.DisplayName,
		"content": result.Content, "reasoning": result.Reasoning,
		"promptTokens": result.Usage.PromptTokens, "completionTokens": result.Usage.CompletionTokens,
		"totalTokens": result.Usage.TotalTokens,
	}
	ctx.State.set(cfgStr(config, "outputKey", "agentResult"), output)
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

// resolveWorkflowAgentModel 解析工作流里该用哪个模型配置。
//
// 难点是"归属":模型配置是按用户存的(owner_id),而工作流是后台 worker 在跑,没有登录用户。
// 这里用工作流定义的创建者当归属人 —— 谁画的流程,就用谁配的模型,语义上也说得通。
// 优先级:节点显式指定 > 智能体绑定的模型 > 该用户启用的第一个模型。
func (a *App) resolveWorkflowAgentModel(definition *db.WorkflowDefinition, agent *db.AssistantAgent, explicitModelID int64) (*aiRuntimeConfig, error) {
	if definition == nil || definition.CreatedBy == nil {
		return nil, bizErr("工作流定义缺少创建者信息,无法确定使用哪个用户的模型配置")
	}
	ownerID := *definition.CreatedBy

	if explicitModelID > 0 {
		return a.getAiRuntimeConfig(explicitModelID, ownerID, true)
	}
	enabled, err := a.listModelConfigs(ownerID, true)
	if err != nil {
		return nil, err
	}
	config := a.pickDefaultModel(ownerID, agent.ID, enabled)
	if config == nil {
		return nil, bizErr("工作流创建者没有可用的模型配置,请先在模型配置中启用模型")
	}
	return a.getAiRuntimeConfig(config.ID, ownerID, true)
}

// ---------- 编辑器面板用的智能体选项 ----------

// ListWorkflowAgentOptions 工作流编辑器里 assistant.agent 节点的智能体下拉选项。
// 同时告诉前端这个智能体要不要 refIdPath、支不支持结构化分析,好按需显示对应输入项。
func (a *App) ListWorkflowAgentOptions() []M {
	var agents []db.AssistantAgent
	a.DB.Where("is_enabled = ?", true).Order("sort ASC, id ASC").Find(&agents)
	options := make([]M, 0, len(agents))
	for i := range agents {
		agent := &agents[i]
		source := getAgentDataSource(agent.DataSourceType)
		item := M{
			"code": agent.Code, "label": agent.DisplayName,
			"description":     strings.TrimSpace(agent.Description),
			"dataSourceType":  agent.DataSourceType,
			"requiresRefId":   false,
			"supportsAnalyze": false,
		}
		if source != nil {
			item["dataSourceLabel"] = source.Label
			item["requiresRefId"] = source.RequiresRefID
			item["supportsAnalyze"] = source.supportsAnalyze()
		}
		options = append(options, item)
	}
	return options
}
