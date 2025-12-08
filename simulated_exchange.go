
package main

import (
	"fmt"
	"log"
	"time"
)

// SimulatedExchange 模拟交易所，实现 Exchange 接口
type SimulatedExchange struct {
	account       AccountInfo
	positions     map[string]PositionInfo
	marketData    map[string]*MarketData
	initialEquity float64
	History       *TradeHistoryManager
}

// NewSimulatedExchange 创建一个新的模拟交易所实例
func NewSimulatedExchange(initialCapital float64) *SimulatedExchange {
	return &SimulatedExchange{
		account: AccountInfo{
			TotalEquity:      initialCapital,
			AvailableBalance: initialCapital,
			UnrealizedPnL:    0,
			TotalPnL:         0,
			TotalPnLPct:      0,
			MarginUsed:       0,
			MarginUsedPct:    0,
			PositionCount:    0,
		},
		positions:     make(map[string]PositionInfo),
		marketData:    make(map[string]*MarketData),
		initialEquity: initialCapital,
		History:       NewTradeHistoryManager(),
	}
}

// FetchMarketData 为每个交易对生成简单的模拟行情
func (s *SimulatedExchange) FetchMarketData(symbols []string) error {
	// 1. 模拟价格变动
	for _, symbol := range symbols {
		md, ok := s.marketData[symbol]
		if !ok {
			md = &MarketData{Symbol: symbol}
		}
		if md.CurrentPrice == 0 {
			md.CurrentPrice = 100.0 // 初始价格
		} else {
			// 简单的随机游走: -0.5% 到 +0.5%
			// 这里只是演示，实际上可以用更复杂的逻辑
			md.CurrentPrice += 0.1 // 简单递增测试
		}
		s.marketData[symbol] = md
	}

	// 2. 更新账户盈亏
	var totalUnrealizedPnL float64
	var totalMarginUsed float64

	for k, pos := range s.positions {
		md, ok := s.marketData[pos.Symbol]
		if !ok {
			continue
		}
		
		// 更新标记价格
		pos.MarkPrice = md.CurrentPrice
		
		// 计算未实现盈亏
		// 多单盈亏 = (当前价 - 开仓价) * 数量
		// 空单盈亏 = (开仓价 - 当前价) * 数量
		if pos.Side == "long" {
			pos.UnrealizedPnL = (pos.MarkPrice - pos.EntryPrice) * pos.Quantity
		} else {
			pos.UnrealizedPnL = (pos.EntryPrice - pos.MarkPrice) * pos.Quantity
		}
		
		// 更新持仓信息
		if pos.MarginUsed > 0 {
			pos.UnrealizedPnLPct = (pos.UnrealizedPnL / pos.MarginUsed) * 100
		}
		s.positions[k] = pos

		totalUnrealizedPnL += pos.UnrealizedPnL
		totalMarginUsed += pos.MarginUsed
	}

	// 更新账户信息
	s.account.UnrealizedPnL = totalUnrealizedPnL
	s.account.MarginUsed = totalMarginUsed
	s.account.TotalEquity = s.account.AvailableBalance + s.account.MarginUsed + s.account.UnrealizedPnL
	if s.account.TotalEquity > 0 {
		s.account.MarginUsedPct = (s.account.MarginUsed / s.account.TotalEquity) * 100
	}

	// 根据初始净值计算累计盈亏
	if s.initialEquity > 0 {
		s.account.TotalPnL = s.account.TotalEquity - s.initialEquity
		s.account.TotalPnLPct = (s.account.TotalPnL / s.initialEquity) * 100
	}

	return nil
}

func (s *SimulatedExchange) GetAccountInfo() AccountInfo {
	return s.account
}

func (s *SimulatedExchange) GetPositions() []PositionInfo {
	positions := make([]PositionInfo, 0, len(s.positions))
	for _, p := range s.positions {
		positions = append(positions, p)
	}
	return positions
}

func (s *SimulatedExchange) GetMarketData() map[string]*MarketData {
	return s.marketData
}

