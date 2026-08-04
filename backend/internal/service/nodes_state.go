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
		TypeCode: "state.set", Label: "设置变量",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"assignments": M{
					"type": "array", "title": "赋值列表",
					"items": M{"type": "object", "properties": M{
						"key":   M{"type": "string", "title": "变量名"},
						"value": M{"type": "string", "title": "值(支持 {{路径}} 模板)"},
						"valuePath": M{
							"type": "string", "title": "值路径",
							"description": "填了就直接把共享状态里该路径的值(可以是对象/数组)搬过来,不经过文本转换",
						},
					}},
				},
			},
			"required": []string{"assignments"},
		},
		Execute: stateSetExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "state.append", Label: "追加到数组",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"key":       M{"type": "string", "title": "目标数组变量名"},
				"value":     M{"type": "string", "title": "追加的值(支持 {{路径}} 模板)"},
				"valuePath": M{"type": "string", "title": "值路径", "description": "填了就追加该路径的原始值(对象/数组也行)"},
			},
			"required": []string{"key"},
		},
		Execute: stateAppendExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "array.filter", Label: "过滤数组",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"itemsPath": M{"type": "string", "title": "源数组路径"},
				"outputKey": M{"type": "string", "title": "结果写入变量名", "default": "filteredItems"},
				"itemPath": M{
					"type": "string", "title": "元素内字段路径",
					"description": "留空表示直接拿元素本身比较;填了就取元素里这个路径的值,例如 insertedCount",
				},
				"operator": M{
					"type": "string", "title": "比较运算", "default": "truthy",
					"enum": []string{"eq", "ne", "contains", "gt", "gte", "lt", "lte", "truthy"},
				},
				"value": M{"type": "string", "title": "比较值"},
			},
			"required": []string{"itemsPath", "operator"},
		},
		Execute: arrayFilterExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "log.message", Label: "记录日志",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"message": M{"type": "string", "title": "日志内容(支持 {{路径}} 模板)"},
				"level":   M{"type": "string", "title": "级别", "default": "info", "enum": []string{"info", "warn", "error"}},
			},
			"required": []string{"message"},
		},
		Execute: logMessageExecute,
	})
}

// resolveConfigValue 取一个"值":valuePath 非空就从共享状态取原始值(保留对象/数组结构),
// 否则用已经渲染过模板的 value 字符串。
//
// 两条路都留着是有必要的:模板渲染出来的一定是字符串,想把上游的整个对象搬过去就得走 valuePath。
func resolveConfigValue(ctx *nodeExecContext, config M) any {
	if path := cfgStr(config, "valuePath", ""); path != "" {
		return ctx.State.get(path)
	}
	return config["value"]
}

// stateSetExecute 按配置逐条给共享状态赋值。
func stateSetExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	assignments, _ := config["assignments"].([]any)
	if len(assignments) == 0 {
		return nil, bizErr("设置变量节点至少需要一条赋值")
	}
	applied := M{}
	for _, itemAny := range assignments {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		key := cfgStr(item, "key", "")
		if key == "" {
			return nil, bizErr("设置变量节点的变量名不能为空")
		}
		value := resolveConfigValue(ctx, item)
		ctx.State.set(key, value)
		applied[key] = value
	}
	if len(applied) == 0 {
		return nil, bizErr("设置变量节点没有任何有效赋值")
	}
	output := M{"assigned": applied, "count": len(applied)}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

// stateAppendExecute 往共享状态里某个数组变量追加一个值。
func stateAppendExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	key := cfgStr(config, "key", "")
	if key == "" {
		return nil, bizErr("追加到数组节点缺少目标变量名")
	}
	value := resolveConfigValue(ctx, config)
	items := ctx.State.appendTo(key, value)
	output := M{"key": key, "appended": value, "length": len(items)}
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output}, nil
}

// arrayFilterExecute 按条件过滤数组,结果写进共享状态。
// 比较逻辑复用 compareValues,和条件节点保持同一套语义。
func arrayFilterExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	itemsPath := cfgStr(config, "itemsPath", "")
	if itemsPath == "" {
		return nil, bizErr("过滤数组节点缺少源数组路径")
	}
	source, _ := ctx.State.get(itemsPath).([]any)
	operator := cfgStr(config, "operator", "truthy")
	itemPath := cfgStr(config, "itemPath", "")
	expected := config["value"]

	kept := make([]any, 0, len(source))
	for _, item := range source {
		actual := item
		if itemPath != "" {
			actual = readPath(item, itemPath)
		}
		if compareValues(actual, expected, operator) {
			kept = append(kept, item)
		}
	}
	outputKey := cfgStr(config, "outputKey", "filteredItems")
	ctx.State.set(outputKey, kept)
	output := M{
		"key": outputKey, "items": kept,
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
