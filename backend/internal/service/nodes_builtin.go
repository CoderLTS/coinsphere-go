// nodes_builtin.go —— 内置工作流节点处理器。
//
// 每种节点类型一个命名处理函数(func(ctx *nodeExecContext) (*nodeExecResult, error)),
// 在本文件的 init() 里用 registerNode 登记进注册表(见 nodes.go)。
// 加一种新节点 = 写一个处理函数 + 一条 registerNode;既能单独看、也能单独测,不用碰引擎。
//
// 处理器约定:
//   - 读写共享状态一律走 ctx.State 的方法(并发安全),不要直接碰底层 map;
//   - 长耗时节点(http/delay)要遵守 ctx.Ctx:整图被取消或超时时尽快返回;
//   - 出错就 return err,引擎会据此中止整图并把错误往上抛。

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// baseStartProperties 所有 start.* 开始节点共有的配置项。
var baseStartProperties = M{
	"entryKey":      M{"type": "string", "title": "入口标识"},
	"displayName":   M{"type": "string", "title": "入口名称"},
	"inputBindings": M{"type": "object", "title": "默认输入绑定"},
}

// mergeProps 合并两份 schema 属性(base 打底,extra 覆盖同名键),生成新 map,不改动传入的两个。
func mergeProps(base M, extra M) M {
	merged := M{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

// init 在包加载时把所有内置节点登记进注册表。Go 会在 main 之前自动运行每个文件的 init()。
func init() {
	// 四种"开始"节点(手动/定时/事件/Webhook 触发),共用 startNodeExecute,只是配置 schema 不同。
	registerNode(&workflowNodeDefinition{
		TypeCode: "start.manual", Label: "手动开始", Kind: nodeKindStart,
		ConfigSchema: M{"type": "object", "properties": baseStartProperties, "required": []string{"entryKey"}},
		Execute:      startNodeExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "start.schedule", Label: "定时开始", Kind: nodeKindStart,
		ConfigSchema: M{
			"type": "object",
			"properties": mergeProps(baseStartProperties, M{
				"scheduleType":   M{"type": "string", "enum": []string{"cron", "interval", "once"}, "title": "计划类型"},
				"cronExpression": M{"type": "string", "title": "Cron 表达式"},
				"value":          M{"type": "integer", "title": "间隔数值"},
				"unit":           M{"type": "string", "enum": []string{"seconds", "minutes", "hours", "days"}, "title": "间隔单位"},
				"runAt":          M{"type": "string", "title": "执行时间"},
			}),
			"required": []string{"entryKey", "scheduleType"},
		},
		Execute: startNodeExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "start.event", Label: "事件开始", Kind: nodeKindStart,
		ConfigSchema: M{
			"type": "object",
			"properties": mergeProps(baseStartProperties, M{
				"eventType": M{"type": "string", "title": "事件类型"},
				"filters": M{
					"type": "array", "title": "过滤条件",
					"items": M{"type": "object", "properties": M{
						"path":   M{"type": "string", "title": "字段路径"},
						"equals": M{"type": "string", "title": "等于"},
					}},
				},
			}),
			"required": []string{"entryKey", "eventType"},
		},
		Execute: startNodeExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "start.webhook", Label: "Webhook 开始", Kind: nodeKindStart,
		ConfigSchema: M{"type": "object", "properties": baseStartProperties, "required": []string{"entryKey"}},
		Execute:      startNodeExecute,
	})

	registerNode(&workflowNodeDefinition{
		TypeCode: "event.publish", Label: "发布事件",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"eventType":     M{"type": "string", "title": "事件类型"},
				"aggregateType": M{"type": "string", "title": "聚合类型", "default": "workflow_execution"},
				"payloadPath":   M{"type": "string", "title": "事件载荷路径", "default": "taskResult"},
				"metadataPath":  M{"type": "string", "title": "元数据路径", "default": ""},
			},
			"required": []string{"eventType"},
		},
		Execute: eventPublishExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "notify", Label: "发送通知",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"targets": M{"type": "array", "title": "通知目标", "items": M{"type": "object", "properties": M{
					"targetType": M{"type": "string", "enum": []string{"user", "role"}},
					"targetId":   M{"type": "integer"},
					"targetCode": M{"type": "string"},
				}}},
				"channelTypes":    M{"type": "array", "title": "通知渠道", "items": M{"type": "string", "enum": []string{"in_app", "dingtalk_webhook", "qq_bot", "smtp_email"}}},
				"titleTemplate":   M{"type": "string", "title": "标题模板"},
				"contentTemplate": M{"type": "string", "title": "内容模板"},
				"messageFormat":   M{"type": "string", "title": "消息格式", "default": "markdown"},
			},
			"required": []string{"targets", "channelTypes", "titleTemplate", "contentTemplate"},
		},
		Execute: notifyExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "http.request", Label: "HTTP 请求",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"url":         M{"type": "string", "title": "请求地址"},
				"method":      M{"type": "string", "title": "请求方法", "default": "POST", "enum": []string{"GET", "POST", "PUT"}},
				"payloadPath": M{"type": "string", "title": "请求体路径", "default": "taskResult"},
				"headersJson": M{"type": "string", "title": "请求头 JSON", "default": "{}"},
				"timeoutMs":   M{"type": "integer", "title": "超时毫秒", "default": 10000},
			},
			"required": []string{"url"},
		},
		Execute: httpRequestExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "condition.branch", Label: "条件判断",
		Kind:     nodeKindBranch,
		Branches: []string{"true", "false"},
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"path":     M{"type": "string", "title": "字段路径"},
				"operator": M{"type": "string", "title": "比较运算", "default": "eq", "enum": []string{"eq", "ne", "contains", "gt", "gte", "lt", "lte", "truthy"}},
				"value":    M{"type": "string", "title": "比较值", "default": ""},
				"valuePath": M{
					"type": "string", "title": "比较值路径",
					"description": "填了就取共享状态里该路径的值参与比较,优先于固定的比较值",
				},
				"logic": M{
					"type": "string", "title": "多条件组合方式", "default": "and",
					"enum": []string{"and", "or"},
				},
				"conditions": M{
					"type": "array", "title": "多条件(留空则用上面的单条件)",
					"items": M{"type": "object", "properties": M{
						"path":      M{"type": "string", "title": "字段路径"},
						"operator":  M{"type": "string", "title": "比较运算", "enum": []string{"eq", "ne", "contains", "gt", "gte", "lt", "lte", "truthy"}},
						"value":     M{"type": "string", "title": "比较值"},
						"valuePath": M{"type": "string", "title": "比较值路径"},
					}},
				},
			},
			// path/operator 不列为必填:用多条件(conditions)时顶层这两项是空的。
			// "至少配了一种条件"改在执行期校验,见 conditionBranchExecute。
		},
		Execute: conditionBranchExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "foreach", Label: "遍历", Kind: nodeKindLoop,
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"itemsPath": M{"type": "string", "title": "数组路径"},
				"itemKey":   M{"type": "string", "title": "元素变量名", "default": "currentItem"},
				"indexKey":  M{"type": "string", "title": "索引变量名", "default": "currentIndex"},
			},
			"required": []string{"itemsPath"},
		},
		Execute: foreachExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "delay.wait", Label: "等待",
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"durationMs": M{"type": "integer", "title": "等待毫秒", "default": 1000, "minimum": 0, "maximum": 600000},
			},
		},
		Execute: delayWaitExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "end", Label: "结束", Kind: nodeKindTerminal,
		ConfigSchema: M{"type": "object", "properties": M{}},
		Execute:      endExecute,
	})
}

