// taskdef.go —— "任务定义"注册表与它的参数配置。
//
// 任务(task)和节点(node)是两层东西:节点是画在图上的方框,任务是 task.run 这种节点真正去执行的
// 那份能力(比如"抓取 Blockbeats 快讯")。任务清单同样"代码即真源"——写死在下面的 taskDefinitions
// 里,数据库只存管理员对默认参数做的覆盖(TaskDefinitionConfig)。
//
// 本文件包含:任务注册表本体与查询、管理页用的分页/序列化、以及参数的三层合并
// (schema 默认值 → 全局覆盖 → 节点运行时输入,后者优先)。
// 节点注册表那一半在 nodes.go;JSON Schema 的通用取值/校验小工具在 schema.go。

package service

import (
	"sort"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
)

// taskDefinition 一个可执行任务能力。
//
// 重点看 Execute 字段:类型是 func(a *App, inputs M) (M, error) —— 一个"函数类型"。
// 结构体里可以直接存一个"函数值",这正是注册表的关键:把"这种任务怎么执行"的那段代码
// 当成数据挂在字段上,需要时取出来调用。见 GO入门笔记『变量、函数、错误』。
// (M 是本项目别名 = map[string]any;ParameterSchema 用 JSON Schema 描述该任务接收哪些参数。)
type taskDefinition struct {
	Code            string
	Label           string
	Description     string
	ParameterSchema M
	Execute         func(a *App, inputs M) (M, error)
}

// taskDefinitions 是所有内置任务的清单(注册表本体)。
// []*taskDefinition 表示"taskDefinition 指针的切片";里面每个 {...} 就是一条登记,
// 元素类型是指针,所以本可写 &taskDefinition{...},Go 允许省略前面的 &taskDefinition。
// 目前只登记了一种任务:抓取 Blockbeats 快讯。
var taskDefinitions = []*taskDefinition{
	{
		Code:        "blockbeats_news_fetch",
		Label:       "Blockbeats 新闻抓取",
		Description: "拉取 Blockbeats 快讯,去重后写入新闻数据表。",
		ParameterSchema: M{
			"type": "object",
			"properties": M{
				"pageSize": M{"type": "integer", "title": "抓取数量", "default": 10, "minimum": 1, "maximum": 50},
				"page":     M{"type": "integer", "title": "页码", "default": 1, "minimum": 1},
			},
		},
		// 这里给 Execute 字段赋了一个"匿名函数"(函数字面量),就是这种任务真正执行的逻辑:
		// 读分页参数(缺省给默认值)→ 调 syncLatestNews 抓取 → 返回统计结果。
		Execute: func(a *App, inputs M) (M, error) {
			pageSize := int(asInt64(inputs["pageSize"]))
			if pageSize <= 0 {
				pageSize = 10
			}
			page := int(asInt64(inputs["page"]))
			if page <= 0 {
				page = 1
			}
			result, err := a.syncLatestNews(pageSize, page)
			if err != nil {
				return nil, err
			}
			return M{
				"taskDefinitionCode": "blockbeats_news_fetch",
				"fetchedCount":       result.FetchedCount,
				"insertedCount":      result.InsertedCount,
				"insertedItems":      result.InsertedItems,
			}, nil
		},
	},
}

// getTaskDefinition 按 code 在注册表里查任务:for range 逐条扫描,找到就返回,找不到返回业务错误。
func getTaskDefinition(code string) (*taskDefinition, error) {
	for _, definition := range taskDefinitions {
		if definition.Code == code {
			return definition, nil
		}
	}
	return nil, bizErr("任务定义不存在: %s", code)
}

// ListTaskDefinitions 任务定义列表(节点面板用)。
func (a *App) ListTaskDefinitions() []M {
	// make([]M, 0, n):新建长度 0、预留容量 n 的切片;下面用 append 一条条填。
	result := make([]M, 0, len(taskDefinitions))
	for _, definition := range taskDefinitions {
		result = append(result, M{
			"code": definition.Code, "label": definition.Label,
			"description": definition.Description, "parameterSchema": definition.ParameterSchema,
		})
	}
	return result
}

