package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"coinsphere/backend/internal/perm"
)

func init() {
	registerWorkflowTradingActions()
	registerWorkflowContentActions()
	registerWorkflowAdminActions()
}

func registerWorkflowTradingActions() {
	trading := perm.TradingOverviewView
	registerWorkflowAction("trading.account.archive", workflowActionSpec{
		Title: "归档交易账户", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			err := app.ArchiveTradingAccount(ctx, p, target, key, token)
			return M{"archived": err == nil}, err
		},
	})
	registerWorkflowAction("trading.account.resume", workflowActionSpec{
		Title: "恢复交易账户", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			return app.ResumeTradingAccount(ctx, p, target, key, token)
		},
	})
	registerWorkflowAction("trading.automation.enable", workflowActionSpec{
		Title: "启用交易自动化", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			return app.SetTradingAutomation(ctx, p, target, true, key, token)
		},
	})
	registerWorkflowAction("trading.authorization.grant", workflowActionSpec{
		Title: "授予自动化授权", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			return app.SetTradingAuthorization(ctx, p, target, true, key, token)
		},
	})
	registerWorkflowAction("trading.emergency.release", workflowActionSpec{
		Title: "解除交易急停", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, _ string, _ M, key, token string) (any, error) {
			return app.ReleaseTradingEmergencyStop(ctx, p, key, token)
		},
	})
	registerWorkflowAction("trading.risk.update", workflowActionSpec{
		Title: "扩大交易风险边界", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		FormSchema: tradingRiskActionSchema(),
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, form M, key, token string) (any, error) {
			payload, err := decodeWorkflowActionForm[TradingRiskPayload](form)
			if err != nil {
				return nil, err
			}
			return app.UpdateTradingRisk(ctx, p, target, payload, key, token)
		},
	})
	registerWorkflowAction("trading.credentials.save", workflowActionSpec{
		Title: "保存交易凭据", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		FormSchema: M{"type": "object", "properties": M{
			"apiKey":                M{"type": "string", "title": "API Key", "secret": true},
			"apiSecret":             M{"type": "string", "title": "API Secret", "secret": true},
			"withdrawalDisabled":    M{"type": "boolean", "title": "已禁用提现"},
			"ipWhitelistConfigured": M{"type": "boolean", "title": "已配置 IP 白名单"},
		}, "required": []string{"apiKey", "apiSecret", "withdrawalDisabled", "ipWhitelistConfigured"}},
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, form M, key, token string) (any, error) {
			payload, err := decodeWorkflowActionForm[TradingCredentialPayload](form)
			if err != nil {
				return nil, err
			}
			return app.SaveTradingCredentials(ctx, p, target, payload, key, token)
		},
	})
	registerWorkflowAction("trading.credentials.revoke", workflowActionSpec{
		Title: "撤销交易凭据", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			return app.RevokeTradingCredentials(ctx, p, target, key, token)
		},
	})
	registerWorkflowAction("strategy.signal.approve", workflowActionSpec{
		Title: "批准策略信号", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			return app.DecideStrategySignal(ctx, p, target, "approved", key, token)
		},
	})
	registerWorkflowAction("strategy.signal.reject", workflowActionSpec{
		Title: "拒绝策略信号", RequiredPermission: trading, RequiresReauth: true, DispatchConsumesReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, key, token string) (any, error) {
			return app.DecideStrategySignal(ctx, p, target, "rejected", key, token)
		},
	})
}

