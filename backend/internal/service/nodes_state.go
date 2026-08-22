// nodes_state.go —— 操作"共享状态"的基础节点。
//
// 跑图时所有节点共用一张变量表(runState),节点之间靠它传数据。这里的几个节点专门用来
// 摆弄这张表:赋值、往数组里攒结果、过滤数组、打调试日志。
//
// 其中 state.append 补的是一个真空缺:foreach 的循环体每轮跑的是同一段子图、写的是同一份状态,
// 所以"遍历每条数据 → 各自处理 → 把所有结果汇总成一条通知"这种最常见的编排,
// 在没有 append 之前根本画不出来 —— 每轮结果都被下一轮覆盖掉了。

package service

import (
	"log/slog"
	"strings"
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "state.set", Label: "构造数据",
		InputPorts: []workflowNodePortDefinition{nodePort("value", "输入值", false, M{})},
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"name":  M{"type": "string", "title": "字段名称", "default": "value"},
				"value": M{"type": "string", "title": "固定值"},
			},
		},
		Execute: stateSetExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "state.append", Label: "追加到数组",
		InputPorts:   []workflowNodePortDefinition{nodePort("value", "追加值", true, M{})},
		ConfigSchema: M{"type": "object", "properties": M{}},
		Execute:      stateAppendExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "array.filter", Label: "过滤数组",
		InputPorts: []workflowNodePortDefinition{
			nodePort("items", "源数组", true, M{"type": "array", "items": M{}}),
			nodePort("compareTo", "比较值", false, M{}),
		},
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"operator": M{
					"type": "string", "title": "比较运算", "default": "truthy",
					"enum": []string{"eq", "ne", "contains", "gt", "gte", "lt", "lte", "truthy"},
				},
				"value": M{"type": "string", "title": "比较值"},
			},
		},
		Execute: arrayFilterExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "log.message", Label: "记录日志",
		InputPorts: []workflowNodePortDefinition{nodePort("message", "日志内容", false, M{"type": "string"})},
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"message": M{"type": "string", "title": "日志内容(支持 {{路径}} 模板)"},
				"level":   M{"type": "string", "title": "级别", "default": "info", "enum": []string{"info", "warn", "error"}},
			},
		},
		Execute: logMessageExecute,
	})
}

func stateSetExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	value, connected := ctx.Inputs["value"]
	if !connected {
		value = config["value"]
	}
	return &nodeExecResult{Output: M{cfgStr(config, "name", "value"): value}}, nil
}

// stateAppendExecute 往共享状态里某个数组变量追加一个值。
func stateAppendExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	key := "_append:" + asString(ctx.Node["id"])
	value := ctx.Inputs["value"]
	items := ctx.State.appendTo(key, value)
	return &nodeExecResult{Output: M{"items": items, "appended": value, "length": len(items)}}, nil
}

// arrayFilterExecute 按条件过滤数组,结果写进共享状态。
// 比较逻辑复用 compareValues,和条件节点保持同一套语义。
func arrayFilterExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	source, _ := ctx.Inputs["items"].([]any)
	operator := cfgStr(config, "operator", "truthy")
	expected := config["value"]
	if value, ok := ctx.Inputs["compareTo"]; ok {
		expected = value
	}

	kept := make([]any, 0, len(source))
	for _, item := range source {
		if compareValues(item, expected, operator) {
			kept = append(kept, item)
		}
	}
	output := M{
		"items": kept,
		"total": len(source), "kept": len(kept), "dropped": len(source) - len(kept),
	}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

// logMessageExecute 往服务端日志打一条自定义消息,同时写进节点输出。
// 排查流程时很实用:能看到某个节点跑到时共享状态里到底是什么。
func logMessageExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	message := strings.TrimSpace(asString(config["message"]))
	if input := strings.TrimSpace(asString(ctx.Inputs["message"])); input != "" {
		message = input
	}
	if message == "" {
		return nil, bizErr("记录日志节点的日志内容不能为空")
	}
	level := strings.ToLower(cfgStr(config, "level", "info"))
	slogLevel := slog.LevelInfo
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	}
	slog.Log(ctx.Ctx, slogLevel, truncateRunes(message, 2000),
		"execution_id", ctx.Execution.ID,
		"node_id", asString(ctx.Node["id"]),
		"event", "workflow.log_message")
	output := M{"level": level, "message": message}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}
