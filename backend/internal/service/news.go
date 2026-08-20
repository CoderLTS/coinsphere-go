// 本文件:新闻数据的增删改查与首页概览统计。

package service

import (
	"strings"
	"time"

	"coinsphere/backend/internal/db"
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
		// []rune(summary) 把字符串按"字符"拆开再数长度 —— 中文按字数算,而不是字节数
		//(一个中文占多个字节,直接 len(字符串) 会偏大)。
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
