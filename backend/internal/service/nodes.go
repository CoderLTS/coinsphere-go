// nodes.go —— 工作流"节点"注册表的框架代码。
//
// 注册表模式(registry pattern)是本文件的核心:与其写一大串 if/switch 判断"是什么类型就干什么",
// 不如把每种类型和它的处理函数成对登记进一张表,用时按编码查表、拿到函数直接调用。
//
// 本文件放"框架";具体的内置节点处理器(start/task/http/notify/condition/foreach…)在
// nodes_builtin.go 里用 init() 调 registerNode 登记进来。以后加一种节点 = 新开一个文件 +
// 一个 init(),不用改动本文件,也不用改引擎(engine.go)、不用改校验器(workflowdef.go)——
// 后两者需要知道的"这个节点在图上怎么连线",由登记项上的 Kind / Branches 声明。
//
// 相关文件:taskdef.go 是另一张注册表(可执行"任务",供 task.run 节点调用);
// schema.go 是两张表共用的 JSON Schema 小工具。
// 所谓"代码即真源":这些能力直接写死在代码里,不依赖数据库配置。

package service

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"coinsphere/backend/internal/db"
)

// nodeExecResult 节点执行结果。
//
// engine.go 跑图时会读这几个字段决定下一步:
//
//	Output         节点输出数据(存成输出快照,也可被下游引用);
//	SelectedBranch 条件节点选中的分支名(*string 指针:nil 表示"没选分支",全部出边都点亮);
//	ForeachItems   循环节点要逐个遍历的数组(非 nil 就触发 foreach,引擎按元素跑一遍循环体子图);
//	ItemKey/IndexKey  foreach 把"当前元素/下标"写进共享状态时用的变量名(供循环体内的节点引用)。
type nodeExecResult struct {
	Output         M
	SelectedBranch *string
	ForeachItems   []any
	ItemKey        string
	IndexKey       string
}

// nodeExecContext 节点执行上下文。
//
// engine.go 每执行一个节点,就把这个"上下文包"传给它的 Execute。
//
//	Ctx    执行级 context:整图被取消(如别的并行分支失败)或超时会经它通知,长耗时节点(http/delay)应遵守;
//	State  并发安全的共享状态(见 runState):节点之间靠它传数据,读写都要走它的方法,不能直接碰底层 map;
//	PublishEvent 引擎注入的回调:节点想发领域事件时调它就行,不必关心底层怎么发。
type nodeExecContext struct {
	Ctx          context.Context
	App          *App
	Definition   *db.WorkflowDefinition
	RuntimeEntry *db.WorkflowRuntimeEntry
	Execution    *db.WorkflowExecution
	NodeLog      *db.WorkflowExecutionNode
	Node         M
	Graph        M
	State        *runState
	TriggerCtx   M
	PublishEvent func(eventType, aggregateType string, payload, metadata M) (int64, error)
}

// runState 跑图全程共享的"变量表",并发安全封装。
//
// 真并发执行下多个节点 goroutine 会同时读写同一张表,裸 map 并发读写会直接 panic,
// 所以所有访问都过一把读写锁(sync.RWMutex:读多写少时读锁可并行、写锁独占)。见 GO入门笔记『并发』。
// ponytail: 存进表里的输出值一律当"只读"对待——节点应构造好新 map 再 set,不要 set 之后再原地改它;
//
//	否则读侧脱锁后仍可能读到半改状态。要就地改就得升级为深拷贝返回。
type runState struct {
	mu   sync.RWMutex
	data M
}

func newRunState(initial M) *runState { return &runState{data: initial} }

// get 按点号路径读一个值(读锁)。
func (s *runState) get(path string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return readPath(s.data, path)
}

// load 读一个顶层键并返回它是否存在(读锁)。
func (s *runState) load(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return value, ok
}

// set 写一个顶层键(写锁)。
func (s *runState) set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// restore 与 load 配对:原来存在旧值就写回,否则删键(写锁)——给 foreach 收尾还原循环变量用。
func (s *runState) restore(key string, previous any, existed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existed {
		s.data[key] = previous
	} else {
		delete(s.data, key)
	}
}