// startNodeExecute 所有 start.* 开始节点共用:只是把触发信息原样输出。
func startNodeExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	return &nodeExecResult{Output: M{
		"started":     true,
		"triggerType": ctx.TriggerCtx["triggerType"],
		"entryKey":    asString(config["entryKey"]),
	}}, nil
}

// eventPublishExecute 从共享状态取载荷/元数据,发布一条领域事件。
func eventPublishExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	payloadPath := cfgStr(config, "payloadPath", "taskResult")
	payload, _ := ctx.State.get(payloadPath).(map[string]any)
	if payload == nil {
		payload = M{"value": ctx.State.get(payloadPath)}
	}
	metadata := M{}
	if metadataPath := cfgStr(config, "metadataPath", ""); metadataPath != "" {
		if candidate, ok := ctx.State.get(metadataPath).(map[string]any); ok {
			metadata = candidate
		}
	}
	aggregateType := cfgStr(config, "aggregateType", "workflow_execution")
	eventType := cfgStr(config, "eventType", "")
	outboxID, err := ctx.PublishEvent(eventType, aggregateType, payload, metadata)
	if err != nil {
		return nil, err
	}
	return &nodeExecResult{Output: M{"outboxId": outboxID, "eventType": eventType}}, nil
}

// notifyExecute 发送通知(站内信/钉钉/邮件)。渲染模板要读整份共享状态,这里传一份快照进去,避免并发读写冲突。
func notifyExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	var outboxEventID *int64
	if raw := ctx.TriggerCtx["triggerOutboxId"]; raw != nil {
		if id := asInt64(raw); id > 0 {
			outboxEventID = &id
		}
	}
	result, err := ctx.App.dispatchNotifyNode(ctx.Ctx, ctx.Execution, ctx.NodeLog, outboxEventID, config, ctx.State.snapshot())
	if err != nil {
		return nil, err
	}
	setNodeOutput(ctx, result)
	return &nodeExecResult{Output: result}, nil
}

