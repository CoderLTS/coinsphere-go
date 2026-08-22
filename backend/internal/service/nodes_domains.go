package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/perm"

	"github.com/google/uuid"
)

func init() {
	registerStrategyNodes()
	registerContentNodes()
	registerTradingNodes()
	registerConfigNodes()
	registerAdminNodes()
}

func domainPayloadPorts(schema M, required bool) []workflowNodePortDefinition {
	return []workflowNodePortDefinition{nodePort("payload", "结构化参数", required, schema)}
}

func domainOutputPorts() []workflowNodePortDefinition {
	return []workflowNodePortDefinition{nodePort("result", "操作结果", false, M{"type": "object"})}
}

func strategyDraftPayloadSchema() M {
	return M{"type": "object", "properties": M{
		"name":            M{"type": "string", "title": "策略名称"},
		"sourceCode":      M{"type": "string", "title": "策略代码", "format": "code", "language": "python"},
		"lookbackBars":    M{"type": "integer", "title": "回看 K 线数", "minimum": 1},
		"parameterSchema": M{"type": "object", "title": "策略参数", "format": "field-schema"},
	}, "required": []string{"name", "sourceCode", "lookbackBars", "parameterSchema"}}
}

func backtestPayloadSchema() M {
	decimal := func(title string) M { return M{"type": "string", "title": title, "format": "decimal"} }
	return M{"type": "object", "properties": M{
		"strategyVersionId": M{"type": "string", "title": "策略版本", "resource": "strategy-version"},
		"instrumentId":      M{"type": "string", "title": "交易品种", "resource": "market-instrument"},
		"interval":          M{"type": "string", "title": "K 线周期", "enum": []string{"1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d", "1w", "1M"}},
		"parameters":        M{"type": "object", "title": "策略参数", "format": "key-value"},
		"startTime":         M{"type": "string", "title": "开始时间", "format": "date-time"},
		"endTime":           M{"type": "string", "title": "结束时间", "format": "date-time"},
		"allocationUsdt":    decimal("分配资金 USDT"), "initialEquity": decimal("初始权益"),
		"feeRate": decimal("手续费率"), "slippageRate": decimal("滑点率"),
		"fundingRates":  M{"type": "array", "title": "资金费率", "items": decimal("资金费率")},
		"stopLossRatio": decimal("止损比例"), "maintenanceMarginRatio": decimal("维持保证金比例"),
	}, "required": []string{"strategyVersionId", "instrumentId", "interval", "parameters", "startTime", "endTime", "allocationUsdt", "initialEquity", "feeRate", "slippageRate"}}
}

func newsPayloadSchema() M {
	return M{"type": "object", "properties": M{
		"title": M{"type": "string", "title": "标题"}, "content": M{"type": "string", "title": "正文", "format": "multiline"},
		"sourceUrl":   M{"type": "string", "title": "来源地址", "format": "uri"},
		"originalUrl": M{"type": "string", "title": "原文地址", "format": "uri"},
		"imageUrl":    M{"type": "string", "title": "图片地址", "format": "uri"},
		"publishedAt": M{"type": "string", "title": "发布时间", "format": "date-time"},
	}, "required": []string{"title", "content"}}
}

func tradingRiskPayloadSchema() M { return tradingRiskActionSchema() }

func tradingAccountCreatePayloadSchema() M {
	decimal := func(title string) M { return M{"type": "string", "title": title, "format": "decimal"} }
	return M{"type": "object", "properties": M{
		"name":           M{"type": "string", "title": "账户名称"},
		"market":         M{"type": "string", "title": "市场", "enum": []string{"spot", "usd_m"}},
		"environment":    M{"type": "string", "title": "环境", "enum": []string{"paper", "testnet", "live"}},
		"initialBalance": decimal("初始余额"), "paperFeeRate": decimal("Paper 手续费率"),
		"risk": tradingRiskPayloadSchema(),
	}, "required": []string{"name", "market", "environment", "initialBalance", "paperFeeRate", "risk"}}
}