func registerWorkflowContentActions() {
	registerWorkflowAction("strategy.archive", workflowActionSpec{
		Title: "归档策略", RequiredPermission: perm.TradingOverviewView, RequiresReauth: true,
		Dispatch: func(ctx context.Context, app *App, p *Principal, target string, _ M, _, _ string) (any, error) {
			err := app.ArchiveStrategyDraft(ctx, p.User.ID, target)
			return M{"archived": err == nil}, err
		},
	})
	registerWorkflowAction("news.delete", workflowActionSpec{
		Title: "删除新闻", RequiredPermission: perm.DataNewsDelete, RequiresReauth: true,
		Dispatch: func(ctx context.Context, app *App, _ *Principal, target string, _ M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			err = app.DeleteNews(id)
			return M{"deleted": err == nil}, err
		},
	})
	registerWorkflowAction("config.ai-model.delete", workflowActionSpec{
		Title: "删除 AI 模型配置", RequiredPermission: perm.ConfigAiModelsDelete, RequiresReauth: true,
		Dispatch: func(_ context.Context, app *App, p *Principal, target string, _ M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			err = app.DeleteAiModelConfig(id, p)
			return M{"deleted": err == nil}, err
		},
	})
	registerWorkflowAction("config.ai-model.create", workflowActionSpec{
		Title: "创建 AI 模型配置", RequiredPermission: perm.ConfigAiModelsCreate, RequiresReauth: true,
		FormSchema: aiModelActionSchema(false),
		Dispatch: func(_ context.Context, app *App, p *Principal, _ string, form M, _, _ string) (any, error) {
			payload, err := decodeAIModelActionForm(form)
			if err != nil {
				return nil, err
			}
			return app.CreateAiModelConfig(payload, p)
		},
	})
	registerWorkflowAction("config.ai-model.update", workflowActionSpec{
		Title: "修改 AI 模型配置", RequiredPermission: perm.ConfigAiModelsUpdate, RequiresReauth: true,
		FormSchema: aiModelActionSchema(true),
		Dispatch: func(_ context.Context, app *App, p *Principal, target string, form M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			payload, err := decodeAIModelActionForm(form)
			if err != nil {
				return nil, err
			}
			return app.UpdateAiModelConfig(id, payload, p)
		},
	})
	registerWorkflowAction("config.assistant.delete", workflowActionSpec{
		Title: "删除智能体配置", RequiredPermission: perm.ConfigAssistantAgentsDelete, RequiresReauth: true,
		Dispatch: func(_ context.Context, app *App, _ *Principal, target string, _ M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			err = app.DeleteAssistantAgent(id)
			return M{"deleted": err == nil}, err
		},
	})
	registerWorkflowAction("config.notification-channel.delete", workflowActionSpec{
		Title: "删除通知渠道", RequiredPermission: perm.ConfigNotificationChannelsDelete, RequiresReauth: true,
		Dispatch: func(_ context.Context, app *App, p *Principal, target string, _ M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			err = app.DeleteNotifyChannel(id, p)
			return M{"deleted": err == nil}, err
		},
	})
	registerWorkflowAction("config.notification-channel.create", workflowActionSpec{
		Title: "创建通知渠道", RequiredPermission: perm.ConfigNotificationChannelsCreate, RequiresReauth: true,
		FormSchema: notificationChannelActionSchema(false),
		Dispatch: func(_ context.Context, app *App, p *Principal, _ string, form M, _, _ string) (any, error) {
			payload, err := decodeNotificationChannelActionForm(form)
			if err != nil {
				return nil, err
			}
			return app.CreateNotifyChannel(payload, p)
		},
	})
	registerWorkflowAction("config.notification-channel.update", workflowActionSpec{
		Title: "修改通知渠道", RequiredPermission: perm.ConfigNotificationChannelsUpdate, RequiresReauth: true,
		FormSchema: notificationChannelActionSchema(true),
		Dispatch: func(_ context.Context, app *App, p *Principal, target string, form M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			payload, err := decodeNotificationChannelActionForm(form)
			if err != nil {
				return nil, err
			}
			return app.UpdateNotifyChannel(id, payload, p)
		},
	})
}