// ListTaskDefinitionPage 任务定义管理页分页。
func (a *App) ListTaskDefinitionPage(current, size int, keyword string) M {
	// 先 copy 一份再排序,避免打乱全局 taskDefinitions 的顺序。
	items := make([]*taskDefinition, len(taskDefinitions))
	copy(items, taskDefinitions)
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Code) < strings.ToLower(items[j].Code)
	})
	keywordText := strings.ToLower(strings.TrimSpace(keyword))
	if keywordText != "" {
		// items[:0] 是常见惯用法:复用同一底层数组做"原地筛选",把命中的元素依次写回开头。
		filtered := items[:0]
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Code), keywordText) ||
				strings.Contains(strings.ToLower(item.Label), keywordText) ||
				strings.Contains(strings.ToLower(item.Description), keywordText) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	// 按页码算出切片的起止下标,并夹到合法范围内;items[start:end] 就是本页数据(左闭右开)。
	total := len(items)
	start := (current - 1) * size
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	pageItems := items[start:end]
	records := make([]M, 0, len(pageItems))
	for _, item := range pageItems {
		records = append(records, a.serializeTaskDefinitionItem(item))
	}
	return pagedResult(records, current, size, int64(total))
}

// UpdateTaskDefinitionDefaultParams 保存全局默认参数覆盖。
func (a *App) UpdateTaskDefinitionDefaultParams(code string, params M, operatorUserID int64) (M, error) {
	definition, err := getTaskDefinition(code)
	if err != nil {
		return nil, err
	}
	if err := validatePartialParams(params, definition.ParameterSchema); err != nil {
		return nil, err
	}
	// GORM 查询:Where 里 ? 是占位符(值单独传,防 SQL 注入),First 查一条并写回 &existing。
	// .Error == nil 表示确实查到了记录。见 GO入门笔记『框架:GORM』。
	var existing db.TaskDefinitionConfig
	existingFound := a.DB.Where("task_definition_code = ?", code).First(&existing).Error == nil
	existingOverrides := M{}
	if existingFound {
		existingOverrides = loadJSONObject(existing.ParameterOverridesJSON)
	}
	mergedEffective := buildEffectiveDefaultParams(definition.ParameterSchema, existingOverrides)
	for key, value := range params {
		mergedEffective[key] = value
	}
	nextOverrides := buildConfiguredOverrides(definition.ParameterSchema, mergedEffective)
	now := time.Now()
	// 三种情况:有覆盖值且记录已存在 → 更新;有覆盖值但没记录 → 新建;没有任何覆盖值 → 删掉旧记录。
	if len(nextOverrides) > 0 {
		if existingFound {
			a.DB.Model(&existing).Updates(map[string]any{
				"parameter_overrides_json": dumpJSON(nextOverrides),
				"updated_by":               operatorUserID, "updated_at": now,
			})
		} else {
			a.DB.Create(&db.TaskDefinitionConfig{
				TaskDefinitionCode:     code,
				ParameterOverridesJSON: dumpJSON(nextOverrides),
				UpdatedBy:              &operatorUserID,
				CreatedAt:              now, UpdatedAt: now,
			})
		}
	} else if existingFound {
		a.DB.Where("task_definition_code = ?", code).Delete(&db.TaskDefinitionConfig{})
	}
	return a.serializeTaskDefinitionItem(definition), nil
}

// buildExecutionInputs 合并 schema 默认值、全局覆盖与节点输入。
//
// 合并有优先级:先铺 schema 默认值,再盖全局覆盖,最后盖节点运行时输入 ——
// 后写的覆盖先写的,所以运行时输入优先级最高。
func (a *App) buildExecutionInputs(code string, taskParams, runtimeInputs M) (M, error) {
	definition, err := getTaskDefinition(code)
	if err != nil {
		return nil, err
	}
	var config db.TaskDefinitionConfig
	overrides := M{}
	if err := a.DB.Where("task_definition_code = ?", code).First(&config).Error; err == nil {
		overrides = loadJSONObject(config.ParameterOverridesJSON)
	}
	result := buildEffectiveDefaultParams(definition.ParameterSchema, overrides)
	for key, value := range taskParams {
		result[key] = value
	}
	for key, value := range runtimeInputs {
		result[key] = value
	}
	return result, nil
}

func (a *App) serializeTaskDefinitionItem(definition *taskDefinition) M {
	var config db.TaskDefinitionConfig
	configFound := a.DB.Where("task_definition_code = ?", definition.Code).First(&config).Error == nil
	overrides := M{}
	updatedAt := ""
	var updatedBy any
	if configFound {
		overrides = loadJSONObject(config.ParameterOverridesJSON)
		updatedAt = fmtTimeV(config.UpdatedAt)
		if config.UpdatedBy != nil {
			updatedBy = *config.UpdatedBy
		}
	}
	return M{
		"code": definition.Code, "label": definition.Label, "description": definition.Description,
		"parameterSchema":        definition.ParameterSchema,
		"schemaDefaultParams":    extractSchemaDefaultParams(definition.ParameterSchema),
		"configuredOverrides":    overrides,
		"effectiveDefaultParams": buildEffectiveDefaultParams(definition.ParameterSchema, overrides),
		"updatedAt":              updatedAt, "updatedBy": updatedBy,
	}
}