func tradingAccountPayloadSchema() M {
	schema := tradingAccountCreatePayloadSchema()
	schema["required"] = []string{"name"}
	return schema
}

func assistantPayloadSchema() M {
	return M{"type": "object", "properties": M{
		"code": M{"type": "string", "title": "智能体编码"}, "displayName": M{"type": "string", "title": "显示名称"},
		"avatar": M{"type": "string", "title": "头像"}, "description": M{"type": "string", "title": "说明", "format": "multiline"},
		"systemPrompt":   M{"type": "string", "title": "系统提示词", "format": "multiline"},
		"welcomeMessage": M{"type": "string", "title": "欢迎语", "format": "multiline"},
		"starterPrompts": M{"type": "array", "title": "推荐问题", "items": M{"type": "string"}},
		"dataSourceType": M{"type": "string", "title": "数据源类型"}, "isEnabled": M{"type": "boolean", "title": "启用"},
		"sort": M{"type": "integer", "title": "排序"},
	}, "required": []string{"code", "displayName", "systemPrompt"}}
}

func registerStrategyNodes() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "strategy.manage", Label: "管理策略", RequiredPermission: perm.TradingOverviewView,
		InputPorts: domainPayloadPorts(strategyDraftPayloadSchema(), false), OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action":     M{"type": "string", "title": "操作", "enum": []string{"create", "update", "archive"}},
			"strategyId": M{"type": "string", "title": "策略", "resource": "strategy-draft"},
		}, "required": []string{"action"}}, Execute: strategyManageExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "strategy.publish", Label: "发布策略", RequiredPermission: perm.TradingOverviewView,
		ExecutionMode: nodeExecutionWorkerJob, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"strategyId": M{"type": "string", "title": "策略", "resource": "strategy-draft"},
		}, "required": []string{"strategyId"}}, Execute: strategyPublishExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "strategy.backtest", Label: "运行回测", RequiredPermission: perm.TradingOverviewView,
		ExecutionMode: nodeExecutionWorkerJob, InputPorts: domainPayloadPorts(backtestPayloadSchema(), true), OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{}}, Execute: strategyBacktestExecute,
	})
}

func strategyManageExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action := cfgStr(config, "action", "")
	strategyID := cfgStr(config, "strategyId", "")
	if action == "archive" {
		return workflowHumanAction(ctx, "strategy.archive", "strategy", strategyID, M{"action": action})
	}
	payload, err := workflowNodePayload[StrategyDraftPayload](ctx)
	if err != nil {
		return nil, err
	}
	var result any
	switch action {
	case "create":
		result, err = ctx.App.CreateStrategyDraft(ctx.Ctx, ctx.Execution.OwnerUserID, payload)
	case "update":
		result, err = ctx.App.UpdateStrategyDraft(ctx.Ctx, ctx.Execution.OwnerUserID, strategyID, payload)
	default:
		return nil, bizErr("Unsupported strategy.manage action: %s", action)
	}
	return workflowNodeResult(result, err)
}

func strategyPublishExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	strategyID := cfgStr(nodeConfig(ctx), "strategyId", "")
	version, err := ctx.App.PublishStrategy(ctx.Ctx, ctx.Execution.OwnerUserID, strategyID, workflowNodeIdempotencyKey(ctx))
	if err != nil {
		return nil, err
	}
	var row db.StrategyVersion
	if err := ctx.App.dbWithContext(ctx.Ctx).Where("id = ? AND published_by_user_id = ?", version.ID, ctx.Execution.OwnerUserID).Take(&row).Error; err != nil {
		return nil, err
	}
	output := workflowResultMap(version)
	return &nodeExecResult{Output: output, Wait: &workflowWaitRequest{
		Kind: "worker_job", TargetType: "strategy_version", TargetID: version.ID,
		Request: M{"workerTaskId": row.WorkerTaskID},
	}}, nil
}

func strategyBacktestExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	payload, err := workflowNodePayload[BacktestCreatePayload](ctx)
	if err != nil {
		return nil, err
	}
	backtest, err := ctx.App.CreateBacktest(ctx.Ctx, ctx.Execution.OwnerUserID, workflowNodeIdempotencyKey(ctx), payload)
	if err != nil {
		return nil, err
	}
	var row db.Backtest
	if err := ctx.App.dbWithContext(ctx.Ctx).Where("id = ? AND owner_user_id = ?", backtest.ID, ctx.Execution.OwnerUserID).Take(&row).Error; err != nil {
		return nil, err
	}
	return &nodeExecResult{Output: workflowResultMap(backtest), Wait: &workflowWaitRequest{
		Kind: "worker_job", TargetType: "backtest", TargetID: backtest.ID,
		Request: M{"workerTaskId": row.WorkerTaskID},
	}}, nil
}

func registerContentNodes() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "news.manage", Label: "管理新闻", RequiredPermission: perm.DataNewsView,
		PermissionConfigKey: "action", PermissionByValue: map[string]string{
			"create": perm.DataNewsCreate, "update": perm.DataNewsUpdate, "delete": perm.DataNewsDelete,
		},
		InputPorts: domainPayloadPorts(newsPayloadSchema(), false), OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action": M{"type": "string", "title": "操作", "enum": []string{"create", "update", "delete"}},
			"newsId": M{"type": "integer", "title": "新闻", "resource": "news"},
		}, "required": []string{"action"}}, Execute: newsManageExecute,
	})
}

func newsManageExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action := cfgStr(config, "action", "")
	newsID := asInt64(config["newsId"])
	if action == "delete" {
		return workflowHumanAction(ctx, "news.delete", "news", int64Text(newsID), M{"action": action})
	}
	payload, err := workflowNodePayload[NewsUpsertPayload](ctx)
	if err != nil {
		return nil, err
	}
	var result any
	switch action {
	case "create":
		result, err = ctx.App.CreateNews(payload)
	case "update":
		result, err = ctx.App.UpdateNews(newsID, payload)
	default:
		return nil, bizErr("Unsupported news.manage action: %s", action)
	}
	return workflowNodeResult(result, err)
}

func registerTradingNodes() {
	trading := perm.TradingOverviewView
	registerNode(&workflowNodeDefinition{
		TypeCode: "trading.account", Label: "管理交易账户", RequiredPermission: trading,
		InputPorts: domainPayloadPorts(tradingAccountPayloadSchema(), false), OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action":    M{"type": "string", "title": "操作", "enum": []string{"create", "update", "resume", "archive"}},
			"accountId": M{"type": "string", "title": "账户", "resource": "trading-account"},
		}, "required": []string{"action"}}, Execute: tradingAccountExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "trading.risk", Label: "调整风险边界", RequiredPermission: trading,
		SecurityPolicy: nodeSecurityHumanReauth, InputPorts: domainPayloadPorts(tradingRiskPayloadSchema(), true), OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"accountId": M{"type": "string", "title": "账户", "resource": "trading-account"},
		}, "required": []string{"accountId"}}, Execute: tradingRiskExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "trading.credentials", Label: "管理交易凭据", RequiredPermission: trading,
		ExecutionMode: nodeExecutionHumanAction, SecurityPolicy: nodeSecurityHumanReauth,
		OutputPorts: domainOutputPorts(), ConfigSchema: M{"type": "object", "properties": M{
			"action":    M{"type": "string", "title": "操作", "enum": []string{"save", "revoke"}},
			"accountId": M{"type": "string", "title": "账户", "resource": "trading-account"},
		}, "required": []string{"action", "accountId"}}, Execute: tradingCredentialsExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "trading.automation", Label: "交易自动化", RequiredPermission: trading,
		SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"enabled":   M{"type": "boolean", "title": "启用"},
			"accountId": M{"type": "string", "title": "账户", "resource": "trading-account"},
		}, "required": []string{"enabled", "accountId"}}, Execute: tradingAutomationExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "trading.authorization", Label: "交易授权", RequiredPermission: trading,
		SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"authorized": M{"type": "boolean", "title": "授予授权"},
			"accountId":  M{"type": "string", "title": "账户", "resource": "trading-account"},
		}, "required": []string{"authorized", "accountId"}}, Execute: tradingAuthorizationExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "trading.emergency", Label: "交易急停", RequiredPermission: trading,
		SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action": M{"type": "string", "title": "操作", "enum": []string{"stop", "release"}},
			"reason": M{"type": "string", "title": "急停原因"},
		}, "required": []string{"action"}}, Execute: tradingEmergencyExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "strategy.signal.review", Label: "审核策略信号", RequiredPermission: trading,
		SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"decision": M{"type": "string", "title": "决策", "enum": []string{"approved", "rejected"}},
			"signalId": M{"type": "string", "title": "策略信号", "resource": "strategy-signal"},
		}, "required": []string{"decision", "signalId"}}, Execute: strategySignalReviewExecute,
	})
}

func tradingAccountExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action, accountID := cfgStr(config, "action", ""), cfgStr(config, "accountId", "")
	switch action {
	case "resume":
		return workflowHumanAction(ctx, "trading.account.resume", "trading_account", accountID, M{"action": action})
	case "archive":
		return workflowHumanAction(ctx, "trading.account.archive", "trading_account", accountID, M{"action": action})
	}
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	var result any
	switch action {
	case "create":
		payload, decodeErr := workflowNodePayload[TradingAccountCreatePayload](ctx)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result, err = ctx.App.CreateTradingAccount(ctx.Ctx, principal.User.ID, payload, workflowNodeIdempotencyKey(ctx))
	case "update":
		payload, decodeErr := workflowNodePayload[TradingAccountUpdatePayload](ctx)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result, err = ctx.App.UpdateTradingAccount(ctx.Ctx, principal.User.ID, accountID, payload)
	default:
		return nil, bizErr("Unsupported trading.account action: %s", action)
	}
	return workflowNodeResult(result, err)
}

func tradingRiskExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	payload, err := workflowNodePayload[TradingRiskPayload](ctx)
	if err != nil {
		return nil, err
	}
	accountID := cfgStr(nodeConfig(ctx), "accountId", "")
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	expands, err := ctx.App.workflowTradingRiskExpands(ctx.Ctx, principal.User.ID, accountID, payload)
	if err != nil {
		return nil, err
	}
	if expands {
		return workflowHumanAction(ctx, "trading.risk.update", "trading_account", accountID, M{"proposed": workflowJSONValue(payload)})
	}
	result, err := ctx.App.UpdateTradingRisk(ctx.Ctx, principal, accountID, payload, workflowNodeIdempotencyKey(ctx), "")
	return workflowNodeResult(result, err)
}

func (a *App) workflowTradingRiskExpands(ctx context.Context, ownerUserID int64, rawID string, payload TradingRiskPayload) (bool, error) {
	accountID, err := requiredTradingUUID(rawID, "accountId")
	if err != nil {
		return false, err
	}
	var account db.TradingAccount
	if err := a.dbWithContext(ctx).Where("id = ? AND owner_user_id = ?", accountID, ownerUserID).Take(&account).Error; err != nil {
		return false, tradingAccountLookupError(err)
	}
	risk, err := validateTradingRisk(payload, account.Market)
	if err != nil {
		return false, err
	}
	var existing []uuid.UUID
	if err := a.dbWithContext(ctx).Model(&db.TradingAccountInstrument{}).Where("account_id = ?", accountID).
		Pluck("instrument_id", &existing).Error; err != nil {
		return false, err
	}
	// Keep the classification in the domain helper; the update itself repeats it under lock and fails closed on races.
	return tradingRiskExpands(account, existing, risk), nil
}

func tradingCredentialsExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action := cfgStr(config, "action", "")
	actionType := "trading.credentials." + action
	if action != "save" && action != "revoke" {
		return nil, bizErr("Unsupported trading.credentials action: %s", action)
	}
	return workflowHumanAction(ctx, actionType, "trading_account", cfgStr(config, "accountId", ""), M{"action": action})
}

func tradingAutomationExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	accountID := cfgStr(config, "accountId", "")
	if isTruthy(config["enabled"]) {
		return workflowHumanAction(ctx, "trading.automation.enable", "trading_account", accountID, M{"enabled": true})
	}
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	result, err := ctx.App.SetTradingAutomation(ctx.Ctx, principal, accountID, false, workflowNodeIdempotencyKey(ctx), "")
	return workflowNodeResult(result, err)
}

func tradingAuthorizationExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	accountID := cfgStr(config, "accountId", "")
	if isTruthy(config["authorized"]) {
		return workflowHumanAction(ctx, "trading.authorization.grant", "trading_account", accountID, M{"authorized": true})
	}
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	result, err := ctx.App.SetTradingAuthorization(ctx.Ctx, principal, accountID, false, workflowNodeIdempotencyKey(ctx), "")
	return workflowNodeResult(result, err)
}

func tradingEmergencyExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	if cfgStr(config, "action", "") == "release" {
		return workflowHumanAction(ctx, "trading.emergency.release", "trading_control", "1", M{"action": "release"})
	}
	if cfgStr(config, "action", "") != "stop" {
		return nil, bizErr("Unsupported trading.emergency action")
	}
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	result, err := ctx.App.ActivateTradingEmergencyStop(ctx.Ctx, principal, cfgStr(config, "reason", "Workflow emergency stop"), workflowNodeIdempotencyKey(ctx))
	return workflowNodeResult(result, err)
}

func strategySignalReviewExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	decision, signalID := cfgStr(config, "decision", ""), cfgStr(config, "signalId", "")
	if decision != "approved" && decision != "rejected" {
		return nil, bizErr("Unsupported signal decision: %s", decision)
	}
	actionType := "strategy.signal.reject"
	if decision == "approved" {
		actionType = "strategy.signal.approve"
	}
	return workflowHumanAction(ctx, actionType, "strategy_signal", signalID, M{"decision": decision})
}

func registerConfigNodes() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "config.ai-model", Label: "配置 AI 模型", RequiredPermission: perm.ConfigAiModelsView,
		PermissionConfigKey: "action", PermissionByValue: map[string]string{
			"create": perm.ConfigAiModelsCreate, "update": perm.ConfigAiModelsUpdate, "delete": perm.ConfigAiModelsDelete,
			"enable": perm.ConfigAiModelsUpdate, "disable": perm.ConfigAiModelsUpdate,
		},
		SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action":   M{"type": "string", "title": "操作", "enum": []string{"create", "update", "delete", "enable", "disable"}},
			"configId": M{"type": "integer", "title": "模型配置", "resource": "ai-model"},
		}, "required": []string{"action"}}, Execute: aiModelConfigExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "config.assistant", Label: "配置智能体", RequiredPermission: perm.ConfigAssistantAgentsView,
		PermissionConfigKey: "action", PermissionByValue: map[string]string{
			"create": perm.ConfigAssistantAgentsCreate, "update": perm.ConfigAssistantAgentsUpdate,
			"delete": perm.ConfigAssistantAgentsDelete, "enable": perm.ConfigAssistantAgentsUpdate, "disable": perm.ConfigAssistantAgentsUpdate,
		},
		InputPorts: domainPayloadPorts(assistantPayloadSchema(), false), OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action":  M{"type": "string", "title": "操作", "enum": []string{"create", "update", "delete", "enable", "disable"}},
			"agentId": M{"type": "integer", "title": "智能体", "resource": "assistant"},
		}, "required": []string{"action"}}, Execute: assistantConfigExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "config.notification-channel", Label: "配置通知渠道", RequiredPermission: perm.ConfigNotificationChannelsView,
		PermissionConfigKey: "action", PermissionByValue: map[string]string{
			"create": perm.ConfigNotificationChannelsCreate, "update": perm.ConfigNotificationChannelsUpdate,
			"delete": perm.ConfigNotificationChannelsDelete, "enable": perm.ConfigNotificationChannelsUpdate,
			"disable": perm.ConfigNotificationChannelsUpdate, "test": perm.ConfigNotificationChannelsTest,
		},
		SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action":    M{"type": "string", "title": "操作", "enum": []string{"create", "update", "delete", "enable", "disable", "test"}},
			"channelId": M{"type": "integer", "title": "通知渠道", "resource": "notification-channel"},
		}, "required": []string{"action"}}, Execute: notificationChannelConfigExecute,
	})
}

func aiModelConfigExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action, target := cfgStr(config, "action", ""), int64Text(asInt64(config["configId"]))
	if action == "create" || action == "update" || action == "delete" {
		return workflowHumanAction(ctx, "config.ai-model."+action, "ai_model_config", target, M{"action": action})
	}
	if action != "enable" && action != "disable" {
		return nil, bizErr("Unsupported config.ai-model action: %s", action)
	}
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	err = ctx.App.SetAiModelEnabled(asInt64(config["configId"]), action == "enable", principal)
	return workflowNodeResult(M{"updated": err == nil, "enabled": action == "enable"}, err)
}

func assistantConfigExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action, agentID := cfgStr(config, "action", ""), asInt64(config["agentId"])
	if action == "delete" {
		return workflowHumanAction(ctx, "config.assistant.delete", "assistant_agent", int64Text(agentID), M{"action": action})
	}
	var result any
	var err error
	switch action {
	case "create":
		var payload AssistantAgentUpsertPayload
		payload, err = workflowNodePayload[AssistantAgentUpsertPayload](ctx)
		if err == nil {
			result, err = ctx.App.CreateAssistantAgent(payload)
		}
	case "update":
		var payload AssistantAgentUpsertPayload
		payload, err = workflowNodePayload[AssistantAgentUpsertPayload](ctx)
		if err == nil {
			result, err = ctx.App.UpdateAssistantAgent(agentID, payload)
		}
	case "enable", "disable":
		err = ctx.App.SetAssistantAgentEnabled(agentID, action == "enable")
		result = M{"updated": err == nil, "enabled": action == "enable"}
	default:
		return nil, bizErr("Unsupported config.assistant action: %s", action)
	}
	return workflowNodeResult(result, err)
}

func notificationChannelConfigExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action, channelID := cfgStr(config, "action", ""), asInt64(config["channelId"])
	if action == "create" || action == "update" || action == "delete" {
		return workflowHumanAction(ctx, "config.notification-channel."+action, "notification_channel", int64Text(channelID), M{"action": action})
	}
	principal, err := ctx.App.buildPrincipal(ctx.Execution.OwnerUserID)
	if err != nil {
		return nil, err
	}
	var result any
	switch action {
	case "enable", "disable":
		result, err = ctx.App.SetNotifyChannelEnabled(channelID, action == "enable", principal)
	case "test":
		result, err = ctx.App.TestNotifyChannel(ctx.Ctx, channelID, principal)
	default:
		return nil, bizErr("Unsupported config.notification-channel action: %s", action)
	}
	return workflowNodeResult(result, err)
}

