package api

import (
	"errors"
	"net/http"
	"strings"

	"coinsphere/backend/internal/service"
)

// ---------- 调度中心 ----------

// 本文件都是"处理函数"。它们的固定签名 (w, r, principal):w 写响应、r 读请求、principal 是鉴权中间件传进来的当前用户。
// (s *Server) 是方法接收者,表示这是 Server 的方法(见 GO入门笔记『方法与接收者』)。套路都一样:调 s.App 里的业务方法,再用 respond 统一返回。
func (s *Server) handleSchedulerOverview(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	// 多返回值:data 是结果,err 是错误;一起交给 respond 判断成败(见 api.go 的 respond)。
	data, err := s.App.GetSchedulerOverview()
	respond(w, data, err, "")
}

func (s *Server) handleListNodeDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListNodeDefinitions())
}

// handleListWorkflowAgentOptions 工作流编辑器里 assistant.agent 节点的智能体下拉选项。
func (s *Server) handleListWorkflowAgentOptions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListWorkflowAgentOptions())
}

func (s *Server) handleListWorkflowDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListWorkflowDefinitions()
	respond(w, data, err, "")
}

func (s *Server) handleValidateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.WorkflowDefinitionUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w, s.App.ValidateWorkflowDefinition(*payload))
}

func (s *Server) handleCreateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.WorkflowDefinitionUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateWorkflowDefinition(*payload, principal.User.ID)
	respond(w, data, err, "工作流定义已创建")
}

// handleGetWorkflowDefinition 处理 GET .../workflow-definitions/{definitionId}:按 ID 取一个工作流定义。
func (s *Server) handleGetWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	// pathInt64 取路径参数 {definitionId} 并转成整数;不合法就直接回错。多数带 {id} 的接口开头都长这样。
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.GetWorkflowDefinition(definitionID)
	respond(w, data, err, "")
}

func (s *Server) handleUpdateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[service.WorkflowDefinitionUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateWorkflowDefinition(definitionID, *payload, principal.User.ID)
	respond(w, data, err, "工作流定义已更新")
}

func (s *Server) handleDeleteWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteWorkflowDefinition(definitionID), "工作流定义版本已删除")
}

func (s *Server) handleActivateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.ActivateDefinition(definitionID, principal.User.ID)
	respond(w, data, err, "工作流版本已激活")
}

func (s *Server) handleDeactivateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.DeactivateDefinition(definitionID)
	respond(w, data, err, "工作流已取消激活")
}

func (s *Server) handleGetWorkflowRuntime(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.GetRuntimeByDefinition(definitionID)
	respond(w, data, err, "")
}

func (s *Server) handlePatchRuntimeEntry(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[struct {
		IsEnabled bool `json:"isEnabled"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.SetEntryEnabled(definitionID, r.PathValue("entryKey"), payload.IsEnabled)
	respond(w, data, err, "入口状态已更新")
}

func (s *Server) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.RotateWebhookSecret(definitionID, r.PathValue("entryKey"))
	respond(w, data, err, "Webhook secret 已轮换")
}

func (s *Server) handleRunWorkflowStarts(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	// []string 是字符串切片(可变长数组,见 GO入门笔记『复合类型』);Inputs 用 M 承接任意 JSON 对象。
	payload, err := decodeBody[struct {
		StartEntryKeys []string `json:"startEntryKeys"`
		Inputs         M        `json:"inputs"`
	}](r)
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	executions, err := s.App.RunManualStarts(definitionID, payload.StartEntryKeys, principal.User.ID, payload.Inputs, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, M{
		"code": 200, "msg": "工作流已加入执行队列",
		"data": M{"executions": executions},
	})
}

func writeWorkflowProblem(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, service.ErrBacklogExceeded) {
		status = http.StatusTooManyRequests
	} else if service.IsIdempotencyConflict(err) {
		status = http.StatusConflict
	}
	writeProblem(w, r, status, err.Error())
}

func (s *Server) handleListDefinitionExecutions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	query := service.WorkflowExecutionQuery{
		Page:         page,
		Keyword:      queryStr(r, "keyword"),
		TriggerType:  queryStr(r, "triggerType"),
		Status:       queryStr(r, "status"),
		DefinitionID: &definitionID,
	}
	data, err := s.App.ListExecutions(query)
	respond(w, data, err, "")
}

func (s *Server) handleListAllExecutions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	query := service.WorkflowExecutionQuery{
		Page:                   page,
		WorkflowDefinitionCode: queryStr(r, "workflowDefinitionCode"),
		Keyword:                queryStr(r, "keyword"),
		TriggerType:            queryStr(r, "triggerType"),
		Status:                 queryStr(r, "status"),
	}
	data, err := s.App.ListExecutions(query)
	respond(w, data, err, "")
}

func (s *Server) handleGetExecutionDetail(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	executionID, err := pathInt64(r, "executionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.GetExecutionDetail(executionID)
	respond(w, data, err, "")
}

// handleWorkflowCatchAll dispatches the one ambiguous execution-detail shape
// that cannot be expressed alongside numeric-looking definition wildcards in
// Go's ServeMux pattern lattice.
func (s *Server) handleWorkflowCatchAll(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	path := r.PathValue("workflowPath")
	if strings.HasPrefix(path, "executions/") && !strings.Contains(strings.TrimPrefix(path, "executions/"), "/") {
		r.SetPathValue("executionId", strings.TrimPrefix(path, "executions/"))
		s.handleGetExecutionDetail(w, r, principal)
		return
	}
	writeProblem(w, r, http.StatusNotFound, "workflow resource was not found")
}

// handleWebhookTrigger 处理已通过登录鉴权、并额外携带工作流 secret 的 webhook 回调。
func (s *Server) handleWebhookTrigger(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[M](r)
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	if *payload == nil {
		*payload = M{}
	}
	secret := strings.TrimSpace(r.Header.Get("X-Workflow-Secret"))
	data, err := s.App.TriggerWebhook(principal.User.ID, r.PathValue("workflowCode"), r.PathValue("entryKey"), secret, *payload, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, M{"code": 200, "msg": "Webhook 已加入执行队列", "data": data})
}
