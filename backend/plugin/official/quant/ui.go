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
	"official.quant.volume_spike_condition": 10,
	"official.quant.price_change_condition": 20,
	"official.quant.macd_condition":         30,
	"official.quant.kdj_condition":          40,
	"official.quant.rsi_condition":          50,
	"official.quant.bollinger_condition":    60,
	"official.quant.evaluate":               10,
	"official.quant.code_strategy":          20,
	"official.quant.position":               30,
	"official.quant.output_signal":          40,
	"official.quant.market_signal":          70,
	"official.quant.order_intent":           50,
	"official.quant.backtest_start":         10,
	"official.quant.backtest":               60,
}

var quantNodeAliases = map[string][]string{
	"official.quant.volume_spike_condition": {"成交量放大", "放量", "量能"},
	"official.quant.price_change_condition": {"涨跌幅", "价格变化", "波动率"},
	"official.quant.macd_condition":         {"MACD", "指数平滑异同移动平均线"},
	"official.quant.kdj_condition":          {"KDJ", "随机指标"},
	"official.quant.rsi_condition":          {"RSI", "相对强弱指标"},
	"official.quant.bollinger_condition":    {"布林带", "BOLL"},
	"official.quant.evaluate":               {"策略评估", "量化计算"},
	"official.quant.code_strategy":          {"CEL 策略", "代码判断"},
	"official.quant.position":               {"目标仓位", "仓位转换"},
	"official.quant.output_signal":          {"策略信号", "信号输出"},
	"official.quant.market_signal":          {"行情信号", "指标信号"},
	"official.quant.order_intent":           {"订单意图", "交易意图"},
	"official.quant.backtest_start":         {"回测入口", "回测开始"},
	"official.quant.backtest":               {"策略回测", "历史回测"},
}

var quantNodeTags = map[string][]string{
	"official.quant.volume_spike_condition": {"指标", "条件", "量化"},
	"official.quant.price_change_condition": {"指标", "条件", "量化"},
	"official.quant.macd_condition":         {"指标", "条件", "量化"},
	"official.quant.kdj_condition":          {"指标", "条件", "量化"},
	"official.quant.rsi_condition":          {"指标", "条件", "量化"},
	"official.quant.bollinger_condition":    {"指标", "条件", "量化"},
	"official.quant.evaluate":               {"策略", "量化"},
	"official.quant.code_strategy":          {"策略", "CEL", "条件"},
	"official.quant.position":               {"策略", "仓位"},
	"official.quant.output_signal":          {"策略", "信号"},
	"official.quant.market_signal":          {"行情", "信号"},
	"official.quant.order_intent":           {"策略", "订单"},
	"official.quant.backtest_start":         {"回测", "入口"},
	"official.quant.backtest":               {"回测", "策略"},
}