// setNodeOutput 把某节点输出按节点 id 存进 nodeOutputs(写锁,惰性建表),方便下游按 id 取用。
func (s *runState) setNodeOutput(nodeID string, output M) {
	s.mu.Lock()
	defer s.mu.Unlock()
	outputs, _ := s.data["nodeOutputs"].(map[string]any)
	if outputs == nil {
		outputs = M{}
		s.data["nodeOutputs"] = outputs
	}
	outputs[nodeID] = output
}

// snapshot 浅拷贝一份顶层键(读锁),给需要"定格当前状态"的场景(如 notify 模板渲染)用。
func (s *runState) snapshot() M {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make(M, len(s.data))
	for key, value := range s.data {
		copied[key] = value
	}
	return copied
}

// snapshotJSON 在读锁内把整张表序列化成 JSON(截断到上限),给节点输入/输出快照落库用。
func (s *runState) snapshotJSON(maxBytes int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return serializeSnapshot(s.data, maxBytes)
}

// 节点"图语义"分类。校验器(workflowdef.go)靠它决定一个节点的出边该怎么查,
// 而不是把 "end" / "condition.branch" / "foreach" 这些具体类型编码写死在校验代码里。
// 新增一种节点时只要在 registerNode 里声明 Kind(必要时加 Branches),校验器自动适配。
const (
	nodeKindPlain    = "plain"    // 普通节点:出边不限,全部点亮
	nodeKindStart    = "start"    // 起始节点:无入边、至少一条出边、需要 entryKey
	nodeKindBranch   = "branch"   // 分支节点:出边按 branchKey 分流,必须覆盖 Branches 声明的全部分支
	nodeKindLoop     = "loop"     // 循环节点:一条出边进循环体,可选一条 branch="next" 的循环后继
	nodeKindTerminal = "terminal" // 终止节点:不能有出边
)

// loopNextBranch 循环节点"循环结束后继续"那条出边的 branchKey。
// 其余出边视为循环体入口(老图里只有一条无 branch 的出边,天然落在这一类,不需要迁移)。
const loopNextBranch = "next"

// appendTo 往某个顶层键的数组里追加一个值(写锁);键不存在或不是数组就先建成数组。
// 给 state.append 节点用:foreach 每轮把当轮结果攒进同一个数组,循环跑完就能整体汇总。
// 追加时构造新切片而不是原地 append —— 读侧拿到的旧切片可能正在被别处使用,不能就地改。
func (s *runState) appendTo(key string, value any) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, _ := s.data[key].([]any)
	next := make([]any, 0, len(existing)+1)
	next = append(next, existing...)
	next = append(next, value)
	s.data[key] = next
	return next
}

// workflowNodeDefinition 一种工作流节点类型的登记项:把类型编码 TypeCode 和它的处理函数 Execute 绑在一起。
// Execute 的类型 func(ctx *nodeExecContext) (*nodeExecResult, error) 就是所有节点统一的"处理器"签名。
//
// Kind / Branches 是给校验器看的"图语义声明":
//
//	Kind      见上面 nodeKindXxx 常量,不填按 plain 处理;
//	Branches  仅 Kind=branch 时有意义,列出这种节点必须、且只能有的分支键(如 ["true","false"])。
//
// 有些分支节点的分支数不是固定的(多路 switch 每个实例的分支都不一样),这时用
// BranchesConfigKey + ExtraBranches 声明"分支从节点自己的配置里读":
//
//	BranchesConfigKey  节点 config 里那个数组字段的名字,数组每项的 "key" 就是一个分支键;
//	ExtraBranches      动态分支之外总是存在的分支(如 switch 的 default)。
//
// 这套声明同时下发给前端(ListNodeDefinitions),画布按它生成端口、校验器按它查出边,
// 两边只依赖同一份声明,不各写一遍解析逻辑。
type workflowNodeDefinition struct {
	TypeCode          string
	Label             string
	Kind              string
	Branches          []string
	BranchesConfigKey string
	ExtraBranches     []string
	ConfigSchema      M
	Execute           func(ctx *nodeExecContext) (*nodeExecResult, error)
}

