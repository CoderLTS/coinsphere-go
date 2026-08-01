// agentsource.go —— 智能体"数据源"注册表。
//
// 一个智能体除了系统提示词,还可以带一段动态上下文:比如新闻分析智能体要把当前这条新闻的
// 标题正文塞进提示词。这类"上下文从哪来、怎么拼"原先是 assistant.go 里的一个 switch,
// 加一种数据源要同时改常量表、白名单、switch、参数校验和前端下拉五处。
//
// 这里改成和工作流节点同一个套路:一种数据源 = 一条 registerAgentDataSource 登记。
// 登记项自己说明「叫什么」「要不要外部 id」「上下文怎么拼」「分析模式追加什么指令」,
// 校验、下拉选项、会话标题都从注册表推导。
//
// 所谓"代码即真源":数据源能力写死在代码里,数据库只存某个智能体选了哪一种。

package service

import (
	"strings"

	"coinsphere/backend/internal/db"
)

// 内置数据源编码。
const (
	agentDataSourceNone          = "none"
	agentDataSourceSystemContext = "system_context"
	agentDataSourceNewsContext   = "news_context"
)

// agentContext 一次上下文解析的结果。
//
//	Text  拼进系统提示词的上下文正文,空串表示这次没有额外上下文;
//	Title 用来做会话标题的短文本(如新闻标题),空串表示沿用智能体名称。
type agentContext struct {
	Text  string
	Title string
}

// agentDataSource 一种数据源的登记项。
//
//	RequiresRefID  为 true 时,调用方必须给出外部实体 id(会话里存在 news_id 列),否则报错;
//	BuildContext   按 refID 取数据并拼成上下文;refID 可能为 nil(不需要外部 id 的数据源);
//	AnalyzePrompt  非空表示这种数据源支持"结构化分析"模式,值就是分析模式下追加的用户指令。
type agentDataSource struct {
	Code          string
	Label         string
	RequiresRefID bool
	BuildContext  func(a *App, refID *int64) (agentContext, error)
	AnalyzePrompt string
}

// supportsAnalyze 这种数据源是否支持"结构化分析"模式。
func (s *agentDataSource) supportsAnalyze() bool { return s != nil && s.AnalyzePrompt != "" }

var (
	agentDataSourceRegistry = map[string]*agentDataSource{}
	agentDataSourceOrder    []*agentDataSource
)

// registerAgentDataSource 登记一种数据源。编码重复属于开发期错误,直接 panic。
func registerAgentDataSource(source *agentDataSource) {
	if _, exists := agentDataSourceRegistry[source.Code]; exists {
		panic("duplicate agent data source: " + source.Code)
	}
	agentDataSourceRegistry[source.Code] = source
	agentDataSourceOrder = append(agentDataSourceOrder, source)
}

// getAgentDataSource 按编码查数据源;查不到返回 nil(调用方据此判断编码是否合法)。
func getAgentDataSource(code string) *agentDataSource {
	if code == "" {
		return agentDataSourceRegistry[agentDataSourceNone]
	}
	return agentDataSourceRegistry[code]
}

// listAgentDataSourceOptions 配置页下拉选项,按登记顺序输出。
func listAgentDataSourceOptions() []M {
	options := make([]M, 0, len(agentDataSourceOrder))
	for _, source := range agentDataSourceOrder {
		options = append(options, M{
			"value": source.Code, "label": source.Label,
			"requiresRefId": source.RequiresRefID, "supportsAnalyze": source.supportsAnalyze(),
		})
	}
	return options
}

func init() {
	registerAgentDataSource(&agentDataSource{
		Code:  agentDataSourceNone,
		Label: "无额外数据源",
		BuildContext: func(_ *App, _ *int64) (agentContext, error) {
			return agentContext{}, nil
		},
	})

	registerAgentDataSource(&agentDataSource{
		Code:  agentDataSourceSystemContext,
		Label: "系统上下文",
		BuildContext: func(_ *App, _ *int64) (agentContext, error) {
			return agentContext{Text: systemContextPrompt}, nil
		},
	})

	registerAgentDataSource(&agentDataSource{
		Code:          agentDataSourceNewsContext,
		Label:         "新闻上下文",
		RequiresRefID: true,
		BuildContext:  buildNewsAgentContext,
		AnalyzePrompt: newsAnalyzePrompt,
	})
}

// systemContextPrompt 告诉模型本系统有哪些能力,避免它凭空编造功能。
// 放在这里而不是散在业务代码里:改文案只动这一处。
const systemContextPrompt = "当前系统是 coinsphere,主要包含首页总览、定时任务、数据管理、配置管理、系统管理、" +
	"站内通知、新闻同步和智能助手等能力。回答时请优先围绕这些真实功能展开。"

// newsAnalyzePrompt 新闻结构化分析的输出要求。
const newsAnalyzePrompt = "请对这条新闻做结构化分析,并遵守以下要求:\n" +
	"1. 第一行必须以【利多】或【利空】开头。\n" +
	"2. 使用 3 点说明判断理由。\n" +
	"3. 给出 3 条具体可执行的后续建议。\n" +
	"4. 总字数控制在 500 字以内。"

func buildNewsAgentContext(a *App, refID *int64) (agentContext, error) {
	var news db.BlockbeatsNews
	if err := a.DB.First(&news, *refID).Error; err != nil {
		return agentContext{}, bizErr("新闻不存在或已删除")
	}
	text := "当前新闻上下文如下:\n" +
		"标题:" + news.Title + "\n" +
		"发布时间:" + fmtTime(news.PublishedAt) + "\n" +
		"正文:" + strings.TrimSpace(news.Content) + "\n" +
		"原文链接:" + news.OriginalURL
	return agentContext{Text: text, Title: news.Title}, nil
}

// resolveAgentContext 按智能体配置的数据源解析上下文。
// refID 是"外部实体 id"(目前只有新闻用到,存在会话的 news_id 列);
// 数据源声明了 RequiresRefID 却没给,直接报错,不让它带着空上下文去问模型。
func (a *App) resolveAgentContext(agent *db.AssistantAgent, refID *int64) (agentContext, error) {
	source := getAgentDataSource(agent.DataSourceType)
	if source == nil {
		return agentContext{}, bizErr("智能体 %s 配置了未知的数据源类型: %s", agent.Code, agent.DataSourceType)
	}
	if source.RequiresRefID && (refID == nil || *refID <= 0) {
		return agentContext{}, bizErr("智能体 %s 需要指定关联数据(%s)", agent.Code, source.Label)
	}
	return source.BuildContext(a, refID)
}
