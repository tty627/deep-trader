package main

// Exchange 定义通用交易所接口
type Exchange interface {
	// FetchMarketData 获取行情数据
	FetchMarketData(symbols []string) error
	
	// GetAccountInfo 获取账户信息
	GetAccountInfo() AccountInfo
	
	// GetPositions 获取当前持仓
	GetPositions() []PositionInfo
	
	// GetMarketData 获取市场数据快照
	GetMarketData() map[string]*MarketData
	
	// ExecuteDecision 执行交易决策
	ExecuteDecision(d Decision) error
}
