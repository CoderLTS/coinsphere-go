package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"coinsphere/backend/internal/config"
	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

const (
	systemLogQueueCapacity = 5000
	systemLogBatchSize     = 100
	systemLogFlushInterval = 250 * time.Millisecond
	systemLogCleanupPeriod = 24 * time.Hour
)

var systemLogAllowedAttrs = map[string]bool{
	"component": true, "request_id": true, "user_id": true,
	"method": true, "route": true, "status": true, "duration_ms": true,
	"error_category": true, "workflow_id": true, "run_id": true,
	"count": true, "engine": true, "event_category": true,
}

var systemLogComponents = map[string]bool{
	"runtime": true, "http.access": true, "audit": true, "workflow.runtime": true,
}

type SystemLogRuntime struct {
	db        *gorm.DB
	level     *slog.LevelVar
	queue     chan db.SystemLog
	stop      chan struct{}
	done      chan struct{}
	cleanup   chan struct{}
	closeOne  sync.Once
	closed    atomic.Bool
	enqueueMu sync.RWMutex
	started   time.Time
	written   atomic.Uint64
	dropped   atomic.Uint64
	failed    atomic.Uint64

	settingsMu sync.RWMutex
	settings   db.SystemLogSettings
}

type systemLogHandler struct {
	runtime *SystemLogRuntime
	next    slog.Handler
	attrs   map[string]slog.Value
	groups  []string
}

type SystemLogQuery struct {
	Page       CursorPage
	StartTime  *time.Time
	EndTime    *time.Time
	Level      string
	Component  string
	RequestID  string
	UserID     *int64
	Method     string
	Route      string
	StatusCode *int
	Keyword    string
}

type SystemLogSettingsPayload struct {
	Level         string `json:"level"`
	RetentionDays int    `json:"retentionDays"`
}

type SystemLogRuntimeStatus struct {
	Level         string `json:"level"`
	RetentionDays int    `json:"retentionDays"`
	QueueDepth    int    `json:"queueDepth"`
	QueueCapacity int    `json:"queueCapacity"`
	Written       uint64 `json:"written"`
	Dropped       uint64 `json:"dropped"`
	Failed        uint64 `json:"failed"`
	StartedAt     string `json:"startedAt"`
	UpdatedAt     string `json:"updatedAt"`
	UpdatedBy     *int64 `json:"updatedBy"`
}

func NewSystemLogRuntime(gdb *gorm.DB, cfg config.LogConfig, levelVar *slog.LevelVar) (*SystemLogRuntime, error) {
	level, levelText, err := parseSystemLogLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	settings := db.SystemLogSettings{
		ID: 1, Level: levelText, RetentionDays: cfg.RetentionDays, UpdatedAt: time.Now().UTC(),
	}
	if err := gdb.Where("id = ?", settings.ID).FirstOrCreate(&settings).Error; err != nil {
		return nil, fmt.Errorf("initialize system log settings: %w", err)
	}
	level, settings.Level, err = parseSystemLogLevel(settings.Level)
	if err != nil || settings.RetentionDays < 1 || settings.RetentionDays > 365 {
		return nil, errors.New("stored system log settings are invalid")
	}
	levelVar.Set(level)
	runtime := &SystemLogRuntime{
		db: gdb, level: levelVar, queue: make(chan db.SystemLog, systemLogQueueCapacity),
		stop: make(chan struct{}), done: make(chan struct{}), cleanup: make(chan struct{}, 1),
		started: time.Now().UTC(), settings: settings,
	}
	go runtime.run()
	return runtime, nil
}

func (r *SystemLogRuntime) Handler(next slog.Handler) slog.Handler {
	return &systemLogHandler{runtime: r, next: next}
}