func registerAdminNodes() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "admin.user", Label: "管理用户", RequiredPermission: perm.SystemUsersView,
		PermissionConfigKey: "action", PermissionByValue: map[string]string{
			"create": perm.SystemUsersCreate, "update": perm.SystemUsersUpdate, "delete": perm.SystemUsersDelete,
		},
		ExecutionMode: nodeExecutionHumanAction, SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action": M{"type": "string", "title": "操作", "enum": []string{"create", "update", "delete"}},
			"userId": M{"type": "integer", "title": "用户", "resource": "user"},
		}, "required": []string{"action"}}, Execute: adminUserExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "admin.role", Label: "管理角色", RequiredPermission: perm.SystemRolesView,
		PermissionConfigKey: "action", PermissionByValue: map[string]string{
			"create": perm.SystemRolesCreate, "update": perm.SystemRolesUpdate, "delete": perm.SystemRolesDelete,
		},
		ExecutionMode: nodeExecutionHumanAction, SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"action": M{"type": "string", "title": "操作", "enum": []string{"create", "update", "delete"}},
			"roleId": M{"type": "integer", "title": "角色", "resource": "role"},
		}, "required": []string{"action"}}, Execute: adminRoleExecute,
	})
	registerNode(&workflowNodeDefinition{
		TypeCode: "admin.permissions", Label: "分配权限", RequiredPermission: perm.SystemRolesAssignPermissions,
		ExecutionMode: nodeExecutionHumanAction, SecurityPolicy: nodeSecurityHumanReauth, OutputPorts: domainOutputPorts(),
		ConfigSchema: M{"type": "object", "properties": M{
			"roleId": M{"type": "integer", "title": "角色", "resource": "role"},
		}, "required": []string{"roleId"}}, Execute: adminPermissionsExecute,
	})
}

func adminUserExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action := cfgStr(config, "action", "")
	if action != "create" && action != "update" && action != "delete" {
		return nil, bizErr("Unsupported admin.user action: %s", action)
	}
	return workflowHumanAction(ctx, "admin.user."+action, "user", int64Text(asInt64(config["userId"])), M{"action": action})
}

func adminRoleExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	action := cfgStr(config, "action", "")
	if action != "create" && action != "update" && action != "delete" {
		return nil, bizErr("Unsupported admin.role action: %s", action)
	}
	return workflowHumanAction(ctx, "admin.role."+action, "role", int64Text(asInt64(config["roleId"])), M{"action": action})
}

func adminPermissionsExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	return workflowHumanAction(ctx, "admin.role.permissions", "role", int64Text(asInt64(nodeConfig(ctx)["roleId"])), M{"action": "assign_permissions"})
}

func workflowNodePayload[T any](ctx *nodeExecContext) (T, error) {
	var zero T
	payload, ok := ctx.Inputs["payload"].(map[string]any)
	if !ok {
		return zero, bizErr("Node requires a structured payload input")
	}
	return decodeWorkflowActionForm[T](payload)
}

func workflowHumanAction(ctx *nodeExecContext, actionType, targetType, targetID string, request M) (*nodeExecResult, error) {
	if _, err := requireWorkflowActionSpec(actionType); err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	output := M{"status": "waiting_action", "actionType": actionType, "targetType": targetType, "targetId": targetID}
	return &nodeExecResult{Output: output, Wait: &workflowWaitRequest{
		Kind: "human_action", ActionType: actionType, TargetType: targetType, TargetID: targetID,
		Request: request, ExpiresAt: &expiresAt,
	}}, nil
}

func workflowNodeResult(value any, err error) (*nodeExecResult, error) {
	if err != nil {
		return nil, err
	}
	return &nodeExecResult{Output: workflowResultMap(value)}, nil
}

func workflowResultMap(value any) M {
	if mapped, ok := workflowJSONValue(value).(map[string]any); ok {
		return mapped
	}
	return M{"result": workflowJSONValue(value)}
}

func workflowNodeIdempotencyKey(ctx *nodeExecContext) string {
	return fmt.Sprintf("workflow-node:%d:%s", ctx.Execution.ID, strings.TrimSpace(asString(ctx.Node["id"])))
}