// kind 取节点的图语义分类;没显式声明就按普通节点处理。
func (d *workflowNodeDefinition) kind() string {
	if d.Kind == "" {
		return nodeKindPlain
	}
	return d.Kind
}

// hasDynamicBranches 分支是否要从节点配置里解析。
func (d *workflowNodeDefinition) hasDynamicBranches() bool { return d.BranchesConfigKey != "" }

// resolveBranches 算出某个节点实例实际应有的分支键。
// 静态声明的直接返回 Branches;动态的从 config 里那个数组逐项取 key,去空去重,最后补上 ExtraBranches。
func (d *workflowNodeDefinition) resolveBranches(config M) []string {
	if !d.hasDynamicBranches() {
		return d.Branches
	}
	branches := make([]string, 0, 4)
	seen := map[string]bool{}
	appendBranch := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		branches = append(branches, key)
	}
	items, _ := config[d.BranchesConfigKey].([]any)
	for _, itemAny := range items {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		appendBranch(asString(item["key"]))
	}
	for _, key := range d.ExtraBranches {
		appendBranch(key)
	}
	return branches
}

// 注册表本体:map 供 O(1) 按编码查处理器,slice 记登记顺序(保证列表接口输出稳定)。
var (
	workflowNodeRegistry = map[string]*workflowNodeDefinition{}
	workflowNodeOrder    []*workflowNodeDefinition
)

// registerNode 登记一种节点类型。各内置节点在自己文件的 init() 里调用它;
// 编码重复属于开发期错误,直接 panic 提醒(而不是悄悄覆盖)。
func registerNode(definition *workflowNodeDefinition) {
	if _, exists := workflowNodeRegistry[definition.TypeCode]; exists {
		panic("duplicate workflow node type: " + definition.TypeCode)
	}
	if definition.kind() == nodeKindBranch && !definition.hasDynamicBranches() && len(definition.Branches) < 2 {
		panic("branch node must declare at least two branches: " + definition.TypeCode)
	}
	workflowNodeRegistry[definition.TypeCode] = definition
	workflowNodeOrder = append(workflowNodeOrder, definition)
}

// getNodeDefinition 按 typeCode 查处理器。engine.go 跑图时靠它把"节点类型"变成"要调用的函数";
// workflowdef.go 校验时也用它,让"图里写了个不存在的节点类型"在落库阶段就被拦下,而不是跑到一半才炸。
func getNodeDefinition(typeCode string) (*workflowNodeDefinition, error) {
	if definition, ok := workflowNodeRegistry[typeCode]; ok {
		return definition, nil
	}
	return nil, bizErr("Unknown workflow node type: %s", typeCode)
}

// nodeKindOf 查某节点类型的图语义分类;类型未登记时返回空串。
func nodeKindOf(typeCode string) string {
	if definition, ok := workflowNodeRegistry[typeCode]; ok {
		return definition.kind()
	}
	return ""
}

// isStartNodeType 判断某节点类型是不是"起始节点"——直接问注册表,不再另维护一份类型名单。
func isStartNodeType(typeCode string) bool { return nodeKindOf(typeCode) == nodeKindStart }

// ListNodeDefinitions 节点定义列表(编辑器面板用),按登记顺序输出。
// kind/branches 一并给前端:分支端口、循环后继端口这些都能按声明渲染,不用在前端再写一份类型名单。
func (a *App) ListNodeDefinitions() []M {
	result := make([]M, 0, len(workflowNodeOrder))
	for _, definition := range workflowNodeOrder {
		branches := definition.Branches
		if branches == nil {
			branches = []string{}
		}
		result = append(result, M{
			"typeCode": definition.TypeCode, "label": definition.Label, "configSchema": definition.ConfigSchema,
			"kind": definition.kind(), "branches": branches,
			"branchesConfigKey": definition.BranchesConfigKey, "extraBranches": definition.ExtraBranches,
		})
	}
	return result
}

// ---------- 节点处理器公用小工具 ----------