func (r *SystemLogRuntime) Close(ctx context.Context) error {
	r.closeOne.Do(func() {
		r.enqueueMu.Lock()
		r.closed.Store(true)
		close(r.stop)
		r.enqueueMu.Unlock()
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *SystemLogRuntime) Status() SystemLogRuntimeStatus {
	r.settingsMu.RLock()
	settings := r.settings
	r.settingsMu.RUnlock()
	return SystemLogRuntimeStatus{
		Level: settings.Level, RetentionDays: settings.RetentionDays,
		QueueDepth: len(r.queue), QueueCapacity: cap(r.queue),
		Written: r.written.Load(), Dropped: r.dropped.Load(), Failed: r.failed.Load(),
		StartedAt: r.started.Format(time.RFC3339Nano), UpdatedAt: settings.UpdatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedBy: settings.UpdatedBy,
	}
}

func (r *SystemLogRuntime) UpdateSettings(ctx context.Context, payload SystemLogSettingsPayload, userID int64) (SystemLogRuntimeStatus, error) {
	level, levelText, err := parseSystemLogLevel(payload.Level)
	if err != nil {
		return SystemLogRuntimeStatus{}, err
	}
	if payload.RetentionDays < 1 || payload.RetentionDays > 365 {
		return SystemLogRuntimeStatus{}, errors.New("retentionDays must be between 1 and 365")
	}
	now := time.Now().UTC()
	updatedBy := &userID
	if err := r.db.WithContext(ctx).Model(&db.SystemLogSettings{}).Where("id = ?", 1).Updates(map[string]any{
		"level": levelText, "retention_days": payload.RetentionDays,
		"updated_by": userID, "updated_at": now,
	}).Error; err != nil {
		return SystemLogRuntimeStatus{}, err
	}
	r.settingsMu.Lock()
	previousRetention := r.settings.RetentionDays
	r.settings.Level = levelText
	r.settings.RetentionDays = payload.RetentionDays
	r.settings.UpdatedBy = updatedBy
	r.settings.UpdatedAt = now
	r.settingsMu.Unlock()
	r.level.Set(level)
	if payload.RetentionDays < previousRetention {
		select {
		case r.cleanup <- struct{}{}:
		default:
		}
	}
	return r.Status(), nil
}

func (r *SystemLogRuntime) run() {
	defer close(r.done)
	flushTicker := time.NewTicker(systemLogFlushInterval)
	cleanupTicker := time.NewTicker(systemLogCleanupPeriod)
	defer flushTicker.Stop()
	defer cleanupTicker.Stop()
	batch := make([]db.SystemLog, 0, systemLogBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := r.db.CreateInBatches(&batch, systemLogBatchSize).Error; err != nil {
			r.failed.Add(uint64(len(batch)))
		} else {
			r.written.Add(uint64(len(batch)))
		}
		batch = batch[:0]
	}
	cleanup := func() {
		r.settingsMu.RLock()
		retentionDays := r.settings.RetentionDays
		r.settingsMu.RUnlock()
		cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
		if err := r.db.Where("logged_at < ?", cutoff).Delete(&db.SystemLog{}).Error; err != nil {
			r.failed.Add(1)
		}
	}
	cleanup()
	for {
		select {
		case entry := <-r.queue:
			batch = append(batch, entry)
			if len(batch) == systemLogBatchSize {
				flush()
			}
		case <-flushTicker.C:
			flush()
		case <-cleanupTicker.C:
			cleanup()
		case <-r.cleanup:
			cleanup()
		case <-r.stop:
			for {
				select {
				case entry := <-r.queue:
					batch = append(batch, entry)
					if len(batch) == systemLogBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (h *systemLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *systemLogHandler) Handle(ctx context.Context, record slog.Record) error {
	nextErr := h.next.Handle(ctx, record)
	attrs := make(map[string]slog.Value, len(h.attrs)+record.NumAttrs())
	for key, value := range h.attrs {
		attrs[key] = value
	}
	record.Attrs(func(attr slog.Attr) bool {
		addSystemLogAttr(attrs, strings.Join(h.groups, "."), attr)
		return true
	})
	if systemLogString(attrs["event_category"]) == "workflow_node" {
		return nextErr
	}
	entry := systemLogEntry(record, attrs)
	if entry.Component == "http.access" && entry.Method == "GET" &&
		(entry.Route == "/api/v1/system/logs" || entry.Route == "/api/v1/system/logs/runtime") {
		return nextErr
	}
	h.runtime.enqueueMu.RLock()
	defer h.runtime.enqueueMu.RUnlock()
	if h.runtime.closed.Load() {
		return nextErr
	}
	select {
	case h.runtime.queue <- entry:
	default:
		h.runtime.dropped.Add(1)
	}
	return nextErr
}

func (h *systemLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	stored := make(map[string]slog.Value, len(h.attrs)+len(attrs))
	for key, value := range h.attrs {
		stored[key] = value
	}
	prefix := strings.Join(h.groups, ".")
	for _, attr := range attrs {
		addSystemLogAttr(stored, prefix, attr)
	}
	return &systemLogHandler{
		runtime: h.runtime, next: h.next.WithAttrs(attrs), groups: append([]string(nil), h.groups...),
		attrs: stored,
	}
}

func (h *systemLogHandler) WithGroup(name string) slog.Handler {
	groups := append([]string(nil), h.groups...)
	if name = strings.TrimSpace(name); name != "" {
		groups = append(groups, name)
	}
	return &systemLogHandler{
		runtime: h.runtime, next: h.next.WithGroup(name), groups: groups,
		attrs: h.attrs,
	}
}

func addSystemLogAttr(values map[string]slog.Value, prefix string, attr slog.Attr) {
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
			addSystemLogAttr(values, key, child)
		}
		return
	}
	if systemLogAllowedAttrs[key] {
		values[key] = attr.Value
	}
}

func systemLogEntry(record slog.Record, attrs map[string]slog.Value) db.SystemLog {
	loggedAt := record.Time.UTC()
	if loggedAt.IsZero() {
		loggedAt = time.Now().UTC()
	}
	component := boundedSystemLogString(systemLogString(attrs["component"]), 64)
	if !systemLogComponents[component] {
		component = "runtime"
	}
	message := strings.TrimSpace(record.Message)
	if message == "" {
		message = "system event"
	}
	requestID := systemLogString(attrs["request_id"])
	if !validSystemLogRequestID(requestID) {
		requestID = ""
	}
	entry := db.SystemLog{
		LoggedAt: loggedAt, Level: workflowLogLevel(record.Level), Component: component,
		Message: truncateWorkflowText(message, 1000), RequestID: requestID,
		Method: boundedSystemLogString(strings.ToUpper(systemLogString(attrs["method"])), 8),
		Route:  boundedSystemLogString(systemLogString(attrs["route"]), 255), DetailsJSON: `{}`,
	}
	if value, ok := systemLogInt64(attrs["user_id"]); ok && value > 0 {
		entry.UserID = &value
	}
	if value, ok := systemLogInt64(attrs["status"]); ok && value >= 100 && value <= 599 {
		status := int(value)
		entry.StatusCode = &status
	}
	if value, ok := systemLogInt64(attrs["duration_ms"]); ok && value >= 0 {
		entry.DurationMS = &value
	}
	details := make(map[string]any)
	for _, key := range []string{"error_category", "workflow_id", "run_id", "count", "engine"} {
		if value, ok := systemLogScalar(attrs[key]); ok {
			details[key] = value
		}
	}
	if raw, err := json.Marshal(details); err == nil {
		entry.DetailsJSON = string(raw)
	}
	return entry
}

func systemLogString(value slog.Value) string {
	if value.Kind() != slog.KindString {
		return ""
	}
	return strings.TrimSpace(value.String())
}

func systemLogInt64(value slog.Value) (int64, bool) {
	switch value.Kind() {
	case slog.KindInt64:
		return value.Int64(), true
	case slog.KindUint64:
		if value.Uint64() <= math.MaxInt64 {
			return int64(value.Uint64()), true
		}
	}
	return 0, false
}

func systemLogScalar(value slog.Value) (any, bool) {
	switch value.Kind() {
	case slog.KindString:
		return boundedSystemLogString(value.String(), 500), true
	case slog.KindBool:
		return value.Bool(), true
	case slog.KindInt64:
		return value.Int64(), true
	case slog.KindUint64:
		return value.Uint64(), true
	case slog.KindFloat64:
		if number := value.Float64(); !math.IsNaN(number) && !math.IsInf(number, 0) {
			return number, true
		}
	}
	return nil, false
}

func boundedSystemLogString(value string, limit int) string {
	return truncateWorkflowText(strings.TrimSpace(value), limit)
}

func validSystemLogRequestID(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func parseSystemLogLevel(value string) (slog.Level, string, error) {
	text := strings.ToLower(strings.TrimSpace(value))
	var level slog.Level
	if text != "debug" && text != "info" && text != "warn" && text != "error" {
		return level, "", errors.New("level must be debug, info, warn, or error")
	}
	if err := level.UnmarshalText([]byte(text)); err != nil {
		return level, "", err
	}
	return level, text, nil
}

func (a *App) ListSystemLogs(ctx context.Context, query SystemLogQuery) (M, error) {
	q, err := a.systemLogQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	afterID, err := query.Page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		q = q.Where("id < ?", afterID)
	}
	var rows []db.SystemLog
	if err := q.Order("id DESC").Limit(query.Page.Limit + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	hasMore := len(rows) > query.Page.Limit
	if hasMore {
		rows = rows[:query.Page.Limit]
	}
	records := make([]M, 0, len(rows))
	for i := range rows {
		records = append(records, serializeSystemLog(&rows[i]))
	}
	lastKey := ""
	if len(rows) > 0 {
		lastKey = int64CursorKey(rows[len(rows)-1].ID)
	}
	return cursorResult(records, query.Page, lastKey, hasMore, total), nil
}

func (a *App) DeleteSystemLogs(ctx context.Context, query SystemLogQuery) (M, error) {
	if !query.hasFilter() {
		return nil, errors.New("at least one log filter is required")
	}
	q, err := a.systemLogQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	result := q.Delete(&db.SystemLog{})
	if result.Error != nil {
		return nil, result.Error
	}
	return M{"deletedCount": result.RowsAffected}, nil
}

func (a *App) GetSystemLogRuntime() (SystemLogRuntimeStatus, error) {
	if a.SystemLogs == nil {
		return SystemLogRuntimeStatus{}, errors.New("system log runtime is unavailable")
	}
	return a.SystemLogs.Status(), nil
}

func (a *App) UpdateSystemLogRuntime(ctx context.Context, payload SystemLogSettingsPayload, userID int64) (SystemLogRuntimeStatus, error) {
	if a.SystemLogs == nil {
		return SystemLogRuntimeStatus{}, errors.New("system log runtime is unavailable")
	}
	return a.SystemLogs.UpdateSettings(ctx, payload, userID)
}

func (a *App) systemLogQuery(ctx context.Context, query SystemLogQuery) (*gorm.DB, error) {
	if query.StartTime != nil && query.EndTime != nil && query.StartTime.After(*query.EndTime) {
		return nil, errors.New("startTime must not be after endTime")
	}
	if query.Level != "" {
		if _, _, err := parseSystemLogLevel(query.Level); err != nil {
			return nil, err
		}
	}
	q := a.DB.WithContext(ctx).Model(&db.SystemLog{})
	if query.StartTime != nil {
		q = q.Where("logged_at >= ?", query.StartTime.UTC())
	}
	if query.EndTime != nil {
		q = q.Where("logged_at <= ?", query.EndTime.UTC())
	}
	if query.Level != "" {
		q = q.Where("level = ?", strings.ToLower(strings.TrimSpace(query.Level)))
	}
	if value := strings.TrimSpace(query.Component); value != "" {
		q = q.Where("component ILIKE ?", "%"+value+"%")
	}
	if value := strings.TrimSpace(query.RequestID); value != "" {
		q = q.Where("request_id = ?", value)
	}
	if query.UserID != nil {
		q = q.Where("user_id = ?", *query.UserID)
	}
	if value := strings.ToUpper(strings.TrimSpace(query.Method)); value != "" {
		q = q.Where("method = ?", value)
	}
	if value := strings.TrimSpace(query.Route); value != "" {
		q = q.Where("route ILIKE ?", "%"+value+"%")
	}
	if query.StatusCode != nil {
		q = q.Where("status_code = ?", *query.StatusCode)
	}
	if value := strings.TrimSpace(query.Keyword); value != "" {
		pattern := "%" + value + "%"
		q = q.Where("message ILIKE ? OR request_id ILIKE ?", pattern, pattern)
	}
	return q, nil
}

func (q SystemLogQuery) hasFilter() bool {
	return q.StartTime != nil || q.EndTime != nil || strings.TrimSpace(q.Level) != "" ||
		strings.TrimSpace(q.Component) != "" || strings.TrimSpace(q.RequestID) != "" || q.UserID != nil ||
		strings.TrimSpace(q.Method) != "" || strings.TrimSpace(q.Route) != "" || q.StatusCode != nil ||
		strings.TrimSpace(q.Keyword) != ""
}

func serializeSystemLog(row *db.SystemLog) M {
	details := map[string]any{}
	_ = json.Unmarshal([]byte(row.DetailsJSON), &details)
	return M{
		"id": row.ID, "loggedAt": row.LoggedAt.UTC().Format(time.RFC3339Nano),
		"level": row.Level, "component": row.Component, "message": row.Message,
		"requestId": row.RequestID, "userId": row.UserID, "method": row.Method,
		"route": row.Route, "statusCode": row.StatusCode, "durationMs": row.DurationMS,
		"details": details,
	}
}