// httpRequestExecute 发起一次 HTTP 请求。用执行级 ctx 派生带超时的子 ctx:整图取消或超时都会中断本请求。
// 失败会明确标注可否重试:网络层错误与 429/5xx 交给重试机制,4xx 属于请求本身有问题,重试也是白搭。
func httpRequestExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	payload := ctx.State.get(cfgStr(config, "payloadPath", "taskResult"))
	headers := M{}
	if headersText := strings.TrimSpace(asString(config["headersJson"])); headersText != "" {
		var parsed M
		if err := json.Unmarshal([]byte(headersText), &parsed); err != nil {
			return nil, bizErr("Node JSON config is invalid")
		}
		headers = parsed
	}
	timeout := time.Duration(cfgInt(config, "timeoutMs", 10000)) * time.Millisecond
	if timeout < time.Second {
		timeout = time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx.Ctx, timeout)
	defer cancel()

	body, _ := json.Marshal(payload)
	method := strings.ToUpper(cfgStr(config, "method", "POST"))
	request, err := http.NewRequestWithContext(requestCtx, method, cfgStr(config, "url", ""), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, asString(value))
	}
	client, err := newSafeHTTPClient(ctx.App.Cfg.Workflow.HTTPAllowedHosts)
	if err != nil {
		return nil, permanentErr(err)
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, errUnsafeHTTPRequest) {
			return nil, permanentErr(err)
		}
		// 连接失败 / 超时 / 被取消都属于基础设施问题,标成可重试。
		return nil, retryableErr(err)
	}
	// defer 登记"函数返回前一定执行"的收尾动作,这里确保响应体最终被关闭(不管后面从哪条路 return)。
	defer response.Body.Close()
	// io.LimitReader 限制最多读 4MB,避免超大响应把内存撑爆。
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		statusErr := bizErr("HTTP request failed with status %d: %s", response.StatusCode, truncateRunes(string(raw), 500))
		// 429(限流)与 5xx(服务端故障)过一会儿可能就好了;其余 4xx 是请求本身的问题,重试无意义。
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return nil, retryableErr(statusErr)
		}
		return nil, permanentErr(statusErr)
	}
	var responsePayload any
	if err := json.Unmarshal(raw, &responsePayload); err != nil {
		responsePayload = string(raw)
	}
	result := M{"statusCode": response.StatusCode, "payload": responsePayload}
	setNodeOutput(ctx, result)
	return &nodeExecResult{Output: result}, nil
}

