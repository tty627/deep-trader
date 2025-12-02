package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
)

// BinanceExchange 真实币安交易所 (合约)
type BinanceExchange struct {
	Client           *futures.Client
	MarketData       map[string]*MarketData
	DualSidePosition bool               // true: Hedge mode, false: One-way mode
	InitialEquity    float64            // 本次程序运行期间的基准净值
	positionPeakPnL  map[string]float64 // 内存追踪持仓最高收益率
}

// SetLeverage 手动调整某个合约的杠杆
func (e *BinanceExchange) SetLeverage(symbol string, leverage int) error {
	if leverage <= 0 {
		return fmt.Errorf("leverage must be > 0, got %d", leverage)
	}

	_, err := e.Client.NewChangeLeverageService().
		Symbol(symbol).
		Leverage(leverage).
		Do(context.Background())
	if err != nil {
		return fmt.Errorf("change leverage failed for %s: %w", symbol, err)
	}

	log.Printf("✅ Set leverage for %s to %dx", symbol, leverage)
	return nil
}

func NewBinanceExchange(apiKey, secretKey, proxyURL string) *BinanceExchange {
	client := binance.NewFuturesClient(apiKey, secretKey)

	if proxyURL != "" {
		proxy, err := url.Parse(proxyURL)
		if err != nil {
			log.Printf("Warning: Invalid Proxy URL: %v", err)
		} else {
			transport := &http.Transport{
				Proxy: http.ProxyURL(proxy),
			}
			client.HTTPClient = &http.Client{
				Transport: transport,
			}
			log.Printf("✅ Binance Client using Proxy: %s", proxyURL)
		}
	}

	ex := &BinanceExchange{
		Client:          client,
		MarketData:      make(map[string]*MarketData),
		positionPeakPnL: make(map[string]float64),
	}

	// 检查当前持仓模式（单向 / 对冲），用于后续是否使用 positionSide / reduceOnly
	if pm, err := client.NewGetPositionModeService().Do(context.Background()); err != nil {
		log.Printf("⚠️ GetPositionMode failed, assume One-way mode (single position): %v", err)
	} else if pm != nil {
		ex.DualSidePosition = pm.DualSidePosition
	}
	log.Printf("Binance position mode: dualSide=%v", ex.DualSidePosition)

	return ex
}

// FetchMarketData 获取行情数据
func (e *BinanceExchange) FetchMarketData(symbols []string) error {
	for _, symbol := range symbols {
		// 1. 获取 3m K线 (用于日内数据)
		klines3m, err := e.fetchKlines(symbol, "3m", 60)
		if err != nil {
			log.Printf("Fetch 3m klines failed for %s: %v", symbol, err)
			continue
		}

		// 2. 获取 4h K线 (用于长期趋势)
		klines4h, err := e.fetchKlines(symbol, "4h", 60)
		if err != nil {
			log.Printf("Fetch 4h klines failed for %s: %v", symbol, err)
			continue
		}

		if len(klines3m) == 0 || len(klines4h) == 0 {
			continue
		}

		// 3. 计算基础数据
		currentKline := klines3m[len(klines3m)-1]
		currentPrice := currentKline.Close

		// 4. 计算指标
		ema20 := calculateEMA(klines3m, 20)
		macd := calculateMACD(klines3m)
		rsi7 := calculateRSI(klines3m, 7)

		// 5. 计算价格变化
		// 1h 变化: 约 20 个 3m K线
		priceChange1h := 0.0
		if len(klines3m) >= 21 {
			prev := klines3m[len(klines3m)-21].Close
			if prev > 0 {
				priceChange1h = (currentPrice - prev) / prev * 100
			}
		}

		// 4h 变化: 对比上一根 4h K线收盘价
		priceChange4h := 0.0
		if len(klines4h) >= 2 {
			prev := klines4h[len(klines4h)-2].Close
			if prev > 0 {
				priceChange4h = (currentPrice - prev) / prev * 100
			}
		}

		// 6. 获取资金费率和持仓量
		fundingRate, _ := e.fetchFundingRate(symbol)
		oiData, _ := e.fetchOpenInterest(symbol)

		// 7. 计算序列数据
		intraday := calculateIntradaySeries(klines3m)
		longerTerm := calculateLongerTermData(klines4h)

		// 8. 构建 MarketData
		e.MarketData[symbol] = &MarketData{
			Symbol:            symbol,
			CurrentPrice:      currentPrice,
			PriceChange1h:     priceChange1h,
			PriceChange4h:     priceChange4h,
			Volume24h:         0, // 不再单独请求24h ticker，节省API额度
			CurrentEMA20:      ema20,
			CurrentMACD:       macd,
			CurrentRSI7:       rsi7,
			FundingRate:       fundingRate,
			OpenInterest:      oiData,
			IntradaySeries:    intraday,
			LongerTermContext: longerTerm,
		}
	}
	return nil
}

