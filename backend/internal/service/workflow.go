package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	WorkflowModeBatch         = "batch"
	WorkflowModeEvent         = "event"
	WorkflowModeStream        = "stream"
	WorkflowStatusInactive    = "inactive"
	WorkflowStatusActive      = "active"
	WorkflowStatusError       = "error"
	WorkflowTemplateBlank     = "blank"
	WorkflowTemplateSchedule  = "scheduled"
	WorkflowTemplateEvent     = "event"
	WorkflowTemplateFailure   = "failure-handler"
	WorkflowTemplateWebhook   = "connector-webhook"
	WorkflowTemplateWebSocket = "connector-websocket"
	WorkflowTemplateQuantData = "quant-market-data"
	WorkflowTemplateQuantLive = "quant-strategy"
	WorkflowTemplateBacktest  = "quant-backtest"
	WorkflowTemplateQuantFlow = "quant-workflow"
	WorkflowTemplatePaper     = "quant-paper"
	maxWorkflowGraphBytes     = 1 << 20
)

const blankWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"manual-trigger","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"manual-to-end","sourceNodeInstanceId":"manual-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const scheduledWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"schedule-trigger","nodeType":"core.schedule","nodeVersion":"1.0.0","config":{"everySeconds":3600},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"schedule-to-end","sourceNodeInstanceId":"schedule-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const eventWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"event-trigger","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["example.event"]},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"event-to-end","sourceNodeInstanceId":"event-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const failureWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"failure-trigger","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["io.coinsphere.workflow.run.failed"],"source":"urn:coinsphere:workflow-core"},"position":{"x":100,"y":220}},
    {"nodeInstanceId":"notify","nodeType":"official.notification.in_app","nodeVersion":"1.0.0","config":{"title":"工作流执行失败"},"inputBindings":{"subjectKey":{"kind":"cel","expression":"event.id"},"message":{"kind":"literal","value":"工作流运行失败，请在工作台查看受控错误分类。"}},"position":{"x":400,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":700,"y":220}}
  ],
  "edges": [
    {"edgeId":"failure-to-notify","sourceNodeInstanceId":"failure-trigger","sourcePort":"out","targetNodeInstanceId":"notify","targetPort":"in"},
    {"edgeId":"notify-to-end","sourceNodeInstanceId":"notify","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const webhookWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"webhook-trigger","nodeType":"official.connector.webhook","nodeVersion":"1.0.0","config":{"eventType":"example.webhook"},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"webhook-to-end","sourceNodeInstanceId":"webhook-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const webSocketWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"websocket-trigger","nodeType":"official.connector.websocket","nodeVersion":"1.0.0","config":{"url":"wss://stream.example.com/events","eventType":"example.event","idField":"id","partitionField":"partitionKey","useAuthorization":false},"position":{"x":160,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"websocket-to-end","sourceNodeInstanceId":"websocket-trigger","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const quantMarketDataWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"market-stream","nodeType":"official.quant.realtime_candles","nodeVersion":"1.0.0","config":{"market":"spot","instrument":"BTCUSDT","intervals":["1h"]},"position":{"x":140,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":520,"y":220}}
  ],
  "edges": [
    {"edgeId":"market-to-end","sourceNodeInstanceId":"market-stream","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const quantStrategyWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"candle-event","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["market.candle.closed"],"source":"urn:coinsphere:plugin:official.quant","subject":"binance:spot:BTCUSDT:1h"},"position":{"x":100,"y":220}},
    {"nodeInstanceId":"strategy","nodeType":"official.quant.evaluate","nodeVersion":"1.0.0","config":{"strategyId":"official.quant.sma-crossover","market":"spot","instrument":"BTCUSDT","interval":"1h","parameters":{"fastPeriod":3,"slowPeriod":5}},"inputBindings":{"eventTime":{"kind":"cel","expression":"event.time"}},"position":{"x":400,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":700,"y":220}}
  ],
  "edges": [
    {"edgeId":"event-to-strategy","sourceNodeInstanceId":"candle-event","sourcePort":"out","targetNodeInstanceId":"strategy","targetPort":"in"},
    {"edgeId":"strategy-to-end","sourceNodeInstanceId":"strategy","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const quantBacktestWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"manual-trigger","nodeType":"core.manual","nodeVersion":"1.0.0","config":{},"position":{"x":100,"y":220}},
    {"nodeInstanceId":"backtest","nodeType":"official.quant.backtest","nodeVersion":"1.0.0","config":{"strategyId":"official.quant.sma-crossover","market":"spot","instrument":"BTCUSDT","interval":"1h","startTime":"2026-01-01T00:00:00Z","endTime":"2026-02-01T00:00:00Z","initialCapital":"10000","feeRate":"0.001","slippageRate":"0.0005","parameters":{"fastPeriod":3,"slowPeriod":5}},"position":{"x":400,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":700,"y":220}}
  ],
  "edges": [
    {"edgeId":"manual-to-backtest","sourceNodeInstanceId":"manual-trigger","sourcePort":"out","targetNodeInstanceId":"backtest","targetPort":"in"},
    {"edgeId":"backtest-to-end","sourceNodeInstanceId":"backtest","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

const quantWorkflowGraph = `{
  "schemaVersion": 2,
  "entryPoints": {"realtime":"candle-close","backtest":"backtest-start"},
  "nodes": [
    {"nodeInstanceId":"candle-close","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["market.candle.closed"],"source":"urn:coinsphere:plugin:official.quant","subject":"binance:spot:BTCUSDT:1h"},"position":{"x":80,"y":120}},
    {"nodeInstanceId":"backtest-start","nodeType":"official.quant.backtest_start","nodeVersion":"1.0.0","config":{"market":"spot","instrument":"BTCUSDT","interval":"1h"},"position":{"x":80,"y":360}},
    {"nodeInstanceId":"macd","nodeType":"official.quant.macd_condition","nodeVersion":"1.0.0","config":{"market":"spot","instrument":"BTCUSDT","checkInterval":"1h","name":"MACD 金叉","interval":"1h","parameters":{"fastPeriod":12,"slowPeriod":26,"signalPeriod":9,"signal":"golden_cross"}},"inputBindings":{"eventTime":{"kind":"cel","expression":"event.time"}},"position":{"x":380,"y":120}},
    {"nodeInstanceId":"code","nodeType":"official.quant.code_strategy","nodeVersion":"1.0.0","config":{"series":[{"alias":"main","market":"spot","instrument":"BTCUSDT","interval":"1h","lookback":30}],"parameters":{"target":"1"},"source":"{\"long\": decimalGt(last(ohlcv.main.close), sma(ohlcv.main.close, 20)), \"target\": params.target}","booleanOutputs":["long"],"decimalOutputs":["target"],"branchField":"long"},"inputBindings":{"eventTime":{"kind":"cel","expression":"event.time"}},"position":{"x":380,"y":360}},
    {"nodeInstanceId":"position","nodeType":"official.quant.position","nodeVersion":"1.0.0","config":{"market":"spot","targetMode":"fixed","fixedTarget":"1","decimalField":"target"},"inputBindings":{"evaluatedAt":{"kind":"field","nodeInstanceId":"code","fieldPath":["evaluatedAt"]}},"position":{"x":700,"y":240}},
    {"nodeInstanceId":"signal","nodeType":"official.quant.output_signal","nodeVersion":"1.0.0","config":{"market":"spot","instrument":"BTCUSDT","interval":"1h"},"position":{"x":980,"y":240}},
    {"nodeInstanceId":"realtime-end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":1280,"y":160}},
    {"nodeInstanceId":"backtest-end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":1280,"y":400}}
  ],
  "edges": [
    {"edgeId":"realtime-macd","sourceNodeInstanceId":"candle-close","sourcePort":"out","targetNodeInstanceId":"macd","targetPort":"in"},
    {"edgeId":"realtime-code","sourceNodeInstanceId":"candle-close","sourcePort":"out","targetNodeInstanceId":"code","targetPort":"in"},
    {"edgeId":"backtest-macd","sourceNodeInstanceId":"backtest-start","sourcePort":"each","targetNodeInstanceId":"macd","targetPort":"in"},
    {"edgeId":"backtest-code","sourceNodeInstanceId":"backtest-start","sourcePort":"each","targetNodeInstanceId":"code","targetPort":"in"},
    {"edgeId":"macd-position","sourceNodeInstanceId":"macd","sourcePort":"true","targetNodeInstanceId":"position","targetPort":"in"},
    {"edgeId":"code-position","sourceNodeInstanceId":"code","sourcePort":"true","targetNodeInstanceId":"position","targetPort":"in"},
    {"edgeId":"position-signal","sourceNodeInstanceId":"position","sourcePort":"out","targetNodeInstanceId":"signal","targetPort":"in"},
    {"edgeId":"signal-realtime-end","sourceNodeInstanceId":"signal","sourcePort":"realtime","targetNodeInstanceId":"realtime-end","targetPort":"in"},
    {"edgeId":"signal-unchanged-end","sourceNodeInstanceId":"signal","sourcePort":"unchanged","targetNodeInstanceId":"realtime-end","targetPort":"in"},
    {"edgeId":"backtest-completed","sourceNodeInstanceId":"backtest-start","sourcePort":"completed","targetNodeInstanceId":"backtest-end","targetPort":"in"}
  ]
}`

const quantPaperWorkflowGraph = `{
  "schemaVersion": 1,
  "nodes": [
    {"nodeInstanceId":"candle-event","nodeType":"core.event","nodeVersion":"1.0.0","config":{"types":["market.candle.closed"],"source":"urn:coinsphere:plugin:official.quant","subject":"binance:spot:BTCUSDT:1h"},"position":{"x":80,"y":220}},
    {"nodeInstanceId":"strategy","nodeType":"official.quant.evaluate","nodeVersion":"1.0.0","config":{"strategyId":"official.quant.sma-crossover","market":"spot","instrument":"BTCUSDT","interval":"1h","parameters":{"fastPeriod":3,"slowPeriod":5}},"inputBindings":{"eventTime":{"kind":"cel","expression":"event.time"}},"position":{"x":320,"y":220}},
    {"nodeInstanceId":"signal","nodeType":"official.quant.signal","nodeVersion":"1.0.0","config":{"market":"spot","instrument":"BTCUSDT","interval":"1h"},"inputBindings":{"strategyId":{"kind":"field","nodeInstanceId":"strategy","fieldPath":["strategyId"]},"strategyVersion":{"kind":"field","nodeInstanceId":"strategy","fieldPath":["strategyVersion"]},"target":{"kind":"field","nodeInstanceId":"strategy","fieldPath":["target"]},"evaluatedAt":{"kind":"field","nodeInstanceId":"strategy","fieldPath":["evaluatedAt"]},"businessKey":{"kind":"cel","expression":"event.subject"}},"position":{"x":560,"y":220}},
    {"nodeInstanceId":"approve","nodeType":"core.human_approval","nodeVersion":"1.0.0","config":{"decisionMode":"human","taskType":"paper_signal","prompt":"审批 Paper 策略信号","expiresSeconds":86400},"inputBindings":{"businessKey":{"kind":"field","nodeInstanceId":"signal","fieldPath":["businessKey"]}},"position":{"x":800,"y":220}},
    {"nodeInstanceId":"paper","nodeType":"official.quant.paper_execute","nodeVersion":"1.0.0","config":{"decisionMode":"human","market":"spot","instrument":"BTCUSDT","interval":"1h","initialBalance":"10000","feeRate":"0.001","maxTotalNotional":"10000","maxInstrumentNotional":"10000","maxOperationNotional":"2500","maxDailyLoss":"500","maxDrawdown":"0.1","maxQuoteAgeSeconds":10},"inputBindings":{"signalId":{"kind":"field","nodeInstanceId":"signal","fieldPath":["signalId"]},"decisionTaskId":{"kind":"field","nodeInstanceId":"approve","fieldPath":["taskId"]},"decisionStatus":{"kind":"field","nodeInstanceId":"approve","fieldPath":["status"]}},"position":{"x":1040,"y":220}},
    {"nodeInstanceId":"notify","nodeType":"official.notification.in_app","nodeVersion":"1.0.0","config":{"title":"Paper 信号已处理"},"inputBindings":{"subjectKey":{"kind":"field","nodeInstanceId":"signal","fieldPath":["businessKey"]},"message":{"kind":"literal","value":"Paper 信号已完成风险检查与执行处理。"}},"position":{"x":1280,"y":220}},
    {"nodeInstanceId":"end","nodeType":"core.end","nodeVersion":"1.0.0","config":{},"position":{"x":1520,"y":220}}
  ],
  "edges": [
    {"edgeId":"event-to-strategy","sourceNodeInstanceId":"candle-event","sourcePort":"out","targetNodeInstanceId":"strategy","targetPort":"in"},
    {"edgeId":"strategy-to-signal","sourceNodeInstanceId":"strategy","sourcePort":"out","targetNodeInstanceId":"signal","targetPort":"in"},
    {"edgeId":"signal-to-approve","sourceNodeInstanceId":"signal","sourcePort":"out","targetNodeInstanceId":"approve","targetPort":"in"},
    {"edgeId":"approve-to-paper","sourceNodeInstanceId":"approve","sourcePort":"out","targetNodeInstanceId":"paper","targetPort":"in"},
    {"edgeId":"paper-to-notify","sourceNodeInstanceId":"paper","sourcePort":"out","targetNodeInstanceId":"notify","targetPort":"in"},
    {"edgeId":"notify-to-end","sourceNodeInstanceId":"notify","sourcePort":"out","targetNodeInstanceId":"end","targetPort":"in"}
  ]
}`

type WorkflowTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

type WorkflowCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	TemplateKey string `json:"templateKey"`
}

type WorkflowUpdatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type WorkflowRevisionSavePayload struct {
	ExpectedActiveRevisionID  int64                  `json:"expectedActiveRevisionId"`
	Graph                     json.RawMessage        `json:"graph"`
	SecretChanges             []WorkflowSecretChange `json:"secretChanges,omitempty"`
	ResetStateNodeInstanceIDs []string               `json:"resetStateNodeInstanceIds,omitempty"`
}

type WorkflowLifecyclePayload struct {
	Action string `json:"action"`
}

type WorkflowView struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Mode              string `json:"mode"`
	Status            string `json:"status"`
	ActiveRevisionID  int64  `json:"activeRevisionId"`
	MainTriggerNodeID string `json:"mainTriggerNodeId"`
	RetentionDays     int    `json:"retentionDays"`
	CreatedBy         int64  `json:"createdBy"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

type WorkflowRuntimeView struct {
	MaxConcurrentRuns int    `json:"maxConcurrentRuns"`
	BacklogLimit      int    `json:"backlogLimit"`
	NextScheduledAt   string `json:"nextScheduledAt,omitempty"`
	LastScheduledAt   string `json:"lastScheduledAt,omitempty"`
	UpdatedAt         string `json:"updatedAt"`
}

type WorkflowDetail struct {
	WorkflowView
	Runtime              WorkflowRuntimeView `json:"runtime"`
	StateNodeInstanceIDs []string            `json:"stateNodeInstanceIds"`
}

type WorkflowRevisionView struct {
	ID                int64                      `json:"id"`
	WorkflowID        int64                      `json:"workflowId"`
	RevisionNumber    int64                      `json:"revisionNumber"`
	Graph             json.RawMessage            `json:"graph"`
	NodeVersions      json.RawMessage            `json:"nodeVersions"`
	MainTriggerNodeID string                     `json:"mainTriggerNodeId"`
	CreatedBy         int64                      `json:"createdBy"`
	CreatedAt         string                     `json:"createdAt"`
	SecretFields      map[string]map[string]bool `json:"secretFields"`
}

func (a *App) ListWorkflowTemplates() []WorkflowTemplate {
	return []WorkflowTemplate{
		{Key: WorkflowTemplateBlank, Name: "Blank batch workflow", Mode: WorkflowModeBatch, Description: "Manual trigger connected to an end node."},
		{Key: WorkflowTemplateSchedule, Name: "Scheduled batch workflow", Mode: WorkflowModeBatch, Description: "UTC interval trigger connected to an end node."},
		{Key: WorkflowTemplateEvent, Name: "Event workflow", Mode: WorkflowModeEvent, Description: "CloudEvent trigger connected to an end node."},
		{Key: WorkflowTemplateFailure, Name: "Failure handler", Mode: WorkflowModeEvent, Description: "Standard workflow failure trigger connected to an end node."},
		{Key: WorkflowTemplateWebhook, Name: "Connector webhook", Mode: WorkflowModeBatch, Description: "Authenticated webhook trigger connected to an end node."},
		{Key: WorkflowTemplateWebSocket, Name: "Connector WebSocket", Mode: WorkflowModeStream, Description: "Public WebSocket stream trigger connected to an end node."},
		{Key: WorkflowTemplateQuantData, Name: "Shared Binance market data", Mode: WorkflowModeStream, Description: "Shared public real-time closed-candle collection for Spot or USD-M."},
		{Key: WorkflowTemplateQuantLive, Name: "Live strategy evaluation", Mode: WorkflowModeEvent, Description: "Evaluate a trusted Go strategy for each matching closed candle."},
		{Key: WorkflowTemplateBacktest, Name: "Strategy backtest", Mode: WorkflowModeBatch, Description: "Run a deterministic closed-candle backtest in the compute pool."},
		{Key: WorkflowTemplateQuantFlow, Name: "量化策略与回测", Mode: WorkflowModeEvent, Description: "同一 revision 复用实时 K 线与节点化回测计算子图。"},
		{Key: WorkflowTemplatePaper, Name: "Paper strategy pair", Mode: WorkflowModeEvent, Description: "Create shared market data and an approval-first Paper strategy workflow."},
	}
}

func (a *App) CreateWorkflow(ctx context.Context, payload WorkflowCreatePayload, principal *Principal) (WorkflowDetail, error) {
	name := strings.TrimSpace(payload.Name)
	description := strings.TrimSpace(payload.Description)
	templateKey := strings.TrimSpace(payload.TemplateKey)
	if templateKey == "" {
		templateKey = WorkflowTemplateBlank
	}
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return WorkflowDetail{}, errors.New("workflow name must contain 1 to 120 characters")
	}
	if utf8.RuneCountInString(description) > 500 {
		return WorkflowDetail{}, errors.New("workflow description must not exceed 500 characters")
	}
	if templateKey != WorkflowTemplateBlank && templateKey != WorkflowTemplateSchedule &&
		templateKey != WorkflowTemplateEvent && templateKey != WorkflowTemplateFailure &&
		templateKey != WorkflowTemplateWebhook && templateKey != WorkflowTemplateWebSocket &&
		templateKey != WorkflowTemplateQuantData && templateKey != WorkflowTemplateQuantLive &&
		templateKey != WorkflowTemplateBacktest && templateKey != WorkflowTemplateQuantFlow && templateKey != WorkflowTemplatePaper {
		return WorkflowDetail{}, fmt.Errorf("unknown workflow template %q", templateKey)
	}
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowDetail{}, ErrPermission
	}
	templateGraph := blankWorkflowGraph
	if templateKey == WorkflowTemplateSchedule {
		templateGraph = scheduledWorkflowGraph
	} else if templateKey == WorkflowTemplateEvent {
		templateGraph = eventWorkflowGraph
	} else if templateKey == WorkflowTemplateFailure {
		templateGraph = failureWorkflowGraph
	} else if templateKey == WorkflowTemplateWebhook {
		templateGraph = webhookWorkflowGraph
	} else if templateKey == WorkflowTemplateWebSocket {
		templateGraph = webSocketWorkflowGraph
	} else if templateKey == WorkflowTemplateQuantData {
		templateGraph = quantMarketDataWorkflowGraph
	} else if templateKey == WorkflowTemplateQuantLive {
		templateGraph = quantStrategyWorkflowGraph
	} else if templateKey == WorkflowTemplateBacktest {
		templateGraph = quantBacktestWorkflowGraph
	} else if templateKey == WorkflowTemplateQuantFlow {
		templateGraph = quantWorkflowGraph
	} else if templateKey == WorkflowTemplatePaper {
		templateGraph = quantPaperWorkflowGraph
	}
	graph, err := a.validateWorkflowGraph(json.RawMessage(templateGraph))
	if err != nil {
		return WorkflowDetail{}, errors.New("workflow template is invalid")
	}

	now := time.Now().UTC()
	workflow := db.Workflow{
		Name: name, Description: description, Mode: workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusInactive,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, CreatedBy: principal.User.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	var marketGraph validatedWorkflowGraph
	if templateKey == WorkflowTemplatePaper {
		marketGraph, err = a.validateWorkflowGraph(json.RawMessage(quantMarketDataWorkflowGraph))
		if err != nil {
			return WorkflowDetail{}, errors.New("paper market data template is invalid")
		}
	}
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if templateKey == WorkflowTemplatePaper {
			if _, err := createWorkflowRecord(tx, name+" Market Data", "Paper shared public market data", marketGraph, principal.User.ID, now); err != nil {
				return err
			}
		}
		var createErr error
		workflow, createErr = createWorkflowRecord(tx, name, description, graph, principal.User.ID, now)
		return createErr
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	return a.GetWorkflow(ctx, workflow.ID)
}

func createWorkflowRecord(tx *gorm.DB, name, description string, graph validatedWorkflowGraph, userID int64, now time.Time) (db.Workflow, error) {
	workflow := db.Workflow{
		Name: name, Description: description, Mode: workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), Status: WorkflowStatusInactive,
		MainTriggerNodeID: graph.mainTriggerID, RetentionDays: 30, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(&workflow).Error; err != nil {
		return db.Workflow{}, errors.New("create workflow failed")
	}
	revision := db.WorkflowRevision{
		WorkflowID: workflow.ID, RevisionNumber: 1, GraphJSON: graph.graphJSON,
		NodeVersions: graph.nodeVersionsJSON, MainTriggerNodeID: graph.mainTriggerID, CreatedBy: userID, CreatedAt: now,
	}
	if err := tx.Create(&revision).Error; err != nil {
		return db.Workflow{}, errors.New("create initial workflow revision failed")
	}
	workflow.ActiveRevisionID = &revision.ID
	if err := tx.Model(&db.Workflow{}).Where("id = ?", workflow.ID).Update("active_revision_id", revision.ID).Error; err != nil {
		return db.Workflow{}, errors.New("activate initial workflow revision failed")
	}
	if err := tx.Create(&db.WorkflowRuntime{
		WorkflowID: workflow.ID, MaxConcurrentRuns: 2, BacklogLimit: 100, UpdatedAt: now,
	}).Error; err != nil {
		return db.Workflow{}, errors.New("create workflow runtime failed")
	}
	return workflow, nil
}

func (a *App) ListWorkflows(ctx context.Context, status string) ([]WorkflowView, error) {
	status = strings.TrimSpace(status)
	if status != "" && !validWorkflowStatus(status) {
		return nil, errors.New("invalid workflow status")
	}
	query := a.DB.WithContext(ctx).Order("updated_at DESC, id DESC")
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var workflows []db.Workflow
	if err := query.Find(&workflows).Error; err != nil {
		return nil, errors.New("list workflows failed")
	}
	items := make([]WorkflowView, 0, len(workflows))
	for _, workflow := range workflows {
		items = append(items, workflowView(workflow))
	}
	return items, nil
}

func (a *App) GetWorkflow(ctx context.Context, workflowID int64) (WorkflowDetail, error) {
	var workflow db.Workflow
	if err := a.DB.WithContext(ctx).First(&workflow, workflowID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowDetail{}, fmt.Errorf("%w: workflow", ErrNotFound)
		}
		return WorkflowDetail{}, errors.New("load workflow failed")
	}
	var runtime db.WorkflowRuntime
	if err := a.DB.WithContext(ctx).First(&runtime, "workflow_id = ?", workflowID).Error; err != nil {
		return WorkflowDetail{}, errors.New("load workflow runtime failed")
	}
	stateNodeInstanceIDs := make([]string, 0)
	if err := a.DB.WithContext(ctx).Model(&db.WorkflowNodeState{}).Where("workflow_id = ?", workflowID).
		Order("node_instance_id").Pluck("node_instance_id", &stateNodeInstanceIDs).Error; err != nil {
		return WorkflowDetail{}, errors.New("load workflow node states failed")
	}
	runtimeView := WorkflowRuntimeView{
		MaxConcurrentRuns: runtime.MaxConcurrentRuns, BacklogLimit: runtime.BacklogLimit,
		UpdatedAt: formatWorkflowTime(runtime.UpdatedAt),
	}
	if runtime.NextScheduledAt != nil {
		runtimeView.NextScheduledAt = formatWorkflowTime(*runtime.NextScheduledAt)
	}
	if runtime.LastScheduledAt != nil {
		runtimeView.LastScheduledAt = formatWorkflowTime(*runtime.LastScheduledAt)
	}
	return WorkflowDetail{
		WorkflowView:         workflowView(workflow),
		Runtime:              runtimeView,
		StateNodeInstanceIDs: stateNodeInstanceIDs,
	}, nil
}

func (a *App) UpdateWorkflow(ctx context.Context, workflowID int64, payload WorkflowUpdatePayload) (WorkflowDetail, error) {
	name := strings.TrimSpace(payload.Name)
	description := strings.TrimSpace(payload.Description)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return WorkflowDetail{}, errors.New("workflow name must contain 1 to 120 characters")
	}
	if utf8.RuneCountInString(description) > 500 {
		return WorkflowDetail{}, errors.New("workflow description must not exceed 500 characters")
	}
	database := a.DB.WithContext(ctx)
	result := database.Model(&db.Workflow{}).Where("id = ?", workflowID).
		Updates(map[string]any{
			"name": name, "description": description, "updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return WorkflowDetail{}, errors.New("update workflow failed")
	}
	if result.RowsAffected == 0 {
		var workflow db.Workflow
		if err := database.Select("id").First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return WorkflowDetail{}, fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return WorkflowDetail{}, errors.New("load workflow failed")
		}
		return WorkflowDetail{}, errors.New("update workflow failed")
	}
	return a.GetWorkflow(ctx, workflowID)
}

func (a *App) SaveWorkflowRevision(ctx context.Context, workflowID int64, payload WorkflowRevisionSavePayload, principal *Principal) (WorkflowRevisionView, error) {
	if payload.ExpectedActiveRevisionID <= 0 {
		return WorkflowRevisionView{}, errors.New("expectedActiveRevisionId must be positive")
	}
	if principal == nil || principal.User == nil || principal.User.ID <= 0 {
		return WorkflowRevisionView{}, ErrPermission
	}
	graph, err := a.validateWorkflowGraph(payload.Graph)
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	secretChanges, err := validateWorkflowSecretChanges(graph, payload.SecretChanges)
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	resetStateNodeIDs := make(map[string]bool, len(payload.ResetStateNodeInstanceIDs))
	for _, rawNodeID := range payload.ResetStateNodeInstanceIDs {
		nodeID := strings.TrimSpace(rawNodeID)
		if !workflowNodeIDPattern.MatchString(nodeID) {
			return WorkflowRevisionView{}, errors.New("resetStateNodeInstanceIds contains an invalid nodeInstanceId")
		}
		if resetStateNodeIDs[nodeID] {
			return WorkflowRevisionView{}, fmt.Errorf("duplicate state reset for node %q", nodeID)
		}
		resetStateNodeIDs[nodeID] = true
	}

	var revision db.WorkflowRevision
	err = a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		if workflow.ActiveRevisionID == nil || *workflow.ActiveRevisionID != payload.ExpectedActiveRevisionID {
			return fmt.Errorf("%w: active workflow revision changed", ErrConflict)
		}
		var activeRevision db.WorkflowRevision
		if err := tx.Where("workflow_id = ? AND id = ?", workflowID, *workflow.ActiveRevisionID).First(&activeRevision).Error; err != nil {
			return errors.New("load active workflow revision failed")
		}
		activeGraph, err := a.validateWorkflowGraph(json.RawMessage(activeRevision.GraphJSON))
		if err != nil {
			return errors.New("active workflow revision graph is invalid")
		}
		var states []db.WorkflowNodeState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("workflow_id = ?", workflowID).Find(&states).Error; err != nil {
			return errors.New("load workflow node states failed")
		}
		requiredStateResets := make(map[string]bool)
		for _, state := range states {
			previous, existed := activeGraph.nodeVersions[state.NodeInstanceID]
			next, remains := graph.nodeVersions[state.NodeInstanceID]
			if !existed || !remains || previous != next {
				requiredStateResets[state.NodeInstanceID] = true
			}
		}
		if len(resetStateNodeIDs) != len(requiredStateResets) {
			return workflowStateResetConflict(requiredStateResets)
		}
		for nodeID := range resetStateNodeIDs {
			if !requiredStateResets[nodeID] {
				return workflowStateResetConflict(requiredStateResets)
			}
		}
		if len(requiredStateResets) > 0 && workflow.Status != WorkflowStatusInactive {
			return fmt.Errorf("%w: workflow must be inactive before resetting node state", ErrConflict)
		}
		if len(requiredStateResets) > 0 {
			nodeIDs := make([]string, 0, len(requiredStateResets))
			for nodeID := range requiredStateResets {
				nodeIDs = append(nodeIDs, nodeID)
			}
			if err := tx.Where("workflow_id = ? AND node_instance_id IN ?", workflowID, nodeIDs).
				Delete(&db.WorkflowNodeState{}).Error; err != nil {
				return errors.New("reset workflow node states failed")
			}
		}
		var latest int64
		if err := tx.Model(&db.WorkflowRevision{}).Where("workflow_id = ?", workflowID).
			Select("COALESCE(MAX(revision_number), 0)").Scan(&latest).Error; err != nil {
			return errors.New("read latest workflow revision failed")
		}
		now := time.Now().UTC()
		revision = db.WorkflowRevision{
			WorkflowID: workflowID, RevisionNumber: latest + 1, GraphJSON: graph.graphJSON,
			NodeVersions: graph.nodeVersionsJSON, MainTriggerNodeID: graph.mainTriggerID,
			CreatedBy: principal.User.ID, CreatedAt: now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return errors.New("create workflow revision failed")
		}
		if err := a.persistWorkflowSecrets(tx, workflowID, *workflow.ActiveRevisionID, revision, activeGraph, graph, secretChanges, now); err != nil {
			return err
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(map[string]any{
			"active_revision_id": revision.ID, "main_trigger_node_id": graph.mainTriggerID,
			"mode": workflowModeForTrigger(graph.nodes[graph.mainTriggerID].NodeType), "updated_at": now,
		}).Error; err != nil {
			return errors.New("activate workflow revision failed")
		}
		if workflow.Status == WorkflowStatusActive {
			nextScheduledAt := any(nil)
			trigger := graph.nodes[graph.mainTriggerID]
			if trigger.NodeType == "core.schedule" {
				next, err := nextWorkflowScheduledAt(trigger.Config, now)
				if err != nil {
					return errors.New("schedule config is invalid")
				}
				nextScheduledAt = next
			}
			if err := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id = ?", workflowID).Updates(map[string]any{
				"next_scheduled_at": nextScheduledAt, "updated_at": now,
			}).Error; err != nil {
				return errors.New("update workflow runtime schedule failed")
			}
		}
		return nil
	})
	if err != nil {
		return WorkflowRevisionView{}, err
	}
	views := []WorkflowRevisionView{workflowRevisionView(revision)}
	if err := a.attachWorkflowRevisionSecrets(ctx, workflowID, views); err != nil {
		return WorkflowRevisionView{}, err
	}
	return views[0], nil
}

func workflowStateResetConflict(required map[string]bool) error {
	nodeIDs := make([]string, 0, len(required))
	for nodeID := range required {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	if len(nodeIDs) == 0 {
		return errors.New("resetStateNodeInstanceIds does not match a destructive state change")
	}
	return fmt.Errorf("%w: destructive edits require resetStateNodeInstanceIds for %s", ErrConflict, strings.Join(nodeIDs, ", "))
}

func (a *App) ListWorkflowRevisions(ctx context.Context, workflowID int64) ([]WorkflowRevisionView, error) {
	var count int64
	if err := a.DB.WithContext(ctx).Model(&db.Workflow{}).Where("id = ?", workflowID).Count(&count).Error; err != nil {
		return nil, errors.New("load workflow failed")
	}
	if count == 0 {
		return nil, fmt.Errorf("%w: workflow", ErrNotFound)
	}
	var revisions []db.WorkflowRevision
	if err := a.DB.WithContext(ctx).Where("workflow_id = ?", workflowID).
		Order("revision_number DESC").Find(&revisions).Error; err != nil {
		return nil, errors.New("list workflow revisions failed")
	}
	items := make([]WorkflowRevisionView, 0, len(revisions))
	for _, revision := range revisions {
		items = append(items, workflowRevisionView(revision))
	}
	if err := a.attachWorkflowRevisionSecrets(ctx, workflowID, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) GetWorkflowRevision(ctx context.Context, workflowID, revisionID int64) (WorkflowRevisionView, error) {
	var revision db.WorkflowRevision
	if err := a.DB.WithContext(ctx).Where("workflow_id = ? AND id = ?", workflowID, revisionID).First(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkflowRevisionView{}, fmt.Errorf("%w: workflow revision", ErrNotFound)
		}
		return WorkflowRevisionView{}, errors.New("load workflow revision failed")
	}
	views := []WorkflowRevisionView{workflowRevisionView(revision)}
	if err := a.attachWorkflowRevisionSecrets(ctx, workflowID, views); err != nil {
		return WorkflowRevisionView{}, err
	}
	return views[0], nil
}

func (a *App) ApplyWorkflowLifecycle(ctx context.Context, workflowID int64, payload WorkflowLifecyclePayload) (WorkflowDetail, error) {
	action := strings.ToLower(strings.TrimSpace(payload.Action))
	err := a.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var workflow db.Workflow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&workflow, workflowID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: workflow", ErrNotFound)
			}
			return errors.New("lock workflow failed")
		}
		next, err := nextWorkflowStatus(workflow.Status, action)
		if err != nil {
			return err
		}
		if action == "activate" && workflow.ActiveRevisionID == nil {
			return fmt.Errorf("%w: workflow is not startable", ErrConflict)
		}
		now := time.Now().UTC()
		updates := map[string]any{"status": next, "updated_at": now}
		var runtimeUpdates map[string]any
		if action == "activate" {
			var revision db.WorkflowRevision
			if err := tx.First(&revision, *workflow.ActiveRevisionID).Error; err != nil {
				return errors.New("load active workflow revision failed")
			}
			validated, err := a.validateWorkflowGraph(json.RawMessage(revision.GraphJSON))
			if err != nil {
				return fmt.Errorf("%w: active workflow revision is invalid", ErrConflict)
			}
			if err := ensureWorkflowRevisionSecrets(tx, workflow.ID, revision.ID, validated); err != nil {
				return err
			}
			runtimeUpdates = map[string]any{"updated_at": now, "next_scheduled_at": nil}
			trigger := validated.nodes[validated.mainTriggerID]
			if trigger.NodeType == "core.schedule" {
				next, err := nextWorkflowScheduledAt(trigger.Config, now)
				if err != nil {
					return fmt.Errorf("%w: schedule config is invalid", ErrConflict)
				}
				runtimeUpdates["next_scheduled_at"] = next
			}
		} else if action == "deactivate" {
			runtimeUpdates = map[string]any{"updated_at": now, "next_scheduled_at": nil}
		}
		if err := tx.Model(&db.Workflow{}).Where("id = ?", workflowID).Updates(updates).Error; err != nil {
			return errors.New("update workflow lifecycle failed")
		}
		if runtimeUpdates != nil {
			if err := tx.Model(&db.WorkflowRuntime{}).Where("workflow_id = ?", workflowID).Updates(runtimeUpdates).Error; err != nil {
				return errors.New("update workflow runtime schedule failed")
			}
		}
		return nil
	})
	if err != nil {
		return WorkflowDetail{}, err
	}
	if action == "deactivate" {
		a.stopWorkflowTrigger(workflowID)
	}
	return a.GetWorkflow(ctx, workflowID)
}

func nextWorkflowStatus(current, action string) (string, error) {
	switch action {
	case "activate":
		if current == WorkflowStatusInactive {
			return WorkflowStatusActive, nil
		}
	case "deactivate":
		if current == WorkflowStatusActive || current == WorkflowStatusError {
			return WorkflowStatusInactive, nil
		}
	default:
		return "", errors.New("lifecycle action must be activate or deactivate")
	}
	return "", fmt.Errorf("%w: cannot %s workflow from %s", ErrConflict, action, current)
}

func workflowView(workflow db.Workflow) WorkflowView {
	activeRevisionID := int64(0)
	if workflow.ActiveRevisionID != nil {
		activeRevisionID = *workflow.ActiveRevisionID
	}
	view := WorkflowView{
		ID: workflow.ID, Name: workflow.Name, Description: workflow.Description, Mode: workflow.Mode,
		Status: workflow.Status, ActiveRevisionID: activeRevisionID,
		MainTriggerNodeID: workflow.MainTriggerNodeID, RetentionDays: workflow.RetentionDays,
		CreatedBy: workflow.CreatedBy, CreatedAt: formatWorkflowTime(workflow.CreatedAt),
		UpdatedAt: formatWorkflowTime(workflow.UpdatedAt),
	}
	return view
}

func workflowRevisionView(revision db.WorkflowRevision) WorkflowRevisionView {
	return WorkflowRevisionView{
		ID: revision.ID, WorkflowID: revision.WorkflowID, RevisionNumber: revision.RevisionNumber,
		Graph: json.RawMessage(revision.GraphJSON), NodeVersions: json.RawMessage(revision.NodeVersions),
		MainTriggerNodeID: revision.MainTriggerNodeID, CreatedBy: revision.CreatedBy,
		CreatedAt: formatWorkflowTime(revision.CreatedAt), SecretFields: map[string]map[string]bool{},
	}
}

func formatWorkflowTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func validWorkflowStatus(status string) bool {
	return status == WorkflowStatusInactive || status == WorkflowStatusActive || status == WorkflowStatusError
}

func workflowModeForTrigger(nodeType string) string {
	switch nodeType {
	case "core.manual", "core.schedule", "official.connector.webhook":
		return WorkflowModeBatch
	case "core.event":
		return WorkflowModeEvent
	default:
		return WorkflowModeStream
	}
}
