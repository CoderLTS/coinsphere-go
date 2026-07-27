package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

const blockbeatsAPIURL = "https://api.theblockbeats.news/v1/open-api/open-flash"

// ---------- 新闻管理 CRUD ----------

// NewsUpsertPayload 新闻创建/更新载荷。
type NewsUpsertPayload struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	SourceURL   string `json:"sourceUrl"`
	OriginalURL string `json:"originalUrl"`
	ImageURL    string `json:"imageUrl"`
	PublishedAt string `json:"publishedAt"`
}

// ListNews 分页查询新闻。
func (a *App) ListNews(current, size int, keyword string) (M, error) {
	q := a.DB.Model(&db.BlockbeatsNews{})
	if keyword != "" {
		q = q.Where("title LIKE ? OR content LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var records []db.BlockbeatsNews
	if err := q.Order("published_at DESC, id DESC").Offset((current - 1) * size).Limit(size).Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]M, 0, len(records))
	for i := range records {
		items = append(items, serializeNews(&records[i]))
	}
	return pagedResult(items, current, size, total), nil
}

// CreateNews 创建新闻。
func (a *App) CreateNews(payload NewsUpsertPayload) (M, error) {
	normalized, err := normalizeNewsPayload(payload)
	if err != nil {
		return nil, err
	}
	var maxID *int64
	a.DB.Model(&db.BlockbeatsNews{}).Select("MAX(source_message_id)").Scan(&maxID)
	next := int64(100000000)
	if maxID != nil {
		next = *maxID
	}
	next++
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

// ---------- Blockbeats 抓取与同步 ----------

type blockbeatsItem struct {
	MessageID int64
	AddTime   *time.Time
	URL       string
	Title     string
	Content   string
	Link      string
	Picture   string
}

func fetchBlockbeatsNews(size, page int) ([]blockbeatsItem, error) {
	url := fmt.Sprintf("%s?type=push&size=%d&page=%d", blockbeatsAPIURL, size, page)
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("blockbeats http %d", resp.StatusCode)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		// 上游 data 字段可能是 {data:[...]} 或直接为数组,做兼容解析。
		var payload struct {
			Data json.RawMessage `json:"data"`
		}
		type blockbeatsRow struct {
			ID         *int64 `json:"id"`
			CreateTime any    `json:"create_time"`
			URL        string `json:"url"`
			Title      string `json:"title"`
			Content    string `json:"content"`
			Link       string `json:"link"`
			Pic        string `json:"pic"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var rows []blockbeatsRow
		var nested struct {
			Data []blockbeatsRow `json:"data"`
		}
		if len(payload.Data) > 0 {
			if err := json.Unmarshal(payload.Data, &nested); err == nil && nested.Data != nil {
				rows = nested.Data
			} else if err := json.Unmarshal(payload.Data, &rows); err != nil {
				rows = nil
			}
		}
		items := make([]blockbeatsItem, 0, len(rows))
		for _, row := range rows {
			if row.ID == nil {
				continue
			}
			items = append(items, blockbeatsItem{
				MessageID: *row.ID,
				AddTime:   parseUnixAny(row.CreateTime),
				URL:       row.URL,
				Title:     row.Title,
				Content:   cleanBlockbeatsContent(row.Content),
				Link:      row.Link,
				Picture:   row.Pic,
			})
		}
		return items, nil
	}
	return nil, lastErr
}

func parseUnixAny(value any) *time.Time {
	var seconds int64
	switch v := value.(type) {
	case float64:
		seconds = int64(v)
	case string:
		if _, err := fmt.Sscanf(v, "%d", &seconds); err != nil {
			return nil
		}
	default:
		return nil
	}
	if seconds <= 0 {
		return nil
	}
	t := time.Unix(seconds, 0)
	return &t
}

func cleanBlockbeatsContent(content string) string {
	replacer := strings.NewReplacer("<p>", "", "</p>", "", "<br", "")
	return strings.TrimSpace(replacer.Replace(content))
}

// newsSyncResult 一次同步结果。
type newsSyncResult struct {
	FetchedCount  int
	InsertedCount int
	InsertedItems []M
}

// syncLatestNews 拉取 Blockbeats 快讯并去重入库。
func (a *App) syncLatestNews(pageSize, page int) (*newsSyncResult, error) {
	rows, err := fetchBlockbeatsNews(pageSize, page)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &newsSyncResult{InsertedItems: []M{}}, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.MessageID)
	}
	var existingIDs []int64
	a.DB.Model(&db.BlockbeatsNews{}).Where("source_message_id IN ?", ids).Pluck("source_message_id", &existingIDs)
	existing := map[int64]bool{}
	for _, id := range existingIDs {
		existing[id] = true
	}

	inserted := 0
	insertedItems := make([]M, 0)
	for _, row := range rows {
		if existing[row.MessageID] {
			continue
		}
		messageID := row.MessageID
		record := db.BlockbeatsNews{
			SourceMessageID: &messageID,
			PublishedAt:     row.AddTime,
			SourceURL:       row.URL,
			Title:           row.Title,
			Content:         row.Content,
			OriginalURL:     row.Link,
			ImageURL:        row.Picture,
		}
		if err := a.DB.Create(&record).Error; err != nil {
			continue
		}
		inserted++
		insertedItems = append(insertedItems, M{
			"id":              record.ID,
			"sourceMessageId": row.MessageID,
			"sourceName":      "Blockbeats",
			"title":           row.Title,
			"content":         row.Content,
			"sourceUrl":       row.URL,
			"originalUrl":     row.Link,
			"publishedAt":     fmtTime(record.PublishedAt),
		})
	}
	return &newsSyncResult{FetchedCount: len(rows), InsertedCount: inserted, InsertedItems: insertedItems}, nil
}

// ---------- 首页 ----------

// GetHomeMeta 首页元信息。
func (a *App) GetHomeMeta() M {
	return M{"service": "coinsphere", "version": "5.0.0"}
}

// GetHomeOverview 首页概览数据。
func (a *App) GetHomeOverview() (M, error) {
	var recentNews []db.BlockbeatsNews
	a.DB.Order("published_at DESC, id DESC").Limit(5).Find(&recentNews)

	definitions := a.listLatestDefinitions()
	if len(definitions) > 5 {
		definitions = definitions[:5]
	}
	definitionIDs := collectIDs(definitions, func(d db.WorkflowDefinition) int64 { return d.ID })
	executionCounts := a.countExecutionsByDefinitionIDs(definitionIDs)

	var runtimeStates []db.WorkflowRuntimeState
	a.DB.Find(&runtimeStates)
	stateByCode := map[string]*db.WorkflowRuntimeState{}
	activeCount := 0
	for i := range runtimeStates {
		stateByCode[runtimeStates[i].WorkflowCode] = &runtimeStates[i]
		if runtimeStates[i].ActiveWorkflowDefinitionID != nil {
			activeCount++
		}
	}

	var newsTotal, newsToday int64
	a.DB.Model(&db.BlockbeatsNews{}).Count(&newsTotal)
	a.DB.Model(&db.BlockbeatsNews{}).Where("published_at >= ?", time.Now().Add(-24*time.Hour)).Count(&newsToday)

	newsItems := make([]M, 0, len(recentNews))
	for i := range recentNews {
		item := recentNews[i]
		summary := strings.ReplaceAll(strings.TrimSpace(item.Content), "\n", " ")
		if len([]rune(summary)) > 120 {
			summary = truncateRunes(summary, 120) + "..."
		}
		var sourceMessageID any
		if item.SourceMessageID != nil {
			sourceMessageID = *item.SourceMessageID
		}
		newsItems = append(newsItems, M{
			"id": item.ID, "sourceMessageId": sourceMessageID,
			"title": item.Title, "summary": summary, "publishedAt": fmtTime(item.PublishedAt),
		})
	}

	definitionItems := make([]M, 0, len(definitions))
	for i := range definitions {
		def := definitions[i]
		isActive := false
		if state, ok := stateByCode[def.Code]; ok && state.ActiveWorkflowDefinitionID != nil {
			isActive = *state.ActiveWorkflowDefinitionID == def.ID
		}
		definitionItems = append(definitionItems, M{
			"workflowDefinitionId":   def.ID,
			"workflowDefinitionCode": def.Code,
			"workflowDefinitionName": def.DisplayName,
			"isActive":               isActive,
			"runCount":               executionCounts[def.ID],
			"createdAt":              fmtTimeV(def.CreatedAt),
		})
	}

	return M{
		"stats": M{
			"newsTotal":         newsTotal,
			"newsToday":         newsToday,
			"activeDefinitions": activeCount,
		},
		"recentNews":  newsItems,
		"definitions": definitionItems,
	}, nil
}
