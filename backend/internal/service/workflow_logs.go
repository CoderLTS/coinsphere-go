package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

const (
	maxWorkflowLogMessageRunes = 1000
	maxWorkflowLogFieldsBytes  = 3500
)

type workflowNodeLogHandler struct {
	app        *App
	next       slog.Handler
	workflowID int64
	runID      int64
	runNodeID  int64
	attrs      []slog.Attr
	groups     []string
}

func (h *workflowNodeLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *workflowNodeLogHandler) Handle(ctx context.Context, record slog.Record) error {
	fields := make(map[string]any)
	for _, attr := range h.attrs {
		addWorkflowLogAttr(fields, strings.Join(h.groups, "."), attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addWorkflowLogAttr(fields, strings.Join(h.groups, "."), attr)
		return true
	})
	logErr := appendWorkflowNodeLog(h.app.DB.WithContext(context.WithoutCancel(ctx)), db.WorkflowNodeLog{
		WorkflowID: h.workflowID,
		RunID:      h.runID,
		RunNodeID:  h.runNodeID,
		LoggedAt:   record.Time.UTC(),
		Level:      workflowLogLevel(record.Level),
		Message:    workflowLogMessage(record.Message),
		FieldsJSON: workflowLogFields(fields),
	})
	if h.next != nil && h.next.Enabled(ctx, record.Level) {
		logErr = errors.Join(logErr, h.next.Handle(ctx, record))
	}
	return logErr
}

func (h *workflowNodeLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	if h.next != nil {
		clone.next = h.next.WithAttrs(attrs)
	}
	return &clone
}

func (h *workflowNodeLogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	if name = strings.TrimSpace(name); name != "" {
		clone.groups = append(append([]string(nil), h.groups...), name)
	}
	if h.next != nil {
		clone.next = h.next.WithGroup(name)
	}
	return &clone
}

func (a *App) workflowNodeLogger(workflowID, runID, runNodeID int64, nodeType string) *slog.Logger {
	return slog.New(&workflowNodeLogHandler{
		app: a, next: slog.Default().Handler(), workflowID: workflowID, runID: runID, runNodeID: runNodeID,
	}).With("event_category", "workflow_node", "node_type", nodeType)
}

func (a *App) appendWorkflowNodeLog(ctx context.Context, workflowID, runID, runNodeID int64, level slog.Level, message string, fields map[string]any) {
	_ = appendWorkflowNodeLog(a.DB.WithContext(context.WithoutCancel(ctx)), db.WorkflowNodeLog{
		WorkflowID: workflowID, RunID: runID, RunNodeID: runNodeID, LoggedAt: time.Now().UTC(),
		Level: workflowLogLevel(level), Message: workflowLogMessage(message), FieldsJSON: workflowLogFields(fields),
	})
}

func appendWorkflowNodeLog(database *gorm.DB, entry db.WorkflowNodeLog) error {
	if entry.LoggedAt.IsZero() {
		entry.LoggedAt = time.Now().UTC()
	}
	if err := database.Create(&entry).Error; err != nil {
		return errors.New("persist workflow node log failed")
	}
	return nil
}

func workflowLogLevel(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warn"
	case level >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

func workflowLogMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "节点日志"
	}
	return truncateWorkflowText(message, maxWorkflowLogMessageRunes)
}

func workflowErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return workflowLogMessage(err.Error())
}

func workflowValueSummary(value map[string]any) string {
	return workflowLogFields(value)
}

func workflowJSONSummary(raw json.RawMessage) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return `{}`
	}
	return workflowValueSummary(value)
}

func workflowLogFields(values map[string]any) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	bounded := make(map[string]any, len(keys))
	for _, key := range keys {
		value, ok := workflowLogScalar(key, values[key])
		if !ok {
			continue
		}
		bounded[key] = value
		raw, err := json.Marshal(bounded)
		if err != nil || len(raw) > maxWorkflowLogFieldsBytes {
			delete(bounded, key)
			break
		}
	}
	raw, err := json.Marshal(bounded)
	if err != nil {
		return `{}`
	}
	return string(raw)
}

func addWorkflowLogAttr(fields map[string]any, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	key := strings.TrimSpace(attr.Key)
	if key == "" {
		return
	}
	if prefix != "" {
		key = prefix + "." + key
	}
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			addWorkflowLogAttr(fields, key, child)
		}
		return
	}
	fields[key] = attr.Value.Any()
}

func workflowLogScalar(key string, value any) (any, bool) {
	if workflowSensitiveLogKey(key) {
		return "[REDACTED]", true
	}
	switch typed := value.(type) {
	case nil:
		return nil, true
	case string:
		return truncateWorkflowText(typed, maxWorkflowLogMessageRunes), true
	case bool:
		return typed, true
	case int:
		return typed, true
	case int8:
		return typed, true
	case int16:
		return typed, true
	case int32:
		return typed, true
	case int64:
		return typed, true
	case uint:
		return typed, true
	case uint8:
		return typed, true
	case uint16:
		return typed, true
	case uint32:
		return typed, true
	case uint64:
		return typed, true
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return nil, false
		}
		return typed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false
		}
		return typed, true
	case json.Number:
		return typed.String(), true
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), true
	case time.Duration:
		return typed.String(), true
	case error:
		return workflowErrorMessage(typed), true
	case []any:
		return fmt.Sprintf("<array:%d>", len(typed)), true
	case map[string]any:
		return fmt.Sprintf("<object:%d>", len(typed)), true
	default:
		return "<omitted>", true
	}
}

func workflowSensitiveLogKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "authorization", "cookie", "credential", "apikey", "dsn", "payload", "rawbody", "rawrequest", "rawresponse",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func truncateWorkflowText(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
