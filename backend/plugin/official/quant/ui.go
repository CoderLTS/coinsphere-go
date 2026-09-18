package quant

import "coinsphere/backend/plugin/sdk"

func quantNodeMeta(desc sdk.NodeDescriptor, title, description, category, color, icon string) sdk.NodeDescriptor {
	desc.Title, desc.Description, desc.Category, desc.Color, desc.Icon = title, description, category, color, icon
	desc.Aliases = append([]string{title}, quantNodeAliases[desc.Type]...)
	desc.Tags = append([]string{category}, quantNodeTags[desc.Type]...)
	desc.SortOrder = quantNodeOrder[desc.Type]
	if desc.SortOrder == 0 {
		desc.SortOrder = 100
	}
	desc.Width, desc.Height = 220, 72
	desc.Capabilities.Stateless = desc.State == sdk.StateStateless
	desc.Capabilities.Deterministic = desc.Capabilities.Deterministic || desc.SideEffect == sdk.SideEffectNone
	return desc
}

var quantNodeOrder = map[string]int{
	"official.quant.indicator":     10,
	"official.quant.evaluate":      20,
	"official.quant.code_strategy": 30,
	"official.quant.position":      40,
	"official.quant.output_signal": 50,
	"official.quant.market_signal": 60,
	"official.quant.order_intent":  70,
	"official.quant.replay":        10,
}

var quantNodeAliases = map[string][]string{
	"official.quant.indicator":     {"RSI", "MACD", "KDJ", "布林带", "放量", "价格波动"},
	"official.quant.evaluate":      {"策略评估", "量化计算"},
	"official.quant.code_strategy": {"CEL 策略", "代码判断"},
	"official.quant.position":      {"目标仓位", "仓位转换"},
	"official.quant.output_signal": {"策略信号", "信号输出"},
	"official.quant.market_signal": {"行情信号", "指标信号"},
	"official.quant.order_intent":  {"订单意图", "交易意图"},
	"official.quant.replay":        {"历史模拟"},
}

var quantNodeTags = map[string][]string{
	"official.quant.indicator":     {"指标", "条件", "量化"},
	"official.quant.evaluate":      {"策略", "量化"},
	"official.quant.code_strategy": {"策略", "CEL", "条件"},
	"official.quant.position":      {"策略", "仓位"},
	"official.quant.output_signal": {"策略", "信号"},
	"official.quant.market_signal": {"行情", "信号"},
	"official.quant.order_intent":  {"策略", "订单"},
	"official.quant.replay":        {"回放", "操作"},
}
