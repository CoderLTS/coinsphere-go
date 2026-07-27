package service

import (
	"strings"

	"github.com/robfig/cron/v3"
)

// quartz 6 位 cron 解析器:秒 分 时 日 月 周。
var quartzParser = cron.NewParser(
	cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// normalizeQuartzCron 规范化 Quartz 6 位表达式('?' 归一为 '*')。
func normalizeQuartzCron(expression string) (string, error) {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(expression)), " ")
	if normalized == "" {
		return "", bizErr("Cron 表达式不能为空")
	}
	parts := strings.Split(normalized, " ")
	if len(parts) != 6 {
		return "", bizErr("当前仅支持 Quartz 6 位 Cron 表达式:秒 分 时 日 月 周")
	}
	if parts[3] == "?" {
		parts[3] = "*"
	}
	if parts[5] == "?" {
		parts[5] = "*"
	}
	return strings.Join(parts, " "), nil
}

// parseQuartzCron 解析为可计算下次触发时间的 Schedule。
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
