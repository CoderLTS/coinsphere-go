package api

import (
	"encoding/json"
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

func (s *Server) handleListTaskDefinitions(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	ok(w, s.App.ListTaskDefinitions())
}

// handleListTaskDefinitionPage 分页查询任务定义:current 是页码(默认 1),size 是每页条数(默认 10,上限 100)。
func (s *Server) handleListTaskDefinitionPage(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	current := queryInt(r, "current", 1)
	size := clampSize(queryInt(r, "size", 10), 100)
	ok(w, s.App.ListTaskDefinitionPage(current, size, queryStr(r, "keyword")))
}

// handleUpdateTaskDefaultParams 处理 PUT .../task-definitions/{taskCode}/default-params:更新某任务的默认参数。
func (s *Server) handleUpdateTaskDefaultParams(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	// r.PathValue("taskCode") 取路径参数 {taskCode}。
	taskCode := r.PathValue("taskCode")
	// decodeBody[T] 把请求体 JSON 解析成 T。这里 T 是"匿名 struct":只用一次、不值得单独命名的临时结构。
	// 字段后的 `json:"params"` 是 struct 标签,告诉 JSON 库该字段对应 JSON 里的 "params" 键(见 GO入门笔记『复合类型』)。
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
	definition, err := s.App.GetWorkflowDefinition(definitionID)
	if err != nil {
		fail(w, err.Error())
		return
	}
	// definition 是 map,definition["code"] 取出的是 any;.(string) 是"类型断言",把它当 string 取出,第二个返回值(用 _ 丢弃)表示是否成功。
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
	// []string 是字符串切片(可变长数组,见 GO入门笔记『复合类型』);Inputs 用 M 承接任意 JSON 对象。
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
		// 队列积压过多时返回 429(Too Many Requests),提示前端稍后再试,而不是当成普通业务错误。
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

// handleWebhookTrigger 处理外部系统回调的 webhook。注意签名只有 (w, r) 没有 principal —— 它不走登录鉴权,靠请求头里的 secret 校验。
func (s *Server) handleWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	// var payload M 声明一个 map 变量(零值为 nil);下面尽力解析请求体,解析失败就退回空对象,不让整个请求挂掉。
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
