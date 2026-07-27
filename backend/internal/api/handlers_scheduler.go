package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"coinsphere/backend/internal/service"
)

// ---------- 调度中心 ----------

func (s *Server) handleSchedulerOverview(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	data, err := s.App.GetSchedulerOverview()
	respond(w, data, err, "")
}

func (s *Server) handleListTaskDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListTaskDefinitions())
}

func (s *Server) handleListTaskDefinitionPage(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	current := queryInt(r, "current", 1)
	size := clampSize(queryInt(r, "size", 10), 100)
	ok(w, s.App.ListTaskDefinitionPage(current, size, queryStr(r, "keyword")))
}

func (s *Server) handleUpdateTaskDefaultParams(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	taskCode := r.PathValue("taskCode")
	payload, err := decodeBody[struct {
		Params M `json:"params"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	data, err := s.App.UpdateTaskDefinitionDefaultParams(taskCode, payload.Params, principal.User.ID)
	respond(w, data, err, "任务定义参数已更新")
}

func (s *Server) handleListNodeDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListNodeDefinitions())
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

func (s *Server) handleGetWorkflowDefinition(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
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
	definition, err := s.App.GetWorkflowDefinition(definitionID)
	if err != nil {
		fail(w, err.Error())
		return
	}
	code, _ := definition["code"].(string)
	data, err := s.App.DeactivateWorkflowCode(code)
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
		fail(w, err.Error())
		return
	}
	payload, err := decodeBody[struct {
		StartEntryKeys []string `json:"startEntryKeys"`
		Inputs         M        `json:"inputs"`
	}](r)
	if err != nil {
		fail(w, err.Error())
		return
	}
	executions, err := s.App.RunManualStarts(definitionID, payload.StartEntryKeys, principal.User.ID, payload.Inputs)
	if err != nil {
		if service.IsBacklogExceeded(err) {
			writeJSON(w, http.StatusTooManyRequests, M{"code": 429, "msg": err.Error(), "data": nil})
			return
		}
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, M{
		"code": 200, "msg": "工作流已加入执行队列",
		"data": M{"executions": executions},
	})
}

func (s *Server) handleListDefinitionExecutions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	definitionID, err := pathInt64(r, "definitionId")
	if err != nil {
		fail(w, err.Error())
		return
	}
	query := service.WorkflowExecutionQuery{
		Current:      queryInt(r, "current", 1),
		Size:         clampSize(queryInt(r, "size", 10), 100),
		Keyword:      queryStr(r, "keyword"),
		TriggerType:  queryStr(r, "triggerType"),
		Status:       queryStr(r, "status"),
		DefinitionID: &definitionID,
	}
	data, err := s.App.ListExecutions(query)
	respond(w, data, err, "")
}

func (s *Server) handleListAllExecutions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	query := service.WorkflowExecutionQuery{
		Current:                queryInt(r, "current", 1),
		Size:                   clampSize(queryInt(r, "size", 10), 100),
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

func (s *Server) handleWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	var payload M
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			payload = M{}
		}
	}
	if payload == nil {
		payload = M{}
	}
	secret := strings.TrimSpace(r.Header.Get("X-Workflow-Secret"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	data, err := s.App.TriggerWebhook(r.PathValue("workflowCode"), r.PathValue("entryKey"), secret, payload, idempotencyKey)
	if err != nil {
		if service.IsBacklogExceeded(err) {
			writeJSON(w, http.StatusTooManyRequests, M{"code": 429, "msg": err.Error(), "data": nil})
			return
		}
		fail(w, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, M{"code": 200, "msg": "Webhook 已加入执行队列", "data": data})
}
