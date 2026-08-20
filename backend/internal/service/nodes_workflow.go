// nodes_workflow.go —— 调用子工作流的节点。
//
// 把一段常用流程抽成独立工作流,再在别处用 workflow.call 复用它 —— 这是"编排"能复合起来的关键。
//
// 两个必须防住的坑:
//  1. 递归。A 调 B、B 又调 A 会无限展开。这里在共享状态里带一条调用链,
//     链上出现过的 code 直接拒绝,并限制总深度。
//  2. 等待子流程时占着执行槽。派发器同时只跑 executor_concurrency 个执行,
//     父执行等子执行时自己也占着一个槽;等的父执行一多就可能互相饿死。
//     所以 waitForResult 默认关闭(发射后不管),开启时要清楚这层代价。

package service

import (
	"context"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

// 调用链在共享状态里的键,以及允许的最大嵌套层数。
const (
	workflowCallChainKey = "_workflowCallChain"
	workflowCallMaxDepth = 5
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "workflow.call", Label: "调用子工作流",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"workflowCode": M{"type": "string", "title": "目标工作流"},
				"entryKey":     M{"type": "string", "title": "开始入口", "description": "选择目标工作流中要触发的开始节点"},
				"inputsPath": M{
					"type": "string", "title": "输入取值路径",
					"description": "把共享状态里该路径的对象作为子工作流的 inputs 传过去,留空则不传",
				},
				"waitForResult": M{
					"type": "boolean", "title": "等待子工作流完成", "default": false,
					"description": "开启后本节点会阻塞到子工作流结束。注意它会一直占用一个执行槽,嵌套多层时请调大 executor_concurrency",
				},
				"waitTimeoutMs": M{"type": "integer", "title": "等待超时毫秒", "default": 300000, "minimum": 1000},
				"outputKey":     M{"type": "string", "title": "结果写入变量名", "default": "subWorkflowResult"},
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

	entry, err := ctx.App.requireActiveRuntimeEntry(workflowCode, entryKey)
	if err != nil {
		return nil, err
	}

	inputs := M{}
	if inputsPath := cfgStr(config, "inputsPath", ""); inputsPath != "" {
		if value, ok := ctx.State.get(inputsPath).(map[string]any); ok {
			inputs = value
		}
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
		"status": asString(execution["status"]), "waited": false,
	}

	if isTruthy(config["waitForResult"]) {
		timeout := time.Duration(cfgInt(config, "waitTimeoutMs", 300000)) * time.Millisecond
		final, err := ctx.App.waitForExecution(ctx.Ctx, executionID, timeout)
		if err != nil {
			return nil, err
		}
		output["waited"] = true
		output["status"] = final.Status
		output["errorMessage"] = final.ErrorMessage
		output["result"] = loadJSONObject(final.ResultSnapshotJSON)
		if final.Status != "success" {
			return nil, bizErr("子工作流 %s 执行失败: %s", workflowCode, orString(final.ErrorMessage, final.Status))
		}
	}

	ctx.State.set(cfgStr(config, "outputKey", "subWorkflowResult"), output)
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
func (a *App) requireActiveRuntimeEntry(workflowCode, entryKey string) (*db.WorkflowRuntimeEntry, error) {
	state := a.getRuntimeStateByCode(workflowCode)
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

// waitForExecution 轮询等待某次执行到达终态。
//
// ponytail: 用轮询而不是通知/回调 —— 执行状态本来就落在库里,一秒查一次足够,
// 也不用为此新增一套跨 goroutine 的等待注册表。真要更实时再换。
func (a *App) waitForExecution(ctx context.Context, executionID int64, timeout time.Duration) (*db.WorkflowExecution, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var execution db.WorkflowExecution
		if err := a.DB.WithContext(ctx).First(&execution, executionID).Error; err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, bizErr("子工作流执行记录不存在: %d", executionID)
		}
		if execution.Status == "success" || execution.Status == "failed" {
			return &execution, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, retryableErr(bizErr("等待子工作流执行 %d 超时", executionID))
		case <-ticker.C:
		}
	}
}

// loadWorkflowCallChain 从触发上下文里取回父级传下来的调用链,跑图开始时放进共享状态。
func loadWorkflowCallChain(triggerCtx M) []any {
	payload, _ := triggerCtx["payload"].(map[string]any)
	chain, _ := payload["workflowCallChain"].([]any)
	return chain
}