// conditionBranchExecute 条件判断,选出 true/false 分支;引擎只会点亮 branchKey 与所选分支相符的出边。
//
// 支持两种写法:
//   - 单条件:直接用 path / operator / value(或 valuePath);
//   - 多条件:填 conditions 数组,再用 logic(and / or)组合,此时忽略顶层单条件。
func conditionBranchExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	clauses, _ := config["conditions"].([]any)
	matched := false
	if len(clauses) == 0 {
		// 单条件模式下 path 是必须的;两种模式都没配就是节点没配好,直接报错,
		// 而不是默默走 false 分支让人以为"条件不成立"。
		if cfgStr(config, "path", "") == "" {
			return nil, bizErr("条件节点需要填写字段路径,或改用多条件配置")
		}
		matched = evalCondition(ctx, config)
	} else if strings.EqualFold(cfgStr(config, "logic", "and"), "or") {
		// or:任一条成立即为真。
		for _, clauseAny := range clauses {
			if clause, ok := clauseAny.(map[string]any); ok && evalCondition(ctx, clause) {
				matched = true
				break
			}
		}
	} else {
		// and(默认):全部成立才为真。
		matched = true
		for _, clauseAny := range clauses {
			clause, ok := clauseAny.(map[string]any)
			if !ok || !evalCondition(ctx, clause) {
				matched = false
				break
			}
		}
	}
	branch := "false"
	if matched {
		branch = "true"
	}
	// SelectedBranch 用 & 取地址填进 *string 字段;引擎看到它非 nil,就只沿匹配分支的边继续走。
	return &nodeExecResult{Output: M{"matched": matched}, SelectedBranch: &branch}, nil
}

// evalCondition 求值一条比较子句:左值取自共享状态的 path;右值优先取 valuePath 指向的状态值,否则用固定的 value。
func evalCondition(ctx *nodeExecContext, clause M) bool {
	actual := ctx.State.get(asString(clause["path"]))
	expected := clause["value"]
	if valuePath := cfgStr(clause, "valuePath", ""); valuePath != "" {
		expected = ctx.State.get(valuePath)
	}
	return compareValues(actual, expected, cfgStr(clause, "operator", "eq"))
}

// foreachExecute 把要遍历的数组放进 ForeachItems 返回;引擎收到后会对每个元素跑一遍循环体子图。
// 元素/索引写进共享状态时用的变量名一并返回(ItemKey/IndexKey),交给引擎按元素设置,循环体内的节点即可引用。
func foreachExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	items, _ := ctx.State.get(asString(config["itemsPath"])).([]any)
	if items == nil {
		items = []any{}
	}
	return &nodeExecResult{
		Output:       M{"count": len(items)},
		ForeachItems: items,
		ItemKey:      cfgStr(config, "itemKey", "currentItem"),
		IndexKey:     cfgStr(config, "indexKey", "currentIndex"),
	}, nil
}

// delayWaitExecute 等待若干毫秒;等待期间若整图被取消(ctx.Done),立刻返回取消错误,不再空等。
func delayWaitExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	durationMs := asInt64(config["durationMs"])
	if durationMs > 0 {
		timer := time.NewTimer(time.Duration(durationMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Ctx.Done():
			return nil, ctx.Ctx.Err()
		}
	}
	return &nodeExecResult{Output: M{"durationMs": durationMs}}, nil
}

// endExecute 结束节点:自身无出边,是图的一个汇点。真 DAG 下它不再"短路终止"其它并行分支,
// 整图会等所有分支各自跑到汇点后自然结束。
func endExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	return &nodeExecResult{Output: M{"ended": true}}, nil
}
