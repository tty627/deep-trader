package main

import "time"

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`             // 交易对，如 "BTCUSDT"
	Side             string  `json:"side"`               // 持仓方向: "long" (多) or "short" (空)
	EntryPrice       float64 `json:"entry_price"`        // 平均开仓价格
	MarkPrice        float64 `json:"mark_price"`         // 当前标记价格
	Quantity         float64 `json:"quantity"`           // 持仓数量 (币的个数，如 0.1 BTC)
	Leverage         int     `json:"leverage"`           // 杠杆倍数
	UnrealizedPnL    float64 `json:"unrealized_pnl"`     // 未实现盈亏 (USDT)
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"` // 未实现盈亏百分比 (基于保证金)
	PeakPnLPct       float64 `json:"peak_pnl_pct"`       // 历史最高收益率（百分比）
	LiquidationPrice float64 `json:"liquidation_price"`  // 预估强平价格
	MarginUsed       float64 `json:"margin_used"`        // 仓位占用的保证金 (USDT)
	UpdateTime       int64   `json:"update_time"`        // 持仓更新时间戳（毫秒）
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户总净值 (可用余额 + 占用保证金 + 未实现盈亏)
	AvailableBalance float64 `json:"available_balance"` // 可用余额 (可用于开新仓的资金)
	UnrealizedPnL    float64 `json:"unrealized_pnl"`    // 所有持仓的总未实现盈亏
	TotalPnL         float64 `json:"total_pnl"`         // 历史已实现总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比 (相对于初始资金)
	MarginUsed       float64 `json:"margin_used"`       // 当前所有持仓占用的保证金总额
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率 (MarginUsed / TotalEquity * 100)
	PositionCount    int     `json:"position_count"`    // 当前持仓数量
}

// IntradayData 日内序列数据 (3分钟)
type IntradayData struct {
	MidPrices   []float64
	EMA20Values []float64
	MACDValues  []float64
	RSI7Values  []float64
	RSI14Values []float64
	Volume      []float64
	ATR14       float64
}

// LongerTermData 长期上下文 (4小时)
type LongerTermData struct {
	EMA20         float64
	EMA50         float64
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64
	MACDValues    []float64
	RSI14Values   []float64
}

// OIData 持仓量数据
type OIData struct {
	Latest  float64
	Average float64 // 可以弃用或保留作为长期均值
	Change1h float64 // 1小时持仓量变化百分比
	Change4h float64 // 4小时持仓量变化百分比 (若有数据)
}

// SectorInfo 板块信息
type SectorInfo struct {
	Name          string
	Symbols       []string
	AvgChange1h   float64
	AvgChange4h   float64
	LeadingSymbol string // 领涨/领跌币种
}

// LongShortData 多空比数据
type LongShortData struct {
	Ratio    float64 `json:"ratio"`     // 多空比值
	LongPct  float64 `json:"long_pct"` // 多头占比
	ShortPct float64 `json:"short_pct"` // 空头占比
}

// LiquidationData 爆仓数据
type LiquidationData struct {
	Symbol    string
	Amount1h  float64 // 1小时内爆仓金额 (USDT)
	Amount4h  float64 // 4小时内爆仓金额
	SideRatio float64 // 爆多/爆空比例 (例如 >1 为多头爆仓多)
}

// VolumeAnalysis 成交量分析结果
type VolumeAnalysis struct {
	RelativeVolume3m  float64 `json:"relative_volume_3m"`   // 当前3m成交量 / 过去若干根平均成交量
	TakerBuySellRatio float64 `json:"taker_buy_sell_ratio"` // 主动买卖比率 (>1 表示主动买盘占优)
	IsVolumeSpike     bool    `json:"is_volume_spike"`      // 是否为巨量K线
}

// SentimentData 情绪与波动率信息
type SentimentData struct {
	FearGreedIndex int     `json:"fear_greed_index"` // 0-100 的本地恐惧/贪婪分数
	FearGreedLabel string  `json:"fear_greed_label"` // "Extreme Fear" / "Fear" / "Neutral" / "Greed" / "Extreme Greed"
	LocalSentiment string  `json:"local_sentiment"`  // 结合资金费率、多空比的标签：Bullish_Crowded / Bearish_Crowded / Neutral
	Volatility1h   float64 `json:"volatility_1h"`    // 最近1小时已实现波动率（简单标准差指标）
}

// MarketData 市场数据
type MarketData struct {
	Symbol         string
	Source         string  // 来源标签 (如 "AI500", "OI_Top")
	CurrentPrice   float64
	PriceChange1h  float64
	PriceChange4h  float64
	PriceChangeDay float64 // 日内价格变化 (从当日 00:00 UTC 开始)
	Volume24h      float64

	// 短周期（日内，3m）指标快照
	CurrentMACD  float64
	CurrentRSI7  float64
	CurrentEMA20 float64 // 新增
	
	// 5m 入场周期指标（更平滑的微观趋势）
	EMA20_5m  float64
	MACD_5m   float64
	RSI14_5m  float64
	ATR14_5m  float64
	
	// 15m 日内趋势周期指标（确认方向）
	EMA20_15m float64
	MACD_15m  float64
	RSI14_15m float64
	ATR14_15m float64
	
	// 1 小时中周期指标（用于桥接日内与4小时趋势）
	EMA20_1h float64
	MACD_1h  float64
	RSI14_1h float64
	ATR14_1h float64
	
	// 30m 辅助周期（可用于对比 15m/1h）
	EMA20_30m float64
	MACD_30m  float64
	RSI14_30m float64
	ATR14_30m float64
		
	// 新增：布林带数据（基于 3m）
	BollingerUpper  float64
	BollingerMiddle float64
	BollingerLower  float64

	FundingRate  float64
	OpenInterest *OIData

	LongShortRatio *LongShortData   // 新增：多空比数据
	Liquidation    *LiquidationData // 新增：爆仓数据

	// 新增：量能与情绪
	VolumeAnalysis *VolumeAnalysis `json:"volume_analysis,omitempty"`
	Sentiment      *SentimentData  `json:"sentiment,omitempty"`

	IntradaySeries    *IntradayData
	LongerTermContext *LongerTermData
}

