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
	Average float64
}

// MarketData 市场数据
type MarketData struct {
	Symbol        string
	Source        string // 来源标签 (如 "AI500", "OI_Top")
	CurrentPrice  float64
	PriceChange1h float64
	PriceChange4h float64
	Volume24h     float64

	CurrentMACD  float64
	CurrentRSI7  float64
	CurrentEMA20 float64 // 新增

	FundingRate  float64
	OpenInterest *OIData

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
	MarketDataMap   map[string]*MarketData `json:"-"`               // 全市场行情数据 (Map 便于查找)
	SharpeRatio     float64                `json:"sharpe_ratio"`    // 运行时夏普比率 (基于本次运行的资金曲线)
	BTCETHLeverage  int                    `json:"-"`               // BTC/ETH 最大杠杆配置
	AltcoinLeverage int                    `json:"-"`               // 山寨币 最大杠杆配置
}

// Decision AI的交易决策
type Decision struct {
	Symbol string `json:"symbol"` // 交易对象
	Action string `json:"action"` // 动作: "open_long", "open_short", "close_long", "close_short", "wait", etc.

	// 开仓参数
	Leverage        int     `json:"leverage,omitempty"`          // 建议杠杆倍数
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"` // 建议仓位大小 (USDT)
	StopLoss        float64 `json:"stop_loss,omitempty"`         // 建议止损价格
	TakeProfit      float64 `json:"take_profit,omitempty"`       // 建议止盈价格
	ProfitTarget    float64 `json:"profit_target,omitempty"`     // 兼容 nofx prompt

	// 调整参数
	NewStopLoss     float64 `json:"new_stop_loss,omitempty"`    // 新止损价格 (用于 update_stop_loss)
	NewTakeProfit   float64 `json:"new_take_profit,omitempty"`  // 新止盈价格 (用于 update_take_profit)
	ClosePercentage float64 `json:"close_percentage,omitempty"` // 平仓比例 (0-100, 用于 partial_close)

	// 执行结果（由本地实盘执行后填充，方便前端展示成功/失败）
	ExecStatus string `json:"exec_status,omitempty"` // "success" / "failed" 等
	ExecError  string `json:"exec_error,omitempty"`  // 失败时的错误信息

	// 通用参数
	Confidence            int     `json:"confidence,omitempty"`             // AI 信心度 (0-100)
	RiskUSD               float64 `json:"risk_usd,omitempty"`               // 预估最大风险金额 (USDT)
	InvalidationCondition string  `json:"invalidation_condition,omitempty"` // 失效条件
	Reasoning  string  `json:"reasoning"`            // 决策理由摘要
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"` // 发送给 AI 的系统提示词
	UserPrompt   string     `json:"user_prompt"`   // 发送给 AI 的用户提示词
	CoTTrace     string     `json:"cot_trace"`     // AI 的思维链 (Chain of Thought) 分析过程
	Decisions    []Decision `json:"decisions"`     // AI 输出的具体决策列表
	Timestamp    time.Time  `json:"timestamp"`     // 决策生成时间
}