// fetchKlines 获取K线数据
func (e *BinanceExchange) fetchKlines(symbol, interval string, limit int) ([]Kline, error) {
	klines, err := e.Client.NewKlinesService().Symbol(symbol).Interval(interval).Limit(limit).Do(context.Background())
	if err != nil {
		return nil, err
	}

	var res []Kline
	for _, k := range klines {
		open, _ := strconv.ParseFloat(k.Open, 64)
		high, _ := strconv.ParseFloat(k.High, 64)
		low, _ := strconv.ParseFloat(k.Low, 64)
		close, _ := strconv.ParseFloat(k.Close, 64)
		volume, _ := strconv.ParseFloat(k.Volume, 64)

		res = append(res, Kline{
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: k.CloseTime,
		})
	}
	return res, nil
}

// fetchFundingRate 获取资金费率
func (e *BinanceExchange) fetchFundingRate(symbol string) (float64, error) {
	res, err := e.Client.NewPremiumIndexService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return 0, err
	}
	if len(res) > 0 {
		rate, _ := strconv.ParseFloat(res[0].LastFundingRate, 64)
		return rate, nil
	}
	return 0, fmt.Errorf("no data")
}

// fetchOpenInterest 获取持仓量
func (e *BinanceExchange) fetchOpenInterest(symbol string) (*OIData, error) {
	res, err := e.Client.NewGetOpenInterestService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return nil, err
	}
	val, _ := strconv.ParseFloat(res.OpenInterest, 64)
	return &OIData{Latest: val, Average: val}, nil
}

