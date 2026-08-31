package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/internal/service"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gin-gonic/gin"
)

const maxWorkflowRequestBytes = 2 << 20
const maxWorkflowEventRequestBytes = 1 << 20
const maxWorkflowHumanDecisionBytes = 64 << 10

func (s *Server) handlePublishWorkflowWebhook(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	secret, secretOK := singleWorkflowSecretHeader(c.Request)
	eventID, eventIDOK := singleWorkflowHeader(c.Request, "Idempotency-Key")
	partitionKey, partitionOK := singleWorkflowHeader(c.Request, "X-CoinSphere-Partition-Key")
	if !secretOK || !eventIDOK || !partitionOK {
		writeProblem(c, http.StatusBadRequest, "webhook requires one secret, idempotency key, and partition key header")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowEventRequestBytes)
	payload, err := decodeBody[map[string]any](c)
	if err != nil || payload == nil {
		writeProblem(c, http.StatusBadRequest, "webhook body must be a JSON object no larger than 1 MiB")
		return
	}
	data, err := s.App.PublishWorkflowWebhook(c.Request.Context(), workflowID, secret, eventID, partitionKey, *payload)
	respond(c, data, err, "")
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

func (s *Server) handlePublishWorkflowEvent(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	raw, err := decodeBody[json.RawMessage](c)
	if err != nil || len(*raw) > 1<<20 {
		writeProblem(c, http.StatusBadRequest, "CloudEvent must be a JSON object no larger than 1 MiB")
		return
	}
	var event cloudevents.Event
	if json.Unmarshal(*raw, &event) != nil {
		writeProblem(c, http.StatusBadRequest, "CloudEvent is invalid")
		return
	}
	data, err := s.App.PublishWorkflowEvent(c.Request.Context(), event)
	respond(c, data, err, "")
}

func (s *Server) handleListWorkflowHumanTasks(c *gin.Context) {
	data, err := s.App.ListWorkflowHumanTasks(c.Request.Context(), queryStr(c, "status"))
	respond(c, M{"items": data}, err, "")
}

func (s *Server) handleDecideWorkflowHumanTask(c *gin.Context) {
	taskID, err := pathInt64(c, "taskId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowHumanDecisionBytes)
	payload, err := decodeBody[service.WorkflowHumanTaskDecision](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.DecideWorkflowHumanTask(c.Request.Context(), taskID, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleListWorkflowTemplates(c *gin.Context) {
	ok(c, M{"items": s.App.ListWorkflowTemplates()})
}

func (s *Server) handleListWorkflowNodeDefinitions(c *gin.Context) {
	ok(c, M{"items": s.App.ListWorkflowNodeDefinitions()})
}

func (s *Server) handleValidateWorkflowGraph(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[struct {
		Graph json.RawMessage `json:"graph"`
	}](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.App.ValidateWorkflowGraph(payload.Graph); err != nil {
		ok(c, M{"valid": false, "issues": []M{{"scope": "graph", "level": "error", "message": err.Error()}}})
		return
	}
	ok(c, M{"valid": true, "issues": []M{}})
}

func (s *Server) handleListWorkflows(c *gin.Context) {
	items, err := s.App.ListWorkflows(c.Request.Context(), queryStr(c, "status"))
	respond(c, M{"items": items}, err, "")
}

func (s *Server) handleCreateWorkflow(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowCreatePayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.CreateWorkflow(c.Request.Context(), *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleGetWorkflow(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflow(c.Request.Context(), workflowID)
	respond(c, data, err, "")
}

func (s *Server) handleUpdateWorkflow(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowUpdatePayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.UpdateWorkflow(c.Request.Context(), workflowID, *payload)
	respond(c, data, err, "")
}

func (s *Server) handleSaveWorkflowRevision(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowRevisionSavePayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.SaveWorkflowRevision(c.Request.Context(), workflowID, *payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleListWorkflowRevisions(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.App.ListWorkflowRevisions(c.Request.Context(), workflowID)
	respond(c, M{"items": items}, err, "")
}

func (s *Server) handleGetWorkflowRevision(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	revisionID, err := pathInt64(c, "revisionId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflowRevision(c.Request.Context(), workflowID, revisionID)
	respond(c, data, err, "")
}

func (s *Server) handleWorkflowLifecycle(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowLifecyclePayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.ApplyWorkflowLifecycle(c.Request.Context(), workflowID, *payload)
	respond(c, data, err, "")
}

func (s *Server) handleListWorkflowRuns(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	page, ok := cursorPage(c)
	if !ok {
		return
	}
	triggerType := queryStr(c, "triggerType")
	if triggerType != "" && triggerType != "manual" && triggerType != "schedule" && triggerType != "event" &&
		triggerType != "stream" && triggerType != "webhook" && triggerType != "failure" {
		writeProblem(c, http.StatusBadRequest, "invalid workflow run triggerType")
		return
	}
	status := queryStr(c, "status")
	statusMap := map[string]string{
		"queued": "queued", "running": "running", "waiting": "waiting", "retrying": "retrying",
		"retry_waiting": "retrying", "success": "succeeded", "succeeded": "succeeded",
		"failed": "failed", "canceled": "cancelled", "cancelled": "cancelled",
	}
	if status != "" {
		mapped, valid := statusMap[status]
		if !valid {
			writeProblem(c, http.StatusBadRequest, "invalid workflow run status")
			return
		}
		status = mapped
	}
	from, err := workflowRunQueryTime(c, "from")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	to, err := workflowRunQueryTime(c, "to")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	if from != nil && to != nil && from.After(*to) {
		writeProblem(c, http.StatusBadRequest, "from must not be after to")
		return
	}
	keyword := queryStr(c, "keyword")
	if len(keyword) > 200 {
		writeProblem(c, http.StatusBadRequest, "keyword must not exceed 200 bytes")
		return
	}
	data, err := s.App.PageWorkflowRuns(c.Request.Context(), workflowID, service.WorkflowRunListQuery{
		Page: page, TriggerType: triggerType, Status: status, From: from, To: to, Keyword: keyword,
	})
	respond(c, data, err, "")
}

func (s *Server) handleCreateWorkflowRun(c *gin.Context) {
	workflowID, err := pathInt64(c, "workflowId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload := service.WorkflowRunCreatePayload{}
	if c.Request.ContentLength != 0 {
		decoded, decodeErr := decodeBody[service.WorkflowRunCreatePayload](c)
		if decodeErr != nil {
			writeProblem(c, http.StatusBadRequest, decodeErr.Error())
			return
		}
		payload = *decoded
	}
	data, err := s.App.CreateWorkflowRun(c.Request.Context(), workflowID, payload, currentPrincipal(c))
	respond(c, data, err, "")
}

func (s *Server) handleGetWorkflowRun(c *gin.Context) {
	runID, err := pathInt64(c, "runId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.GetWorkflowRunDetail(c.Request.Context(), runID)
	respond(c, data, err, "")
}

func (s *Server) handleGetWorkflowArtifactManifest(c *gin.Context) {
	data, err := s.App.GetWorkflowArtifactManifest(c.Request.Context(), c.Param("sha256"), true)
	respond(c, data, err, "")
}

func workflowRunQueryTime(c *gin.Context, name string) (*time.Time, error) {
	raw := queryStr(c, name)
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 UTC time", name)
	}
	_, offset := value.Zone()
	if offset != 0 {
		return nil, fmt.Errorf("%s must be UTC", name)
	}
	value = value.UTC()
	return &value, nil
}

func (s *Server) handleDownloadWorkflowArtifact(c *gin.Context) {
	reader, artifact, err := s.App.OpenWorkflowArtifact(c.Request.Context(), c.Param("sha256"))
	if err != nil {
		respond(c, nil, err, "")
		return
	}
	defer reader.Close()
	c.Header("Content-Type", artifact.MediaType)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", artifact.SHA256))
	c.Header("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	c.Header("ETag", fmt.Sprintf("\"%s\"", artifact.SHA256))
	c.Header("X-Content-SHA256", artifact.SHA256)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

func (s *Server) handleWorkflowRunAction(c *gin.Context) {
	runID, err := pathInt64(c, "runId")
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	payload, err := decodeBody[service.WorkflowRunActionPayload](c)
	if err != nil {
		writeProblem(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := s.App.ApplyWorkflowRunAction(c.Request.Context(), runID, *payload)
	respond(c, data, err, "")
}
