package service

import (
	"strings"

	"github.com/robfig/cron/v3"
)

// quartz 6 位 cron 解析器:秒 分 时 日 月 周。
//
// 见 GO入门笔记『框架:robfig/cron』。
// robfig/cron 是专门解析 cron 表达式、并推算"下一次该在哪个时刻触发"的第三方库。
// cron.NewParser(...) 造一个解析器,参数里用按位或 | 把要启用的字段"叠"在一起(每个 cron.Xxx
// 都是一个位标志):这里启用了 秒|分|时|日|月|周 共 6 个字段,即 Quartz 风格的 6 位表达式
// (普通 Unix cron 只有 5 位、没有"秒")。Dom = day of month(日),Dow = day of week(周)。
var quartzParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// normalizeQuartzCron 规范化 Quartz 6 位表达式('?' 归一为 '*')。
func normalizeQuartzCron(expression string) (string, error) {
	// strings.Fields 按连续空白把字符串拆成若干段并丢掉空串,再用单个空格 Join 拼回去——
	// 目的是把用户输入里多余/不规则的空格压平成标准的"6 段、用单空格隔开"。
	normalized := strings.Join(strings.Fields(strings.TrimSpace(expression)), " ")
	if normalized == "" {
		return "", bizErr("Cron 表达式不能为空")
	}
	parts := strings.Split(normalized, " ")
	// 拆开后必须正好 6 段,否则就不是合法的 Quartz 6 位表达式。
	if len(parts) != 6 {
		return "", bizErr("当前仅支持 Quartz 6 位 Cron 表达式:秒 分 时 日 月 周")
	}
	// Quartz 里"日"(第 4 段,下标 3)和"周"(第 6 段,下标 5)常用 '?' 表示"这个字段不指定";
	// 但 robfig/cron 不认识 '?',所以统一换成 '*'(表示"任意")。切片下标从 0 开始计数。
	if parts[3] == "?" {
		parts[3] = "*"
	}
	if parts[5] == "?" {
		parts[5] = "*"
	}
	return strings.Join(parts, " "), nil
}

// parseQuartzCron 解析为可计算下次触发时间的 Schedule。
//
// 先规范化,再交给解析器 Parse,得到一个 cron.Schedule。cron.Schedule 是个接口(interface,
// 只约定"能做什么"),它的核心方法是 Next(t):给它"当前时间"就返回"下一次触发时间"——
// 调度循环正是靠反复调用它来算出每个任务下次几点该跑。%v 是把 err 拼进提示串的格式化占位符。
func parseQuartzCron(expression string) (cron.Schedule, error) {
	normalized, err := normalizeQuartzCron(expression)
	if err != nil {
		return nil, err
	}
	schedule, err := quartzParser.Parse(normalized)
	if err != nil {
		return nil, bizErr("Cron 表达式不合法: %v", err)
	}
	return schedule, nil
}