// Context 交易上下文，传递给 AI 的核心数据结构
type Context struct {
	CurrentTime     string                 `json:"current_time"`    // 当前时间字符串
	RuntimeMinutes  int                    `json:"runtime_minutes"` // 程序运行分钟数
	CallCount       int                    `json:"call_count"`      // AI 调用计数
	Account         AccountInfo            `json:"account"`         // 账户当前状态
	Positions       []PositionInfo         `json:"positions"`       // 当前所有持仓
	Sectors         []SectorInfo           `json:"sectors"`         // 板块热度
	MarketDataMap   map[string]*MarketData `json:"-"`               // 全市场行情数据 (Map 便于查找)
	SharpeRatio     float64                `json:"sharpe_ratio"`    // 运行时夏普比率 (基于本次运行的资金曲线)
	BTCETHLeverage  int                    `json:"-"`               // BTC/ETH 最大杠杆配置
	AltcoinLeverage int                    `json:"-"`               // 山寨币 最大杠杆配置
}

// Decision AI的交易决策
// 说明：
// - Action 是主要的行为字段，仅支持少量标准值（open_long/open_short/close_long/close_short/...）；
// - Side 仅用于兼容模型可能输出的 "open_position" + "side" 方案，后端会在归一化阶段将其映射为标准 Action；
// - 其余未在协议中列出但模型可能输出的字段，一律在解析阶段静默忽略。
type Decision struct {
	Symbol string `json:"symbol"` // 交易对象
	Action string `json:"action"` // 动作: "open_long", "open_short", "close_long", "close_short", "wait", etc.

	// 可选：方向字段，仅用于兼容 "open_position" + "side" 风格的输出
	// 允许取值如 "long" / "short" / "buy" / "sell"，归一化逻辑会将其映射为标准 Action
	Side string `json:"side,omitempty"`

	// 开仓参数
	Leverage        int     `json:"leverage,omitempty"`            // 建议杠杆倍数
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`   // 建议名义仓位大小 (USDT)
	PositionPercent float64 `json:"position_percent,omitempty"`    // 可选：相对仓位(0-100 或 0-1)，用于按账户规模动态计算 position_size_usd
	StopLoss        float64 `json:"stop_loss,omitempty"`           // 建议止损价格
	TakeProfit      float64 `json:"take_profit,omitempty"`         // 建议止盈价格
	ProfitTarget    float64 `json:"profit_target,omitempty"`       // 兼容 nofx prompt

	// 调整参数
	NewStopLoss     float64 `json:"new_stop_loss,omitempty"`    // 新止损价格 (用于 update_stop_loss)
	NewTakeProfit   float64 `json:"new_take_profit,omitempty"`  // 新止盈价格 (用于 update_take_profit)
	ClosePercentage float64 `json:"close_percentage,omitempty"` // 平仓比例 (0-100, 用于 partial_close)

	// 执行结果（由本地实盘执行后填充，方便前端展示成功/失败）
	ExecStatus string `json:"exec_status,omitempty"` // "success" / "failed" 等
	ExecError  string `json:"exec_error,omitempty"`  // 失败时的错误信息

	// 通用参数
	// Confidence 允许使用 0-1 或 0-100 的小数，后端仅做展示，不参与风控计算
	Confidence            float64 `json:"confidence,omitempty"`             // AI 信心度 (0-1 或 0-100)
	RiskUSD               float64 `json:"risk_usd,omitempty"`               // 预估最大风险金额 (USDT)
	InvalidationCondition string  `json:"invalidation_condition,omitempty"` // 失效条件
	Reasoning             string  `json:"reasoning"`                        // 决策理由摘要
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"` // 发送给 AI 的系统提示词
	UserPrompt   string     `json:"user_prompt"`   // 发送给 AI 的用户提示词
	CoTTrace     string     `json:"cot_trace"`     // AI 的思维链 (Chain of Thought) 分析过程
	Decisions    []Decision `json:"decisions"`     // AI 输出的具体决策列表
	Timestamp    time.Time  `json:"timestamp"`     // 决策生成时间
}

// TradeRecord 历史交易记录
type TradeRecord struct {
	Time      string  `json:"time"`       // 平仓时间
	Symbol    string  `json:"symbol"`     // 交易对
	Side      string  `json:"side"`       // 方向 (long/short)
	Action    string  `json:"action"`     // 操作 (close_long/partial_close...)
	EntryPrice float64 `json:"entry_price"` // 入场均价
	ExitPrice  float64 `json:"exit_price"`  // 平仓价格 (近似)
	Quantity   float64 `json:"quantity"`    // 平仓数量
	PnL        float64 `json:"pnl"`         // 实现盈亏 (USDT)
	PnLPct     float64 `json:"pnl_pct"`     // 收益率%
	Reason     string  `json:"reason"`      // 平仓原因/备注
}