// GetAccountInfo 获取账户信息
func (e *BinanceExchange) GetAccountInfo() AccountInfo {
	acc, err := e.Client.NewGetAccountService().Do(context.Background())
	if err != nil {
		log.Printf("获取账户信息失败: %v", err)
		return AccountInfo{}
	}

	var totalWalletBalance, totalUnrealizedPnL, totalMarginUsed float64

	totalWalletBalance, _ = strconv.ParseFloat(acc.TotalWalletBalance, 64)
	totalUnrealizedPnL, _ = strconv.ParseFloat(acc.TotalUnrealizedProfit, 64)
	totalMarginUsed, _ = strconv.ParseFloat(acc.TotalInitialMargin, 64)

	totalEquity := totalWalletBalance + totalUnrealizedPnL

	// 在本次程序运行期间，第一次获取时锁定一个基准净值，用于计算累计收益
	if e.InitialEquity == 0 && totalEquity > 0 {
		e.InitialEquity = totalEquity
	}

	var totalPnl, totalPnlPct float64
	if e.InitialEquity > 0 {
		totalPnl = totalEquity - e.InitialEquity
		totalPnlPct = (totalPnl / e.InitialEquity) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return AccountInfo{
		TotalEquity:      totalEquity,
		AvailableBalance: totalWalletBalance - totalMarginUsed, // Approx
		UnrealizedPnL:    totalUnrealizedPnL,
		TotalPnL:         totalPnl,
		TotalPnLPct:      totalPnlPct,
		MarginUsed:       totalMarginUsed,
		MarginUsedPct:    marginUsedPct,
		PositionCount:    len(e.GetPositions()),
	}
}

// GetPositions 获取当前持仓
func (e *BinanceExchange) GetPositions() []PositionInfo {
	return e.getPositionsFromRisk()
}

func (e *BinanceExchange) getPositionsFromRisk() []PositionInfo {
	risks, err := e.Client.NewGetPositionRiskService().Do(context.Background())
	if err != nil {
		log.Printf("GetPositionRisk Error: %v", err)
		return nil
	}

	var result []PositionInfo
	for _, p := range risks {
		amt, _ := strconv.ParseFloat(p.PositionAmt, 64)
		if amt == 0 {
			continue
		}

		side := "long"
		if amt < 0 {
			side = "short"
			amt = -amt
		}

		entryPrice, _ := strconv.ParseFloat(p.EntryPrice, 64)
		markPrice, _ := strconv.ParseFloat(p.MarkPrice, 64)
		unRealizedProfit, _ := strconv.ParseFloat(p.UnRealizedProfit, 64)
		leverage, _ := strconv.Atoi(p.Leverage)
		liquidationPrice, _ := strconv.ParseFloat(p.LiquidationPrice, 64)

		// Calculate margin used approx
		marginUsed := (amt * markPrice) / float64(leverage)
		
		unrealizedPnLPct := 0.0
		if marginUsed > 0 {
			unrealizedPnLPct = (unRealizedProfit / marginUsed) * 100
		}

		// 更新并获取最高收益率
		// 注意：如果持仓方向改变了（比如平多开空），理论上应该重置。
		// 这里简化处理：只要有持仓，就维护这个数值。平仓时手动重置。
		currentPeak := e.positionPeakPnL[p.Symbol]
		if unrealizedPnLPct > currentPeak {
			e.positionPeakPnL[p.Symbol] = unrealizedPnLPct
			currentPeak = unrealizedPnLPct
		}

		info := PositionInfo{
			Symbol:           p.Symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         amt,
			Leverage:         leverage,
			UnrealizedPnL:    unRealizedProfit,
			UnrealizedPnLPct: unrealizedPnLPct,
			PeakPnLPct:       currentPeak,
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       time.Now().UnixMilli(), // TODO: 最好能从API获取建仓时间，这里暂时用当前时间占位
		}
		
		result = append(result, info)
	}
	return result
}

// GetMarketData 获取市场数据快照
func (e *BinanceExchange) GetMarketData() map[string]*MarketData {
	// Return copy
	res := make(map[string]*MarketData)
	for k, v := range e.MarketData {
		res[k] = v
	}
	return res
}

// ExecuteDecision 执行交易决策
func (e *BinanceExchange) ExecuteDecision(d Decision) error {
	symbol := d.Symbol
	
	// 1. 调整杠杆（如果给了建议杠杆）
	if d.Leverage > 0 {
		// 检查当前杠杆是否已经是目标值（简单优化）
		_, err := e.Client.NewChangeLeverageService().Symbol(symbol).Leverage(d.Leverage).Do(context.Background())
		if err != nil {
			log.Printf("调整杠杆失败 %s: %v", symbol, err)
			// 不中断，继续尝试下单
		}
	}

	// 2. 开仓前，清理该币种所有现有挂单（防止止损/止盈堆积）
	if d.Action == "open_long" || d.Action == "open_short" {
		if err := e.CancelAllOrders(symbol); err != nil {
			log.Printf("⚠️ 开仓前取消挂单失败 %s: %v", symbol, err)
		}
	}

	// 3. 根据 action 决定买卖方向，以及在对冲模式下的 positionSide
	var side futures.SideType
	var positionSide futures.PositionSideType
	usePositionSide := e.DualSidePosition // 只有对冲模式才需要带 positionSide / reduceOnly

	switch d.Action {
	case "open_long":
		side = futures.SideTypeBuy
		if usePositionSide {
			positionSide = futures.PositionSideTypeLong
		}
	case "open_short":
		side = futures.SideTypeSell
		if usePositionSide {
			positionSide = futures.PositionSideTypeShort
		}
	case "close_long":
		side = futures.SideTypeSell
		if usePositionSide {
			positionSide = futures.PositionSideTypeLong
		}
	case "close_short":
		side = futures.SideTypeBuy
		if usePositionSide {
			positionSide = futures.PositionSideTypeShort
		}
	case "update_stop_loss":
		return e.handleUpdateStopLoss(d)
	case "update_take_profit":
		return e.handleUpdateTakeProfit(d)
	case "partial_close":
		return e.handlePartialClose(d)
	default:
		return nil // 其它动作这里忽略
	}

	// 3. 计算下单数量
	// Binance 要求数量为币的数量（例如 0.001 BTC），AI 给出的是 USD notional
	md, ok := e.MarketData[symbol]
	if !ok {
		return fmt.Errorf("No market data for %s", symbol)
	}
	if md.CurrentPrice <= 0 {
		return fmt.Errorf("invalid market price for %s", symbol)
	}

	quantity := d.PositionSizeUSD / md.CurrentPrice

	// 基础精度处理（实际生产环境应根据 ExchangeInfo 动态获取）
	qtyStr := fmt.Sprintf("%.3f", quantity)
	if strings.Contains(symbol, "BTC") {
		qtyStr = fmt.Sprintf("%.3f", quantity)
	}
	if strings.Contains(symbol, "ETH") {
		qtyStr = fmt.Sprintf("%.3f", quantity)
	}
	if strings.Contains(symbol, "SOL") {
		qtyStr = fmt.Sprintf("%.1f", quantity)
	}
	if strings.Contains(symbol, "DOGE") {
		qtyStr = fmt.Sprintf("%.0f", quantity)
	}

	// 对于 close_* 指令，忽略 AI 的 position_size_usd，直接按实际持仓全平
	if strings.HasPrefix(d.Action, "close") {
		positions := e.GetPositions()
		found := false
		for _, p := range positions {
			if p.Symbol != symbol {
				continue
			}
			// 匹配方向
			if (d.Action == "close_long" && p.Side == "long") || (d.Action == "close_short" && p.Side == "short") {
				// 全平当前仓位
				qtyStr = fmt.Sprintf("%f", p.Quantity)
				// 再按交易对做精度格式化
				if strings.Contains(symbol, "BTC") {
					qtyStr = fmt.Sprintf("%.3f", p.Quantity)
				}
				if strings.Contains(symbol, "ETH") {
					qtyStr = fmt.Sprintf("%.3f", p.Quantity)
				}
				if strings.Contains(symbol, "SOL") {
					qtyStr = fmt.Sprintf("%.1f", p.Quantity)
				}
				if strings.Contains(symbol, "DOGE") {
					qtyStr = fmt.Sprintf("%.0f", p.Quantity)
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("no matching position to %s for %s", d.Action, symbol)
		}
	}

	// 4. 构造下单请求
	service := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeMarket).
		Quantity(qtyStr)

	// 对冲模式下才传 positionSide；reduceOnly 在 Hedge 模式下会被拒绝，这里统一不使用
	if usePositionSide {
		service = service.PositionSide(positionSide)
	}

	// 5. 发送订单
	_, err := service.Do(context.Background())
	if err != nil {
		return fmt.Errorf("Binance Order Failed: %v", err)
	}

	log.Printf("Binance Executed: %s %s Qty:%s (dualSide=%v)", d.Action, symbol, qtyStr, e.DualSidePosition)

	// 如果是平仓操作，重置最高收益率记录
	if strings.HasPrefix(d.Action, "close") {
		delete(e.positionPeakPnL, symbol)
	}

	// 6. 开仓后，设置止损和止盈（如果有）
	if (d.Action == "open_long" || d.Action == "open_short") && (d.StopLoss > 0 || d.TakeProfit > 0) {
		log.Printf("正在设置止损止盈 for %s...", symbol)
		// 确定持仓方向
		posSide := "LONG"
		if d.Action == "open_short" {
			posSide = "SHORT"
		}

		// 设置止损
		if d.StopLoss > 0 {
			if err := e.SetStopLoss(symbol, posSide, quantity, d.StopLoss); err != nil {
				log.Printf("❌ 设置止损失败: %v", err)
			} else {
				log.Printf("✅ 止损已设置: %.4f", d.StopLoss)
			}
		}

		// 设置止盈
		if d.TakeProfit > 0 {
			if err := e.SetTakeProfit(symbol, posSide, quantity, d.TakeProfit); err != nil {
				log.Printf("❌ 设置止盈失败: %v", err)
			} else {
				log.Printf("✅ 止盈已设置: %.4f", d.TakeProfit)
			}
		}
	}

	return nil
}

// handleUpdateStopLoss 处理更新止损
func (e *BinanceExchange) handleUpdateStopLoss(d Decision) error {
	symbol := d.Symbol
	newSL := d.NewStopLoss
	if newSL <= 0 {
		return fmt.Errorf("invalid new stop loss: %f", newSL)
	}

	// 1. 获取当前持仓
	positions := e.GetPositions()
	var currentPos *PositionInfo
	for _, p := range positions {
		if p.Symbol == symbol {
			currentPos = &p
			break
		}
	}
	if currentPos == nil {
		return fmt.Errorf("no position found for %s to update stop loss", symbol)
	}

	// 2. 取消旧的止损单
	if err := e.CancelStopLossOrders(symbol); err != nil {
		log.Printf("⚠️ 取消旧止损失败: %v", err)
		// 继续尝试设置新止损
	}

	// 3. 设置新止损
	posSide := "LONG"
	if currentPos.Side == "short" {
		posSide = "SHORT"
	}
	
	log.Printf("Updating Stop Loss for %s %s to %.4f", symbol, posSide, newSL)
	return e.SetStopLoss(symbol, posSide, currentPos.Quantity, newSL)
}

// handleUpdateTakeProfit 处理更新止盈
func (e *BinanceExchange) handleUpdateTakeProfit(d Decision) error {
	symbol := d.Symbol
	newTP := d.NewTakeProfit
	if newTP <= 0 {
		return fmt.Errorf("invalid new take profit: %f", newTP)
	}

	// 1. 获取当前持仓
	positions := e.GetPositions()
	var currentPos *PositionInfo
	for _, p := range positions {
		if p.Symbol == symbol {
			currentPos = &p
			break
		}
	}
	if currentPos == nil {
		return fmt.Errorf("no position found for %s to update take profit", symbol)
	}

	// 2. 取消旧的止盈单
	if err := e.CancelTakeProfitOrders(symbol); err != nil {
		log.Printf("⚠️ 取消旧止盈失败: %v", err)
	}

	// 3. 设置新止盈
	posSide := "LONG"
	if currentPos.Side == "short" {
		posSide = "SHORT"
	}

	log.Printf("Updating Take Profit for %s %s to %.4f", symbol, posSide, newTP)
	return e.SetTakeProfit(symbol, posSide, currentPos.Quantity, newTP)
}

// handlePartialClose 处理部分平仓
func (e *BinanceExchange) handlePartialClose(d Decision) error {
	symbol := d.Symbol
	pct := d.ClosePercentage
	if pct <= 0 || pct > 100 {
		return fmt.Errorf("invalid close percentage: %f", pct)
	}

	// 1. 获取当前持仓
	positions := e.GetPositions()
	var currentPos *PositionInfo
	for _, p := range positions {
		if p.Symbol == symbol {
			currentPos = &p
			break
		}
	}
	if currentPos == nil {
		return fmt.Errorf("no position found for %s to partial close", symbol)
	}

	// 2. 计算平仓数量
	closeQty := currentPos.Quantity * (pct / 100.0)
	
	// 格式化精度 (简单处理)
	qtyStr := fmt.Sprintf("%.3f", closeQty)
	if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", closeQty) }
	if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", closeQty) }

	// 3. 执行平仓订单
	side := futures.SideTypeSell
	var posSide futures.PositionSideType
	if currentPos.Side == "short" {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	} else {
		posSide = futures.PositionSideTypeLong
	}

	service := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeMarket).
		Quantity(qtyStr)

	if e.DualSidePosition {
		service = service.PositionSide(posSide)
	}

	_, err := service.Do(context.Background())
	if err != nil {
		return fmt.Errorf("partial close failed: %v", err)
	}

	log.Printf("✅ Partial Close %s %s: %s (%.1f%%)", symbol, currentPos.Side, qtyStr, pct)
	
	// 如果是 100% 平仓，也清理记录
	if pct >= 99.9 {
		delete(e.positionPeakPnL, symbol)
	}
	return nil
}

// CancelStopLossOrders 取消所有止损单
func (e *BinanceExchange) CancelStopLossOrders(symbol string) error {
	openOrders, err := e.Client.NewListOpenOrdersService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return err
	}
	for _, o := range openOrders {
		if o.Type == futures.OrderTypeStopMarket || o.Type == futures.OrderTypeStop {
			e.Client.NewCancelOrderService().Symbol(symbol).OrderID(o.OrderID).Do(context.Background())
		}
	}
	return nil
}

