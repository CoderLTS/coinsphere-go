package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"coinsphere/backend/internal/service"
	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const maxWorkflowRequestBytes = 2 << 20
const maxWorkflowEventRequestBytes = 1 << 20
const maxWorkflowHumanDecisionBytes = 64 << 10

func (s *Server) handlePublishWorkflowWebhook(w http.ResponseWriter, r *http.Request) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	secret, secretOK := singleWorkflowSecretHeader(r)
	eventID, eventIDOK := singleWorkflowHeader(r, "Idempotency-Key")
	partitionKey, partitionOK := singleWorkflowHeader(r, "X-CoinSphere-Partition-Key")
	if !secretOK || !eventIDOK || !partitionOK {
		writeProblem(w, r, http.StatusBadRequest, "webhook requires one secret, idempotency key, and partition key header")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowEventRequestBytes)
	payload, err := decodeBody[map[string]any](r)
	if err != nil || payload == nil {
		writeProblem(w, r, http.StatusBadRequest, "webhook body must be a JSON object no larger than 1 MiB")
		return
	}
	data, err := s.App.PublishWorkflowWebhook(r.Context(), workflowID, secret, eventID, partitionKey, *payload)
	respond(w, data, err, "")
}

func singleWorkflowHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

func singleWorkflowSecretHeader(r *http.Request) (string, bool) {
	values := r.Header.Values("X-CoinSphere-Webhook-Secret")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false
	}
	return values[0], true
}

func (s *Server) handlePublishWorkflowEvent(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := decodeBody[json.RawMessage](r)
	if err != nil || len(*raw) > 1<<20 {
		writeProblem(w, r, http.StatusBadRequest, "CloudEvent must be a JSON object no larger than 1 MiB")
		return
	}
	var event cloudevents.Event
	if json.Unmarshal(*raw, &event) != nil {
		writeProblem(w, r, http.StatusBadRequest, "CloudEvent is invalid")
		return
	}
	data, err := s.App.PublishWorkflowEvent(r.Context(), event)
	respond(w, data, err, "")
}

func (s *Server) handleListWorkflowHumanTasks(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.ListWorkflowHumanTasks(r.Context(), queryStr(r, "status"))
	respond(w, M{"items": data}, err, "")
}

func (s *Server) handleDecideWorkflowHumanTask(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	taskID, err := pathInt64(r, "taskId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowHumanDecisionBytes)
	payload, err := decodeBody[service.WorkflowHumanTaskDecision](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.DecideWorkflowHumanTask(r.Context(), taskID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleListWorkflowTemplates(w http.ResponseWriter, _ *http.Request, _ *service.Principal) {
	ok(w, M{"items": s.App.ListWorkflowTemplates()})
}

func (s *Server) handleListWorkflowNodeDefinitions(w http.ResponseWriter, _ *http.Request, _ *service.Principal) {
	ok(w, M{"items": s.App.ListWorkflowNodeDefinitions()})
}

func (s *Server) handleValidateWorkflowGraph(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[struct {
		Graph json.RawMessage `json:"graph"`
	}](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.App.ValidateWorkflowGraph(payload.Graph); err != nil {
		ok(w, M{"valid": false, "issues": []M{{"scope": "graph", "level": "error", "message": err.Error()}}})
		return
	}
	ok(w, M{"valid": true, "issues": []M{}})
}

func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	items, err := s.App.ListWorkflows(r.Context(), queryStr(r, "status"))
	respond(w, M{"items": items}, err, "")
}

func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowCreatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.CreateWorkflow(r.Context(), *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflow(r.Context(), workflowID)
	respond(w, data, err, "")
}

func (s *Server) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowUpdatePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.UpdateWorkflow(r.Context(), workflowID, *payload)
	respond(w, data, err, "")
}

func (s *Server) handleSaveWorkflowRevision(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowRevisionSavePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.SaveWorkflowRevision(r.Context(), workflowID, *payload, principal)
	respond(w, data, err, "")
}

func (s *Server) handleListWorkflowRevisions(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.App.ListWorkflowRevisions(r.Context(), workflowID)
	respond(w, M{"items": items}, err, "")
}

func (s *Server) handleGetWorkflowRevision(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	revisionID, err := pathInt64(r, "revisionId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflowRevision(r.Context(), workflowID, revisionID)
	respond(w, data, err, "")
}

func (s *Server) handleWorkflowLifecycle(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowLifecyclePayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.ApplyWorkflowLifecycle(r.Context(), workflowID, *payload)
	respond(w, data, err, "")
}

func (s *Server) handleListWorkflowBatches(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	page, ok := cursorPage(w, r)
	if !ok {
		return
	}
	triggerType := queryStr(r, "triggerType")
	if triggerType != "" && triggerType != "manual" && triggerType != "schedule" && triggerType != "event" &&
		triggerType != "stream" && triggerType != "webhook" && triggerType != "failure" {
		writeProblem(w, r, http.StatusBadRequest, "invalid workflow batch triggerType")
		return
	}
	status := queryStr(r, "status")
	statusMap := map[string]string{
		"queued": "queued", "running": "running", "waiting": "waiting", "retrying": "retrying",
		"retry_waiting": "retrying", "success": "succeeded", "succeeded": "succeeded",
		"failed": "failed", "canceled": "cancelled", "cancelled": "cancelled",
	}
	if status != "" {
		mapped, valid := statusMap[status]
		if !valid {
			writeProblem(w, r, http.StatusBadRequest, "invalid workflow batch status")
			return
		}
		status = mapped
	}
	data, err := s.App.PageWorkflowBatches(r.Context(), workflowID, service.WorkflowBatchListQuery{
		Page: page, TriggerType: triggerType, Status: status,
	})
	respond(w, data, err, "")
}

func (s *Server) handleCreateWorkflowBatch(w http.ResponseWriter, r *http.Request, principal *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.CreateWorkflowBatch(r.Context(), workflowID, principal)
	respond(w, data, err, "")
}

func (s *Server) handleGetWorkflowBatch(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	batchID, err := pathInt64(r, "batchId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflowBatchDetail(r.Context(), batchID)
	respond(w, data, err, "")
}

func (s *Server) handleListWorkflowActivity(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	workflowID, err := pathInt64(r, "workflowId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	after := int64(0)
	if raw := queryStr(r, "after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			writeProblem(w, r, http.StatusBadRequest, "after must be a non-negative activity cursor")
			return
		}
	}
	limit := 100
	if raw := queryStr(r, "limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			writeProblem(w, r, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
	}
	items, next, err := s.App.ListWorkflowActivities(r.Context(), workflowID, after, limit)
	respond(w, M{"items": items, "nextCursor": next}, err, "")
}

func (s *Server) handleGetWorkflowArtifactManifest(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	data, err := s.App.GetWorkflowArtifactManifest(r.Context(), r.PathValue("sha256"), true)
	respond(w, data, err, "")
}

func (s *Server) handleDownloadWorkflowArtifact(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	reader, artifact, err := s.App.OpenWorkflowArtifact(r.Context(), r.PathValue("sha256"))
	if err != nil {
		respond(w, nil, err, "")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", artifact.MediaType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", artifact.SHA256))
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	w.Header().Set("ETag", fmt.Sprintf("\"%s\"", artifact.SHA256))
	w.Header().Set("X-Content-SHA256", artifact.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func (s *Server) handleWorkflowBatchAction(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	batchID, err := pathInt64(r, "batchId")
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowBatchActionPayload](r)
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.ApplyWorkflowBatchAction(r.Context(), batchID, *payload)
	respond(w, data, err, "")
}