// nodeConfig 取节点的 config 对象,并把里面的字符串统一做一次 {{路径}} 模板渲染。
//
// 渲染让所有节点配置都能引用运行时状态:http 的 url 可以写 "https://x/api/{{currentItem.id}}",
// event.publish 的 eventType 可以按上游输出动态拼,condition 的比较值可以取另一个路径。
// 渲染只作用于字符串(含嵌套 map / 数组里的字符串),数字、布尔原样保留;
// 模板里引用不到的路径渲染成空串(与通知模板一致)。
func nodeConfig(ctx *nodeExecContext) M {
	config, _ := ctx.Node["config"].(map[string]any)
	if len(config) == 0 {
		return M{}
	}
	rendered, _ := renderConfigValue(config, ctx.State.snapshot()).(map[string]any)
	if rendered == nil {
		return M{}
	}
	return rendered
}

// renderConfigValue 递归渲染配置值里的字符串;map / slice 都返回新容器,不改动原始 config
// (原始 config 属于工作流定义,跑图期间必须保持只读)。
func renderConfigValue(value any, variables M) any {
	switch typed := value.(type) {
	case string:
		return renderTemplate(typed, variables)
	case map[string]any:
		result := make(M, len(typed))
		for key, item := range typed {
			result[key] = renderConfigValue(item, variables)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = renderConfigValue(item, variables)
		}
		return result
	default:
		return value
	}
}

// rawNodeConfig 取节点原始 config(不渲染模板)。校验器与调度注册这类"看定义不看运行时"的场景用它。
func rawNodeConfig(node M) M {
	config, _ := node["config"].(map[string]any)
	if config == nil {
		return M{}
	}
	return config
}

// cfgStr 读字符串配置项并去空白;为空则用 fallback。
func cfgStr(config M, key, fallback string) string {
	if value := strings.TrimSpace(asString(config[key])); value != "" {
		return value
	}
	return fallback
}

// cfgInt 读整数配置项;缺省或 <= 0 时用 fallback。
func cfgInt(config M, key string, fallback int64) int64 {
	if value := asInt64(config[key]); value > 0 {
		return value
	}
	return fallback
}

// setNodeOutput 把当前节点输出按节点 id 存进共享状态的 nodeOutputs,方便下游按 id 引用。
func setNodeOutput(ctx *nodeExecContext, output M) {
	ctx.State.setNodeOutput(asString(ctx.Node["id"]), output)
}

// compareValues 按 operator 比较两个值:
// truthy 看真假;eq/ne 按文本比;contains 按子串(左值是数组时按元素比);其余(gt/gte/lt/lte)按数字比。
func compareValues(actual, expected any, operator string) bool {
	switch operator {
	case "truthy":
		return isTruthy(actual)
	case "eq":
		return pyStr(actual) == pyStr(expected)
	case "ne":
		return pyStr(actual) != pyStr(expected)
	case "contains":
		// 左值是数组就看有没有相等的元素,否则退化成字符串包含。
		if items, ok := actual.([]any); ok {
			for _, item := range items {
				if pyStr(item) == pyStr(expected) {
					return true
				}
			}
			return false
		}
		return strings.Contains(pyStr(actual), pyStr(expected))
	}
	actualNumber, okActual := toFloatFlexible(actual)
	expectedNumber, okExpected := toFloatFlexible(expected)
	if !okActual || !okExpected {
		return false
	}
	switch operator {
	case "gt":
		return actualNumber > expectedNumber
	case "gte":
		return actualNumber >= expectedNumber
	case "lt":
		return actualNumber < expectedNumber
	case "lte":
		return actualNumber <= expectedNumber
	default:
		return false
	}
}

// isTruthy 判断一个值是否"为真"(仿脚本语言:nil/空串/0/空数组/空 map 都算假)。
func isTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case int64:
		return typed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

// pyStr 与 Python str() 对齐的比较文本(数值不带多余小数)。
func pyStr(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		return asString(value)
	}
}

func toFloatFlexible(value any) (float64, bool) {
	if number, ok := toFloat(value); ok {
		return number, true
	}
	if text, ok := value.(string); ok {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}