func registerWorkflowAdminActions() {
	registerWorkflowAction("admin.user.create", workflowActionSpec{
		Title: "创建用户", RequiredPermission: perm.SystemUsersCreate, RequiresReauth: true,
		FormSchema: userActionSchema(false),
		Dispatch: func(_ context.Context, app *App, p *Principal, _ string, form M, _, _ string) (any, error) {
			payload, err := decodeWorkflowActionForm[UserUpsertPayload](form)
			if err != nil {
				return nil, err
			}
			return app.CreateUser(payload, p)
		},
	})
	registerWorkflowAction("admin.user.update", workflowActionSpec{
		Title: "修改用户", RequiredPermission: perm.SystemUsersUpdate, RequiresReauth: true,
		FormSchema: userActionSchema(true),
		Dispatch: func(_ context.Context, app *App, p *Principal, target string, form M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			payload, err := decodeWorkflowActionForm[UserUpsertPayload](form)
			if err != nil {
				return nil, err
			}
			return app.UpdateUser(id, payload, p)
		},
	})
	registerWorkflowAction("admin.user.delete", workflowActionSpec{
		Title: "删除用户", RequiredPermission: perm.SystemUsersDelete, RequiresReauth: true,
		Dispatch: func(_ context.Context, app *App, p *Principal, target string, _ M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			err = app.DeleteUser(id, p)
			return M{"deleted": err == nil}, err
		},
	})
	registerWorkflowAction("admin.role.create", workflowActionSpec{
		Title: "创建角色", RequiredPermission: perm.SystemRolesCreate, RequiresReauth: true,
		FormSchema: roleActionSchema(),
		Dispatch: func(_ context.Context, app *App, _ *Principal, _ string, form M, _, _ string) (any, error) {
			payload, err := decodeWorkflowActionForm[RoleUpsertPayload](form)
			if err != nil {
				return nil, err
			}
			return app.CreateRole(payload)
		},
	})
	registerWorkflowAction("admin.role.update", workflowActionSpec{
		Title: "修改角色", RequiredPermission: perm.SystemRolesUpdate, RequiresReauth: true,
		FormSchema: roleActionSchema(),
		Dispatch: func(_ context.Context, app *App, _ *Principal, target string, form M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			payload, err := decodeWorkflowActionForm[RoleUpsertPayload](form)
			if err != nil {
				return nil, err
			}
			return app.UpdateRole(id, payload)
		},
	})
	registerWorkflowAction("admin.role.delete", workflowActionSpec{
		Title: "删除角色", RequiredPermission: perm.SystemRolesDelete, RequiresReauth: true,
		Dispatch: func(_ context.Context, app *App, _ *Principal, target string, _ M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			err = app.DeleteRole(id)
			return M{"deleted": err == nil}, err
		},
	})
	registerWorkflowAction("admin.role.permissions", workflowActionSpec{
		Title: "修改角色权限", RequiredPermission: perm.SystemRolesAssignPermissions, RequiresReauth: true,
		FormSchema: M{"type": "object", "properties": M{
			"menuIds":   M{"type": "array", "title": "菜单", "items": M{"type": "integer"}},
			"buttonIds": M{"type": "array", "title": "操作权限", "items": M{"type": "integer"}},
		}, "required": []string{"menuIds", "buttonIds"}},
		Dispatch: func(_ context.Context, app *App, _ *Principal, target string, form M, _, _ string) (any, error) {
			id, err := workflowActionInt64(target)
			if err != nil {
				return nil, err
			}
			payload, err := decodeWorkflowActionForm[RolePermissionPayload](form)
			if err != nil {
				return nil, err
			}
			err = app.SaveRolePermissions(id, payload)
			return M{"updated": err == nil}, err
		},
	})
}

func decodeWorkflowActionForm[T any](form M) (T, error) {
	var result T
	raw, err := json.Marshal(form)
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, bizErr("Invalid workflow action form: %s", err.Error())
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return result, bizErr("Invalid workflow action form")
	}
	return result, nil
}

func workflowActionInt64(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, bizErr("Invalid workflow action target")
	}
	return id, nil
}

func tradingRiskActionSchema() M {
	decimalField := func(title string) M { return M{"type": "string", "format": "decimal", "title": title} }
	return M{"type": "object", "properties": M{
		"instrumentIds":    M{"type": "array", "title": "品种白名单", "items": M{"type": "string"}},
		"maxTotalNotional": decimalField("总名义价值上限"), "maxSymbolNotional": decimalField("单品种上限"),
		"maxOrderNotional": decimalField("单笔上限"), "maxDailyLoss": decimalField("单日亏损上限"),
		"maxDrawdown": decimalField("最大回撤"), "maxQuoteAgeSeconds": M{"type": "integer", "title": "行情最大延迟秒数"},
		"leverage": M{"type": "integer", "title": "杠杆"},
	}}
}

type workflowAIModelActionForm struct {
	Provider        string `json:"provider"`
	ProviderName    string `json:"providerName"`
	DisplayName     string `json:"displayName"`
	ModelIdentifier string `json:"modelIdentifier"`
	BaseURL         string `json:"baseUrl"`
	APIKey          string `json:"apiKey"`
	IsEnabled       *bool  `json:"isEnabled"`
	Priority        int    `json:"priority"`
	RequestHeaders  M      `json:"requestHeaders"`
	RequestBody     M      `json:"requestBody"`
	TimeoutMs       int    `json:"timeoutMs"`
	Remark          string `json:"remark"`
}

func decodeAIModelActionForm(form M) (AiModelUpsertPayload, error) {
	value, err := decodeWorkflowActionForm[workflowAIModelActionForm](form)
	if err != nil {
		return AiModelUpsertPayload{}, err
	}
	return AiModelUpsertPayload{
		Provider: value.Provider, ProviderName: value.ProviderName, DisplayName: value.DisplayName,
		ModelIdentifier: value.ModelIdentifier, BaseURL: value.BaseURL, APIKey: value.APIKey,
		IsEnabled: value.IsEnabled, Priority: value.Priority, RequestHeadersJSON: dumpJSON(value.RequestHeaders),
		RequestBodyJSON: dumpJSON(value.RequestBody), TimeoutMs: value.TimeoutMs, Remark: value.Remark,
	}, nil
}

