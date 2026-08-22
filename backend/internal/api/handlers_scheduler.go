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
func (s *Server) handleWorkbench(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetWorkbench(r.Context(), principal)
	respond(w, data, err, "")
}

func (s *Server) handleListNodeDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListNodeDefinitions(principal))
}

// handleListWorkflowAgentOptions 工作流编辑器里 assistant.agent 节点的智能体下拉选项。
func (s *Server) handleListWorkflowAgentOptions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListWorkflowAgentOptions())
}

func (s *Server) handleListWorkflowDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListWorkflowDefinitions(principal.User.ID)
	respond(w, data, err, "")
}

func (s *Server) handleValidateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.WorkflowDefinitionUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	ok(w, s.App.ValidateWorkflowDefinitionForPrincipal(*payload, principal))
}

func (s *Server) handleCreateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.WorkflowDefinitionUpsertPayload](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.CreateWorkflowDefinitionForPrincipal(*payload, principal)
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
	data, err := s.App.GetWorkflowDefinition(definitionID, principal.User.ID)
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
	data, err := s.App.UpdateWorkflowDefinitionForPrincipal(definitionID, *payload, principal)
	respond(w, data, err, "工作流定义已更新")
}

func (s *Server) handleDeleteWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	respond(w, nil, s.App.DeleteWorkflowDefinition(definitionID, principal.User.ID), "工作流定义版本已删除")
}

func (s *Server) handleActivateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.ActivateDefinitionForPrincipal(definitionID, principal)
	respond(w, data, err, "工作流版本已激活")
}

func (s *Server) handleDeactivateWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.DeactivateDefinition(definitionID, principal.User.ID)
	respond(w, data, err, "工作流已取消激活")
}

func (s *Server) handleGetWorkflowRuntime(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.GetRuntimeByDefinition(definitionID, principal.User.ID)
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
	data, err := s.App.SetEntryEnabled(definitionID, principal.User.ID, r.PathValue("entryKey"), payload.IsEnabled)
	respond(w, data, err, "入口状态已更新")
}

func (s *Server) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.RotateWebhookSecret(definitionID, principal.User.ID, r.PathValue("entryKey"))
	respond(w, data, err, "Webhook secret 已轮换")
}

func (s *Server) handleCreateWorkflowExecution(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[struct {
		WorkflowDefinitionID int64    `json:"workflowDefinitionId"`
		StartEntryKeys       []string `json:"startEntryKeys"`
		Inputs               M        `json:"inputs"`
	}](r)
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	executions, err := s.App.RunManualStarts(
		payload.WorkflowDefinitionID, payload.StartEntryKeys, principal.User.ID, payload.Inputs, r.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, M{"code": 200, "msg": "工作流已加入执行队列", "data": M{"executions": executions}})
}

func writeWorkflowProblem(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, service.ErrBacklogExceeded) {
		status = http.StatusTooManyRequests
	} else if service.IsIdempotencyConflict(err) || errors.Is(err, service.ErrWorkflowActionConflict) {
		status = http.StatusConflict
	} else if errors.Is(err, service.ErrWorkflowActionReauthentication) ||
		errors.Is(err, service.ErrTradingReauthentication) || errors.Is(err, service.ErrStrategySignalReauthentication) {
		status = http.StatusUnauthorized
	} else if errors.Is(err, service.ErrPermission) {
		status = http.StatusForbidden
	} else if errors.Is(err, service.ErrNotFound) {
		status = http.StatusNotFound
	}
	detail := err.Error()
	if status == http.StatusNotFound {
		detail = "resource not found"
	}
	writeProblem(w, r, status, detail)
}

func (s *Server) handleListWorkflowActions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.ListWorkflowActions(principal.User.ID, queryStr(r, "status"))
	respond(w, data, err, "")
}

func (s *Server) handleGetWorkflowAction(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetWorkflowAction(r.PathValue("actionId"), principal.User.ID)
	respond(w, data, err, "")
}

func (s *Server) handleDecideWorkflowAction(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	payload, err := decodeBody[service.WorkflowActionDecision](r)
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	data, err := s.App.DecideWorkflowAction(
		r.Context(), r.PathValue("actionId"), principal, *payload,
		r.Header.Get("Idempotency-Key"), r.Header.Get("X-Reauth-Token"),
	)
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	okMsg(w, data, "待办已处理")
}

func (s *Server) handleListAllExecutions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	definitionID := queryInt64Ptr(r, "workflowDefinitionId")
	if queryStr(r, "workflowDefinitionId") != "" && definitionID == nil {
		fail(w, "workflowDefinitionId 必须是正整数")
		return
	}
	query := service.WorkflowExecutionQuery{
		OwnerUserID:            principal.User.ID,
		Page:                   page,
		DefinitionID:           definitionID,
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
	data, err := s.App.GetExecutionDetail(executionID, principal.User.ID)
	respond(w, data, err, "")
}

func (s *Server) handleCancelExecution(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	executionID, err := pathInt64(r, "executionId")
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	data, err := s.App.CancelExecution(executionID, principal.User.ID)
	respond(w, data, err, "取消请求已提交")
}

func (s *Server) handleRerunExecution(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	executionID, err := pathInt64(r, "executionId")
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	data, err := s.App.RerunExecution(executionID, principal.User.ID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeWorkflowProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, M{"code": 200, "msg": "工作流已重新加入队列", "data": data})
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