// CancelTakeProfitOrders 取消所有止盈单
func (e *BinanceExchange) CancelTakeProfitOrders(symbol string) error {
	openOrders, err := e.Client.NewListOpenOrdersService().Symbol(symbol).Do(context.Background())
	if err != nil {
		return err
	}
	for _, o := range openOrders {
		if o.Type == futures.OrderTypeTakeProfitMarket || o.Type == futures.OrderTypeTakeProfit {
			e.Client.NewCancelOrderService().Symbol(symbol).OrderID(o.OrderID).Do(context.Background())
		}
	}
	return nil
}

// CancelAllOrders 取消该币种的所有挂单
func (e *BinanceExchange) CancelAllOrders(symbol string) error {
	return e.Client.NewCancelAllOpenOrdersService().Symbol(symbol).Do(context.Background())
}

// SetStopLoss 设置止损单
func (e *BinanceExchange) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	// 简单格式化 quantity (应与开仓保持一致)
	qtyStr := fmt.Sprintf("%.3f", quantity)
	if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", quantity) }
	if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", quantity) }

	_, err := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.OrderTypeStopMarket).
		StopPrice(fmt.Sprintf("%.4f", stopPrice)). // 简化处理，全部保留4位小数
		Quantity(qtyStr).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true). // 触发后平仓
		Do(context.Background())

	return err
}

// SetTakeProfit 设置止盈单
func (e *BinanceExchange) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	var side futures.SideType
	var posSide futures.PositionSideType

	if positionSide == "LONG" {
		side = futures.SideTypeSell
		posSide = futures.PositionSideTypeLong
	} else {
		side = futures.SideTypeBuy
		posSide = futures.PositionSideTypeShort
	}

	// 简单格式化 quantity
	qtyStr := fmt.Sprintf("%.3f", quantity)
	if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", quantity) }
	if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", quantity) }

	_, err := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.OrderTypeTakeProfitMarket).
		StopPrice(fmt.Sprintf("%.4f", takeProfitPrice)).
		Quantity(qtyStr).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true).
		Do(context.Background())

	return err
}