func aiModelActionSchema(update bool) M {
	properties := M{
		"provider":        M{"type": "string", "title": "提供商", "enum": []string{aiProviderOpenAICompatible, aiProviderAnthropic, aiProviderGemini}},
		"providerName":    M{"type": "string", "title": "提供商名称"},
		"displayName":     M{"type": "string", "title": "显示名称"},
		"modelIdentifier": M{"type": "string", "title": "模型标识"},
		"baseUrl":         M{"type": "string", "title": "服务地址"},
		"apiKey":          M{"type": "string", "title": "API Key", "secret": true},
		"isEnabled":       M{"type": "boolean", "title": "启用"},
		"priority":        M{"type": "integer", "title": "优先级"},
		"requestHeaders":  M{"type": "object", "title": "请求头"},
		"requestBody":     M{"type": "object", "title": "请求参数"},
		"timeoutMs":       M{"type": "integer", "title": "超时毫秒"},
		"remark":          M{"type": "string", "title": "备注"},
	}
	required := []string{"provider", "displayName", "modelIdentifier"}
	if !update {
		required = append(required, "apiKey")
	}
	return M{"type": "object", "properties": properties, "required": required}
}

type workflowNotificationChannelActionForm struct {
	ChannelType string `json:"channelType"`
	DisplayName string `json:"displayName"`
	IsEnabled   *bool  `json:"isEnabled"`
	Settings    M      `json:"settings"`
	Secrets     M      `json:"secrets"`
	Remark      string `json:"remark"`
}

func decodeNotificationChannelActionForm(form M) (NotifyChannelUpsertPayload, error) {
	value, err := decodeWorkflowActionForm[workflowNotificationChannelActionForm](form)
	if err != nil {
		return NotifyChannelUpsertPayload{}, err
	}
	var secrets *string
	if value.Secrets != nil {
		encoded := dumpJSON(value.Secrets)
		secrets = &encoded
	}
	return NotifyChannelUpsertPayload{
		ChannelType: value.ChannelType, DisplayName: value.DisplayName, IsEnabled: value.IsEnabled,
		SettingsJSON: dumpJSON(value.Settings), SecretJSON: secrets, Remark: value.Remark,
	}, nil
}

func notificationChannelActionSchema(update bool) M {
	properties := M{
		"channelType": M{"type": "string", "title": "渠道类型", "enum": []string{"dingtalk_webhook", "qq_bot", "smtp_email"}},
		"displayName": M{"type": "string", "title": "渠道名称"},
		"isEnabled":   M{"type": "boolean", "title": "启用"},
		"settings":    M{"type": "object", "title": "渠道设置"},
		"secrets":     M{"type": "object", "title": "渠道密钥", "secret": true},
		"remark":      M{"type": "string", "title": "备注"},
	}
	required := []string{"displayName", "settings"}
	if !update {
		required = append(required, "channelType", "secrets")
	}
	return M{"type": "object", "properties": properties, "required": required}
}

func userActionSchema(update bool) M {
	properties := M{
		"username": M{"type": "string", "title": "用户名"}, "nickname": M{"type": "string", "title": "昵称"},
		"fullName": M{"type": "string", "title": "姓名"}, "gender": M{"type": "string", "title": "性别", "enum": []string{"male", "female", "unknown"}},
		"phone": M{"type": "string", "title": "手机号"}, "email": M{"type": "string", "title": "邮箱"},
		"avatar": M{"type": "string", "title": "头像"}, "isActive": M{"type": "boolean", "title": "启用"},
		"password":  M{"type": "string", "title": "密码", "secret": true},
		"roleCodes": M{"type": "array", "title": "角色", "items": M{"type": "string"}},
	}
	required := []string{"username", "nickname", "roleCodes"}
	if !update {
		required = append(required, "password")
	}
	return M{"type": "object", "properties": properties, "required": required}
}

func roleActionSchema() M {
	return M{"type": "object", "properties": M{
		"displayName": M{"type": "string", "title": "角色名称"}, "code": M{"type": "string", "title": "角色编码"},
		"description": M{"type": "string", "title": "说明"}, "isEnabled": M{"type": "boolean", "title": "启用"},
	}, "required": []string{"displayName", "code"}}
}
