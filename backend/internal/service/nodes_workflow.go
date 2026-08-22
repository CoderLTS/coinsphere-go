// nodes_workflow.go —— 调用子工作流的节点。
//
// 把一段常用流程抽成独立工作流,再在别处用 workflow.call 复用它 —— 这是"编排"能复合起来的关键。
//
// 两个必须防住的坑:
//  1. 递归。A 调 B、B 又调 A 会无限展开。这里在共享状态里带一条调用链,
//     链上出现过的 code 直接拒绝,并限制总深度。
//  2. 子流程始终异步派发，父执行不占槽轮询等待。

package service

import (
	"strings"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"
)

// 调用链在共享状态里的键,以及允许的最大嵌套层数。
const (
	workflowCallChainKey = "_workflowCallChain"
	workflowCallMaxDepth = 5
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "workflow.call", Label: "调用子工作流", RequiredPermission: perm.SchedulerWorkflowDefinitionsRun,
		InputPorts: []workflowNodePortDefinition{
			nodePort("inputs", "运行输入", false, M{"type": "object"}),
		},
		OutputPorts: []workflowNodePortDefinition{
			nodePort("result", "调用结果", false, M{"type": "object"}),
			nodePort("executionId", "子执行 ID", false, M{"type": "integer"}),
		},
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"workflowCode": M{"type": "string", "title": "目标工作流", "resource": "workflow-code"},
				"entryKey":     M{"type": "string", "title": "开始入口", "description": "选择目标工作流中要触发的开始节点"},
			},
			"required": []string{"workflowCode", "entryKey"},
		},
		Execute: workflowCallExecute,
	})
}

func workflowCallExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	if err := ctx.Ctx.Err(); err != nil {
		return nil, err
	}
	config := nodeConfig(ctx)
	workflowCode := cfgStr(config, "workflowCode", "")
	entryKey := cfgStr(config, "entryKey", "")
	if workflowCode == "" || entryKey == "" {
		return nil, bizErr("子工作流节点需要同时指定 workflowCode 与 entryKey")
	}
	// 递归防护要在做任何事之前。
	chain, err := nextWorkflowCallChain(ctx, workflowCode)
	if err != nil {
		return nil, err
	}

	entry, err := ctx.App.requireActiveRuntimeEntry(ctx.Execution.OwnerUserID, workflowCode, entryKey)
	if err != nil {
		return nil, err
	}

	inputs, _ := ctx.Inputs["inputs"].(map[string]any)
	if inputs == nil {
		inputs = M{}
	}
	triggerCtx := M{
		"triggerType": "workflow",
		"triggerKey": "workflow-call:" + ctx.Definition.Code + ":" + asString(ctx.Node["id"]) +
			":" + int64Text(ctx.Execution.ID),
		"payload": M{
			"parentExecutionId":   ctx.Execution.ID,
			"parentWorkflowCode":  ctx.Definition.Code,
			"parentNodeId":        asString(ctx.Node["id"]),
			"workflowCallChain":   chain,
			"parentStartEntryKey": ctx.Execution.StartEntryKey,
		},
		"inputs": inputs,
	}

	started, err := ctx.App.RunRuntimeEntry(entry.ID, triggerCtx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Ctx.Err(); err != nil {
		return nil, err
	}
	execution, _ := started["execution"].(M)
	executionID := asInt64(execution["id"])

	output := M{
		"workflowCode": workflowCode, "entryKey": entryKey,
		"executionId": executionID, "duplicate": started["duplicate"],
		"status": asString(execution["status"]),
	}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

// nextWorkflowCallChain 算出加上本次调用后的调用链,顺便挡住递归与过深嵌套。
//
// 链条存在共享状态里,并通过 triggerCtx.payload 传给子工作流 —— 子工作流启动时会把它
// 读回自己的共享状态(见 loadWorkflowCallChain),这样多层嵌套也能连成一条完整的链。
func nextWorkflowCallChain(ctx *nodeExecContext, targetCode string) ([]any, error) {
	current, _ := ctx.State.get(workflowCallChainKey).([]any)
	chain := make([]any, 0, len(current)+2)
	chain = append(chain, current...)
	// 当前工作流自己也要在链上,否则"A 调 B、B 调 A"这种两层环检测不出来。
	if len(chain) == 0 {
		chain = append(chain, ctx.Definition.Code)
	}
	for _, item := range chain {
		if asString(item) == targetCode {
			return nil, bizErr("检测到工作流循环调用: %s", strings.Join(chainCodes(append(chain, targetCode)), " → "))
		}
	}
	chain = append(chain, targetCode)
	if len(chain) > workflowCallMaxDepth {
		return nil, bizErr("子工作流嵌套层数超过上限 %d: %s", workflowCallMaxDepth, strings.Join(chainCodes(chain), " → "))
	}
	return chain, nil
}

func chainCodes(chain []any) []string {
	codes := make([]string, 0, len(chain))
	for _, item := range chain {
		codes = append(codes, asString(item))
	}
	return codes
}

// requireActiveRuntimeEntry 按 workflowCode + entryKey 找到当前生效版本的运行入口。
func (a *App) requireActiveRuntimeEntry(ownerUserID int64, workflowCode, entryKey string) (*db.WorkflowRuntimeEntry, error) {
	state := a.getRuntimeStateByCode(ownerUserID, workflowCode)
	if state == nil || state.ActiveWorkflowDefinitionID == nil {
		return nil, bizErr("子工作流 %s 未激活", workflowCode)
	}
	var entry db.WorkflowRuntimeEntry
	if err := a.DB.Where("workflow_runtime_state_id = ? AND entry_key = ?", state.ID, entryKey).
		First(&entry).Error; err != nil {
		return nil, bizErr("子工作流 %s 不存在入口 %s", workflowCode, entryKey)
	}
	if !entry.IsEnabled {
		return nil, bizErr("子工作流 %s 的入口 %s 已停用", workflowCode, entryKey)
	}
	return &entry, nil
}

// loadWorkflowCallChain 从触发上下文里取回父级传下来的调用链,跑图开始时放进共享状态。
func loadWorkflowCallChain(triggerCtx M) []any {
	payload, _ := triggerCtx["payload"].(map[string]any)
	chain, _ := payload["workflowCallChain"].([]any)
	return chain
}
