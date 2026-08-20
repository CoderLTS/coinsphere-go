// 本文件:新闻数据的增删改查与首页概览统计。

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"gorm.io/gorm"
)

// ---------- 新闻管理 CRUD ----------

// NewsUpsertPayload 新闻创建/更新载荷。
// struct 把一组字段打包成一个类型;反引号里的 `json:"title"` 是 struct tag,
// 决定它和前端 JSON 里哪个键对应(见 GO入门笔记『复合类型』)。
type NewsUpsertPayload struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	SourceURL   string `json:"sourceUrl"`
	OriginalURL string `json:"originalUrl"`
	ImageURL    string `json:"imageUrl"`
	PublishedAt string `json:"publishedAt"`
}

// ListNews 分页查询新闻。
// (a *App) 是方法接收者;返回 (M, error) 是多返回值:结果 + 错误(见 GO入门笔记『方法与接收者』『变量、函数、错误』)。
func (a *App) ListNews(page CursorPage, keyword string) (M, error) {
	// q 是一个 GORM 查询构造器,可以按条件逐步叠加(见 GO入门笔记『框架:GORM』)。
	// &db.BlockbeatsNews{} 传一个空结构体指针,GORM 借它知道要查哪张表。
	q := a.DB.Model(&db.BlockbeatsNews{})
	if keyword != "" {
		q = q.Where("title LIKE ? OR content LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	// var total int64 声明一个变量并自动赋零值 0;Count 把总行数写回 &total(传指针才能写回)。
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	afterID, err := page.AfterID()
	if err != nil {
		return nil, err
	}
	if afterID > 0 {
		q = q.Where("id < ?", afterID)
	}
	var records []db.BlockbeatsNews
	if err := q.Order("id DESC").Limit(page.Limit + 1).Find(&records).Error; err != nil {
		return nil, err
	}
	hasMore := len(records) > page.Limit
	if hasMore {
		records = records[:page.Limit]
	}
	// make 预建切片,for ... range 遍历,append 逐条追加(见 GO入门笔记『复合类型』)。
	// 用 &records[i] 取元素地址,避免拷贝整个结构体。
	items := make([]M, 0, len(records))
	for i := range records {
		items = append(items, serializeNews(&records[i]))
	}
	lastKey := ""
	if len(records) > 0 {
		lastKey = int64CursorKey(records[len(records)-1].ID)
	}
	return cursorResult(items, page, lastKey, hasMore, total), nil
}

// CreateNews 创建新闻。
func (a *App) CreateNews(payload NewsUpsertPayload) (M, error) {
	normalized, err := normalizeNewsPayload(payload)
	if err != nil {
		return nil, err
	}
	// maxID 用 *int64(指针),因为表可能为空、MAX 结果可能是 NULL —— 那时 maxID 会是 nil。
	var maxID *int64
	a.DB.Model(&db.BlockbeatsNews{}).Select("MAX(source_message_id)").Scan(&maxID)
	// int64(100000000) 是类型转换,给手动创建的新闻一个从 1 亿起步的编号,和抓取来的错开。
	next := int64(100000000)
	if maxID != nil {
		next = *maxID
	}
	next++
	// struct 字面量:按"字段名: 值"逐个赋值,构造一条待插入的记录(见 GO入门笔记『复合类型』)。
	record := db.BlockbeatsNews{
		SourceMessageID: &next,
		PublishedAt:     normalized.publishedAt,
		Title:           normalized.title,
		Content:         normalized.content,
		SourceURL:       normalized.sourceURL,
		OriginalURL:     normalized.originalURL,
		ImageURL:        normalized.imageURL,
	}
	if err := a.DB.Create(&record).Error; err != nil {
		return nil, err
	}
	return serializeNews(&record), nil
}

// UpdateNews 更新新闻。
func (a *App) UpdateNews(newsID int64, payload NewsUpsertPayload) (M, error) {
	var record db.BlockbeatsNews
	if err := a.DB.First(&record, newsID).Error; err != nil {
		return nil, bizErr("News record not found")
	}
	normalized, err := normalizeNewsPayload(payload)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{
		"published_at": normalized.publishedAt, "title": normalized.title, "content": normalized.content,
		"source_url": normalized.sourceURL, "original_url": normalized.originalURL, "image_url": normalized.imageURL,
	}
	if err := a.DB.Model(&record).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := a.DB.First(&record, newsID).Error; err != nil {
		return nil, err
	}
	return serializeNews(&record), nil
}

// DeleteNews 删除新闻。
func (a *App) DeleteNews(newsID int64) error {
	var record db.BlockbeatsNews
	if err := a.DB.First(&record, newsID).Error; err != nil {
		return bizErr("News record not found")
	}
	return a.DB.Delete(&record).Error
}

type normalizedNews struct {
	title, content, sourceURL, originalURL, imageURL string
	publishedAt                                      *time.Time
}

func normalizeNewsPayload(payload NewsUpsertPayload) (*normalizedNews, error) {
	title := strings.TrimSpace(payload.Title)
	content := strings.TrimSpace(payload.Content)
	if title == "" {
		return nil, bizErr("News title cannot be empty")
	}
	if content == "" {
		return nil, bizErr("News content cannot be empty")
	}
	publishedAt := time.Now()
	if raw := strings.TrimSpace(payload.PublishedAt); raw != "" {
		parsed, err := parseFlexibleTime(raw)
		if err != nil {
			return nil, bizErr("publishedAt 时间格式不正确")
		}
		publishedAt = parsed
	}
	return &normalizedNews{
		title: title, content: content,
		sourceURL:   strings.TrimSpace(payload.SourceURL),
		originalURL: strings.TrimSpace(payload.OriginalURL),
		imageURL:    strings.TrimSpace(payload.ImageURL),
		publishedAt: &publishedAt,
	}, nil
}

func parseFlexibleTime(raw string) (time.Time, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(raw), "T", " ")
	for _, layout := range []string{timeLayout, "2006-01-02 15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, normalized, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.ParseInLocation(time.RFC3339, raw, time.Local)
}

func serializeNews(record *db.BlockbeatsNews) M {
	// SourceMessageID 是 *int64:非 nil 时用 * 取出值,nil 时保持 any 的零值(即 JSON 的 null)。
	var sourceMessageID any
	if record.SourceMessageID != nil {
		sourceMessageID = *record.SourceMessageID
	}
	return M{
		"id": record.ID, "sourceMessageId": sourceMessageID,
		"title": record.Title, "content": record.Content,
		"summary":   truncateRunes(strings.TrimSpace(record.Content), 160),
		"sourceUrl": record.SourceURL, "originalUrl": record.OriginalURL, "imageUrl": record.ImageURL,
		"publishedAt": fmtTime(record.PublishedAt),
	}
}

// ---------- 首页 ----------

// GetHomeMeta 首页元信息。
func (a *App) GetHomeMeta() M {
	return M{"service": "coinsphere", "version": "5.0.0"}
}

// GetHomeOverview aggregates the database-owned operational state. Process and
// HTTP counters are merged by the API layer because they belong to the server.
func (a *App) GetHomeOverview(ctx context.Context) (M, error) {
	database := a.dbWithContext(ctx)
	sqlDB, err := database.DB()
	if err != nil {
		return nil, err
	}
	databaseStatus := "healthy"
	pingCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		databaseStatus = "unavailable"
	}
	pool := sqlDB.Stats()

	type laneQueue struct {
		Lane   string
		Queued int64
		Active int64
	}
	var queueRows []laneQueue
	if err := database.Model(&db.WorkerHeartbeat{}).Raw(`
SELECT lane,
       COUNT(*) FILTER (WHERE status = 'queued') AS queued,
       COUNT(*) FILTER (WHERE status IN ('claimed','running','cancelRequested')) AS active
FROM worker_tasks
GROUP BY lane
`).Scan(&queueRows).Error; err != nil {
		return nil, err
	}
	queueByLane := map[string]laneQueue{}
	for _, row := range queueRows {
		queueByLane[row.Lane] = row
	}
	var heartbeatRows []db.WorkerHeartbeat
	if err := database.Order("lane, worker_id").Find(&heartbeatRows).Error; err != nil {
		return nil, err
	}
	heartbeatByLane := map[string]db.WorkerHeartbeat{}
	for _, row := range heartbeatRows {
		current, exists := heartbeatByLane[row.Lane]
		if !exists || row.LastHeartbeatAt.After(current.LastHeartbeatAt) {
			heartbeatByLane[row.Lane] = row
		}
	}
	now := time.Now().UTC()
	workers := make([]M, 0, 2)
	offlineLanes := make([]string, 0, 2)
	for _, lane := range []string{"realtime", "backtest"} {
		heartbeat, exists := heartbeatByLane[lane]
		online := exists && heartbeat.Status == "online" && now.Sub(heartbeat.LastHeartbeatAt) <= 45*time.Second
		status := "offline"
		if online {
			status = "online"
		} else {
			offlineLanes = append(offlineLanes, lane)
		}
		queue := queueByLane[lane]
		lastHeartbeat := ""
		workerID := ""
		if exists {
			lastHeartbeat = formatUTC(heartbeat.LastHeartbeatAt)
			workerID = heartbeat.WorkerID
		}
		workers = append(workers, M{
			"lane": lane, "status": status, "workerId": workerID, "lastHeartbeatAt": lastHeartbeat,
			"queuedCount": queue.Queued, "activeCount": queue.Active,
		})
	}

	type workflowCounts struct {
		Running int64
		Failed  int64
		Success int64
	}
	var workflow workflowCounts
	if err := database.Model(&db.WorkflowExecution{}).Select(`
COUNT(*) FILTER (WHERE status IN ('queued','running','retry_waiting')) AS running,
COUNT(*) FILTER (WHERE status = 'failed') AS failed,
COUNT(*) FILTER (WHERE status = 'success') AS success
`).Scan(&workflow).Error; err != nil {
		return nil, err
	}
	var activeDefinitions int64
	if err := database.Model(&db.WorkflowRuntimeState{}).
		Where("active_workflow_definition_id IS NOT NULL").Count(&activeDefinitions).Error; err != nil {
		return nil, err
	}

	marketStatus := M{"status": "not_synced", "lastSyncAt": "", "nextSyncAt": ""}
	if syncStatus, err := a.GetMarketSyncStatus(ctx); err == nil {
		marketStatus["lastSyncAt"] = syncStatus["lastSyncAt"]
		marketStatus["nextSyncAt"] = syncStatus["nextSyncAt"]
		if execution, ok := syncStatus["lastExecution"].(map[string]any); ok {
			marketStatus["status"] = execution["status"]
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var instrumentCount int64
	if err := database.Model(&db.MarketInstrument{}).Where("quote_asset = 'USDT'").Count(&instrumentCount).Error; err != nil {
		return nil, err
	}
	marketStatus["instrumentCount"] = instrumentCount

	type accountCounts struct {
		Total  int64
		Active int64
		Paused int64
	}
	var accounts accountCounts
	if err := database.Model(&db.TradingAccount{}).Where("archived_at IS NULL").Select(`
COUNT(*) AS total,
COUNT(*) FILTER (WHERE status = 'active') AS active,
COUNT(*) FILTER (WHERE status = 'paused') AS paused
`).Scan(&accounts).Error; err != nil {
		return nil, err
	}
	var control db.TradingControl
	if err := database.Where("id = 1").Take(&control).Error; err != nil {
		return nil, err
	}

	alerts := make([]M, 0)
	if databaseStatus != "healthy" {
		alerts = append(alerts, M{"severity": "danger", "title": "数据库不可用", "description": "PostgreSQL 健康检查失败", "path": ""})
	}
	if len(offlineLanes) > 0 {
		alerts = append(alerts, M{"severity": "danger", "title": "Worker 离线", "description": strings.Join(offlineLanes, "、") + " 队列超过 45 秒未收到心跳", "path": "/scheduler/execution"})
	}
	if workflow.Failed > 0 {
		alerts = append(alerts, M{"severity": "warning", "title": "存在失败的工作流", "description": "请在执行记录中查看结构化失败信息", "count": workflow.Failed, "path": "/scheduler/execution"})
	}
	if marketStatus["status"] == "failed" {
		alerts = append(alerts, M{"severity": "warning", "title": "行情同步失败", "description": "最近一次币种元数据同步未成功", "path": "/data/market-metadata"})
	}
	if control.EmergencyStopped {
		alerts = append(alerts, M{"severity": "danger", "title": "交易急停已开启", "description": control.StopReason, "path": "/trading/accounts"})
	}
	if accounts.Paused > 0 {
		alerts = append(alerts, M{"severity": "warning", "title": "交易账户已暂停", "description": "请检查账户凭据和风控状态", "count": accounts.Paused, "path": "/trading/accounts"})
	}

	return M{
		"database": M{
			"status": databaseStatus, "maxOpenConnections": pool.MaxOpenConnections,
			"openConnections": pool.OpenConnections, "inUse": pool.InUse, "idle": pool.Idle,
			"waitCount": pool.WaitCount,
		},
		"workers": workers,
		"workflow": M{
			"activeDefinitions": activeDefinitions, "runningCount": workflow.Running,
			"failedCount": workflow.Failed, "successCount": workflow.Success,
		},
		"market": marketStatus,
		"trading": M{
			"accountCount": accounts.Total, "activeAccountCount": accounts.Active,
			"pausedAccountCount": accounts.Paused, "emergencyStopped": control.EmergencyStopped,
		},
		"alerts": alerts,
	}, nil
}