// GetTradeHistory 获取历史记录
func (s *SimulatedExchange) GetTradeHistory() []TradeRecord {
	if s.History != nil {
		return s.History.GetHistory()
	}
	return nil
}

func (s *SimulatedExchange) ExecuteDecision(d Decision) error {
	fmt.Printf("Simulated execution for %s: %s size $%.2f\n", d.Symbol, d.Action, d.PositionSizeUSD)

	md, ok := s.marketData[d.Symbol]
	if !ok {
		return fmt.Errorf("no market data for %s", d.Symbol)
	}
	price := md.CurrentPrice
	if price <= 0 {
		return fmt.Errorf("invalid price for %s", d.Symbol)
	}

	switch d.Action {
	case "open_long", "open_short":
		// 检查余额
		marginRequired := d.PositionSizeUSD / float64(d.Leverage)
		if s.account.AvailableBalance < marginRequired {
			return fmt.Errorf("insufficient balance: have %.2f, need %.2f", s.account.AvailableBalance, marginRequired)
		}

		// 计算数量
		quantity := d.PositionSizeUSD / price
		side := "long"
		if d.Action == "open_short" {
			side = "short"
		}

		// 检查是否已有持仓
		if pos, exists := s.positions[d.Symbol]; exists {
			if pos.Side != side {
				return fmt.Errorf("conflict: existing %s position for %s", pos.Side, d.Symbol)
			}
			// 加仓逻辑 (简单平均价格)
			totalCost := pos.EntryPrice * pos.Quantity
			newCost := price * quantity
			totalQty := pos.Quantity + quantity
			avgPrice := (totalCost + newCost) / totalQty

			pos.EntryPrice = avgPrice
			pos.Quantity = totalQty
			pos.MarginUsed += marginRequired
			pos.Leverage = d.Leverage // 更新杠杆
			s.positions[d.Symbol] = pos
		} else {
			// 新建仓位
			s.positions[d.Symbol] = PositionInfo{
				Symbol:     d.Symbol,
				Side:       side,
				EntryPrice: price,
				MarkPrice:  price,
				Quantity:   quantity,
				Leverage:   d.Leverage,
				MarginUsed: marginRequired,
				UpdateTime: time.Now().UnixMilli(),
			}
			s.account.PositionCount++
		}

		// 扣除可用余额
		s.account.AvailableBalance -= marginRequired
		s.account.MarginUsed += marginRequired

	case "close_long", "close_short":
		pos, exists := s.positions[d.Symbol]
		if !exists {
			return fmt.Errorf("no position to close for %s", d.Symbol)
		}
		
		// 验证方向
		expectedSide := "long"
		if d.Action == "close_short" {
			expectedSide = "short"
		}
		if pos.Side != expectedSide {
			return fmt.Errorf("position side mismatch: have %s, want close %s", pos.Side, expectedSide)
		}

		// 计算平仓盈亏
		var pnl float64
		if pos.Side == "long" {
			pnl = (price - pos.EntryPrice) * pos.Quantity
		} else {
			pnl = (pos.EntryPrice - price) * pos.Quantity
		}

		// 返还资金 = 保证金 + 盈亏
		amountToReturn := pos.MarginUsed + pnl
		
		s.account.AvailableBalance += amountToReturn
		s.account.TotalPnL += pnl
		s.account.MarginUsed -= pos.MarginUsed
		
		// 移除持仓
		delete(s.positions, d.Symbol)
		s.account.PositionCount--
		
		log.Printf("Closed %s position for %s. PnL: %.2f", pos.Side, d.Symbol, pnl)

		// 记录历史
		if s.History != nil {
			rec := TradeRecord{
				Time:       time.Now().Format("15:04:05"),
				Symbol:     d.Symbol,
				Side:       pos.Side,
				Action:     d.Action,
				EntryPrice: pos.EntryPrice,
				ExitPrice:  price,
				Quantity:   pos.Quantity,
				PnL:        pnl,
				PnLPct:     (pnl / pos.MarginUsed) * 100,
				Reason:     d.Reasoning,
			}
			s.History.AddRecord(rec)
		}
	}

	return nil
}
