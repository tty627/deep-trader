package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// ErrBacktestFinished 表示历史数据已经走完
var ErrBacktestFinished = errors.New("backtest finished")

// BacktestSymbolData 保存某个交易对的历史 K 线
// 目前仅使用 3m K 线驱动回测，所有指标都基于该序列计算
// CSV 格式约定（无表头，或表头会被自动跳过）：
// open,high,low,close,volume,taker_buy_volume,close_time_ms
// 例如：
// 43123.5,43200.1,43000.0,43100.2,1234.56,789.12,1719811200000

type BacktestSymbolData struct {
	Klines3m []Kline
}

// BacktestExchange 使用历史 K 线做回测，实现 Exchange 接口
// 行情由离线数据驱动，撮合逻辑与 SimulatedExchange 类似

type BacktestExchange struct {
	account       AccountInfo
	positions     map[string]PositionInfo
	marketData    map[string]*MarketData
	initialEquity float64

	data     map[string]*BacktestSymbolData
	step     int // 当前回测步数（对应 3m K 线索引）
	maxStep  int // 所有 symbol 共享的最大步数

	History *TradeHistoryManager
}

// NewBacktestExchangeFromCSV 从本地 CSV 目录创建回测交易所。
// dataDir 下期望存在若干文件：例如 BTCUSDT_3m.csv, ETHUSDT_3m.csv ...
// 所有 symbol 的 3m 序列长度应尽量一致，实际以最短的为准。
func NewBacktestExchangeFromCSV(initialCapital float64, dataDir string, symbols []string) (*BacktestExchange, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("dataDir is empty for backtest")
	}

	bt := &BacktestExchange{
		account: AccountInfo{
			TotalEquity:      initialCapital,
			AvailableBalance: initialCapital,
			TotalPnL:         0,
			TotalPnLPct:      0,
			MarginUsed:       0,
			MarginUsedPct:    0,
			PositionCount:    0,
		},
		positions:     make(map[string]PositionInfo),
		marketData:    make(map[string]*MarketData),
		initialEquity: initialCapital,
		data:          make(map[string]*BacktestSymbolData),
		History:       NewTradeHistoryManager(),
	}

	minLen := -1
	for _, symbol := range symbols {
		path := filepath.Join(dataDir, fmt.Sprintf("%s_3m.csv", symbol))
		klines, err := loadKlinesFromCSV(path)
		if err != nil {
			return nil, fmt.Errorf("load %s failed: %w", filepath.Base(path), err)
		}
		if len(klines) == 0 {
			return nil, fmt.Errorf("no klines loaded for %s", symbol)
		}
		bt.data[symbol] = &BacktestSymbolData{Klines3m: klines}
		if minLen == -1 || len(klines) < minLen {
			minLen = len(klines)
		}
	}

	if minLen <= 1 {
		return nil, fmt.Errorf("not enough backtest data (minLen=%d)", minLen)
	}

	bt.maxStep = minLen
	bt.step = 0
	return bt, nil
}

// loadKlinesFromCSV 解析 CSV 为 Kline 序列
func loadKlinesFromCSV(path string) ([]Kline, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1 // 允许可变列

	var klines []Kline
	lineNum := 0
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv %s line %d: %w", path, lineNum+1, err)
		}
		lineNum++

		// 跳过可能存在的表头
		if lineNum == 1 {
			if _, convErr := strconv.ParseFloat(rec[0], 64); convErr != nil {
				continue
			}
		}

		if len(rec) < 6 {
			continue
		}

		open, _ := strconv.ParseFloat(rec[0], 64)
		high, _ := strconv.ParseFloat(rec[1], 64)
		low, _ := strconv.ParseFloat(rec[2], 64)
		closePrice, _ := strconv.ParseFloat(rec[3], 64)
		vol, _ := strconv.ParseFloat(rec[4], 64)
		takerVol, _ := strconv.ParseFloat(rec[5], 64)

		var closeTime int64
		if len(rec) >= 7 {
			ct, _ := strconv.ParseInt(rec[6], 10, 64)
			closeTime = ct
		}

		klines = append(klines, Kline{
			Open:           open,
			High:           high,
			Low:            low,
			Close:          closePrice,
			Volume:         vol,
			TakerBuyVolume: takerVol,
			CloseTime:      closeTime,
		})
	}

	return klines, nil
}

// FetchMarketData 使用当前 step 对应的 K 线快照刷新行情和账户状态
func (b *BacktestExchange) FetchMarketData(symbols []string) error {
	if b.step >= b.maxStep {
		return ErrBacktestFinished
	}

	for _, symbol := range symbols {
		data, ok := b.data[symbol]
		if !ok {
			return fmt.Errorf("no backtest data for symbol %s", symbol)
		}
		if b.step >= len(data.Klines3m) {
			return ErrBacktestFinished
		}

		// 使用 [0 : step+1] 作为已知历史序列（3m）
		series3m := data.Klines3m[:b.step+1]
		currentK := series3m[len(series3m)-1]
		currentPrice := currentK.Close

		// 从 3m 聚合出 1h 和 4h K 线，便于与实时实盘对齐
		klines1h := aggregateKlines(series3m, 20)  // 20 * 3m = 60m
		klines4h := aggregateKlines(series3m, 80)  // 80 * 3m = 240m

		// 价格变化
		priceChange1h := 0.0
		if len(klines1h) >= 2 {
			prev := klines1h[len(klines1h)-2].Close
			if prev > 0 {
				priceChange1h = (currentPrice - prev) / prev * 100
			}
		} else if len(series3m) >= 21 {
			// Fallback：使用 3m 近似 1h 变化
			prev := series3m[len(series3m)-21].Close
			if prev > 0 {
				priceChange1h = (currentPrice - prev) / prev * 100
			}
		}

		priceChange4h := 0.0
		if len(klines4h) >= 2 {
			prev := klines4h[len(klines4h)-2].Close
			if prev > 0 {
				priceChange4h = (currentPrice - prev) / prev * 100
			}
		} else if len(series3m) >= 81 { // 80 * 3m = 240m
			// Fallback：使用 3m 近似 4h 变化
			prev := series3m[len(series3m)-81].Close
			if prev > 0 {
				priceChange4h = (currentPrice - prev) / prev * 100
			}
		}

		// 技术指标（3m 日内)
		ema20 := calculateEMA(series3m, 20)
		macd := calculateMACD(series3m)
		rsi7 := calculateRSI(series3m, 7)
		bbUpper, bbMid, bbLower := calculateBollingerBands(series3m, 20, 2.0)
		intraday := calculateIntradaySeries(series3m)

		// 聚合 30m K线并计算指标 (10 * 3m = 30m)
		klines30m := aggregateKlines(series3m, 10)
		var ema20_30m, macd_30m, rsi14_30m, atr14_30m float64
		if len(klines30m) > 0 {
			ema20_30m = calculateEMA(klines30m, 20)
			macd_30m = calculateMACD(klines30m)
			rsi14_30m = calculateRSI(klines30m, 14)
			atr14_30m = calculateATR(klines30m, 14)
		}

		// 中周期 1h 指标
		var ema20_1h, macd_1h, rsi14_1h, atr14_1h float64
		if len(klines1h) > 0 {
			ema20_1h = calculateEMA(klines1h, 20)
			macd_1h = calculateMACD(klines1h)
			rsi14_1h = calculateRSI(klines1h, 14)
			atr14_1h = calculateATR(klines1h, 14)
		}

		// 长周期 4h 上下文
		var longerTerm *LongerTermData
		if len(klines4h) > 0 {
			longerTerm = calculateLongerTermData(klines4h)
		} else {
			// Fallback：在早期样本不足时，仍然基于 3m 计算一个近似的长期上下文
			longerTerm = calculateLongerTermData(series3m)
		}

		// 3m 成交量分析
		volAnalysis := calculateVolumeAnalysis(series3m, 20)

		// 使用 1h 聚合K线计算简单波动率
		vol1h := 0.0
		if len(klines1h) > 0 {
			vol1h = calculateRealizedVol(klines1h, 20)
		}
		sentiment := &SentimentData{
			Volatility1h:   vol1h,
			FearGreedIndex: 50,
			FearGreedLabel: "Neutral",
			LocalSentiment: "Backtest_Unknown",
		}

		b.marketData[symbol] = &MarketData{
			Symbol:        symbol,
			CurrentPrice:  currentPrice,
			PriceChange1h: priceChange1h,
			PriceChange4h: priceChange4h,
			
			CurrentEMA20: ema20,
			CurrentMACD:  macd,
			CurrentRSI7:  rsi7,
			
			EMA20_1h: ema20_1h,
			MACD_1h:  macd_1h,
			RSI14_1h: rsi14_1h,
			ATR14_1h: atr14_1h,

			EMA20_30m: ema20_30m,
			MACD_30m:  macd_30m,
			RSI14_30m: rsi14_30m,
			ATR14_30m: atr14_30m,

			BollingerUpper:  bbUpper,
			BollingerMiddle: bbMid,
			BollingerLower:  bbLower,
			
			VolumeAnalysis:    volAnalysis,
			Sentiment:         sentiment,
			IntradaySeries:    intraday,
			LongerTermContext: longerTerm,
		}
	}

	// 根据最新行情重新估值账户
	b.revalueAccount()

	b.step++
	return nil
}

// revalueAccount 根据当前 marketData 和 positions 重新计算账户净值
func (b *BacktestExchange) revalueAccount() {
	var totalUnrealizedPnL float64
	var totalMarginUsed float64

	for k, pos := range b.positions {
		md, ok := b.marketData[pos.Symbol]
		if !ok {
			continue
		}

		pos.MarkPrice = md.CurrentPrice
		if pos.Side == "long" {
			pos.UnrealizedPnL = (pos.MarkPrice - pos.EntryPrice) * pos.Quantity
		} else {
			pos.UnrealizedPnL = (pos.EntryPrice - pos.MarkPrice) * pos.Quantity
		}

		if pos.MarginUsed > 0 {
			pos.UnrealizedPnLPct = (pos.UnrealizedPnL / pos.MarginUsed) * 100
		}
		b.positions[k] = pos

		totalUnrealizedPnL += pos.UnrealizedPnL
		totalMarginUsed += pos.MarginUsed
	}

	b.account.UnrealizedPnL = totalUnrealizedPnL
	b.account.MarginUsed = totalMarginUsed
	b.account.TotalEquity = b.account.AvailableBalance + b.account.MarginUsed + b.account.UnrealizedPnL
	if b.account.TotalEquity > 0 {
		b.account.MarginUsedPct = (b.account.MarginUsed / b.account.TotalEquity) * 100
	}

	if b.initialEquity > 0 {
		b.account.TotalPnL = b.account.TotalEquity - b.initialEquity
		b.account.TotalPnLPct = (b.account.TotalPnL / b.initialEquity) * 100
	}
}

// GetAccountInfo 获取账户信息
func (b *BacktestExchange) GetAccountInfo() AccountInfo {
	return b.account
}

// GetPositions 获取当前持仓
func (b *BacktestExchange) GetPositions() []PositionInfo {
	positions := make([]PositionInfo, 0, len(b.positions))
	for _, p := range b.positions {
		positions = append(positions, p)
	}
	return positions
}

// GetMarketData 获取当前行情快照
func (b *BacktestExchange) GetMarketData() map[string]*MarketData {
	return b.marketData
}

// GetTradeHistory 获取历史交易记录
func (b *BacktestExchange) GetTradeHistory() []TradeRecord {
	if b.History != nil {
		return b.History.GetHistory()
	}
	return nil
}

// ExecuteDecision 在回测环境下执行交易决策
// 目前实现与 SimulatedExchange 基本一致，支持开仓、全平和部分平仓。
func (b *BacktestExchange) ExecuteDecision(d Decision) error {
	md, ok := b.marketData[d.Symbol]
	if !ok {
		return fmt.Errorf("no market data for %s", d.Symbol)
	}
	price := md.CurrentPrice
	if price <= 0 {
		return fmt.Errorf("invalid price for %s", d.Symbol)
	}

	switch d.Action {
	case "open_long", "open_short":
		marginRequired := d.PositionSizeUSD / float64(d.Leverage)
		if b.account.AvailableBalance < marginRequired {
			return fmt.Errorf("insufficient balance: have %.2f, need %.2f", b.account.AvailableBalance, marginRequired)
		}

		quantity := d.PositionSizeUSD / price
		side := "long"
		if d.Action == "open_short" {
			side = "short"
		}

		if pos, exists := b.positions[d.Symbol]; exists {
			if pos.Side != side {
				return fmt.Errorf("conflict: existing %s position for %s", pos.Side, d.Symbol)
			}
			totalCost := pos.EntryPrice * pos.Quantity
			newCost := price * quantity
			totalQty := pos.Quantity + quantity
			avgPrice := (totalCost + newCost) / totalQty

			pos.EntryPrice = avgPrice
			pos.Quantity = totalQty
			pos.MarginUsed += marginRequired
			pos.Leverage = d.Leverage
			b.positions[d.Symbol] = pos
		} else {
			b.positions[d.Symbol] = PositionInfo{
				Symbol:     d.Symbol,
				Side:       side,
				EntryPrice: price,
				MarkPrice:  price,
				Quantity:   quantity,
				Leverage:   d.Leverage,
				MarginUsed: marginRequired,
			}
			b.account.PositionCount++
		}

		b.account.AvailableBalance -= marginRequired
		b.account.MarginUsed += marginRequired

	case "close_long", "close_short":
		pos, exists := b.positions[d.Symbol]
		if !exists {
			return fmt.Errorf("no position to close for %s", d.Symbol)
		}

		expectedSide := "long"
		if d.Action == "close_short" {
			expectedSide = "short"
		}
		if pos.Side != expectedSide {
			return fmt.Errorf("position side mismatch: have %s, want close %s", pos.Side, expectedSide)
		}

		var pnl float64
		if pos.Side == "long" {
			pnl = (price - pos.EntryPrice) * pos.Quantity
		} else {
			pnl = (pos.EntryPrice - price) * pos.Quantity
		}

		amountToReturn := pos.MarginUsed + pnl

		b.account.AvailableBalance += amountToReturn
		b.account.TotalPnL += pnl
		b.account.MarginUsed -= pos.MarginUsed

		delete(b.positions, d.Symbol)
		b.account.PositionCount--

		if b.History != nil {
			rec := TradeRecord{
				Time:       "", // 回测模式可选填时间戳（未来可从 K 线时间推导）
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
			b.History.AddRecord(rec)
		}

	case "partial_close":
		pos, exists := b.positions[d.Symbol]
		if !exists {
			return fmt.Errorf("no position to partial close for %s", d.Symbol)
		}

		// Remove the profit threshold check for backtest
		// In backtest mode, allow partial close at any profit level for testing flexibility

		pct := d.ClosePercentage / 100.0
		if pct <= 0 {
			// 兼容仅提供 position_size_usd 的情况：根据当前持仓名义价值推导出比例
			if d.PositionSizeUSD > 0 {
				notional := pos.Quantity * price
				if notional <= 0 {
					return fmt.Errorf("cannot derive close percentage for %s: notional<=0 (qty=%.6f, price=%.6f)", d.Symbol, pos.Quantity, price)
				}
				pct = d.PositionSizeUSD / notional
				if pct <= 0 {
					return fmt.Errorf("invalid partial close notional for %s: position_size_usd=%.2f, notional=%.2f", d.Symbol, d.PositionSizeUSD, notional)
				}
				if pct > 1 {
					pct = 1
				}
				// 回写推导出的百分比，方便后续日志/历史记录查看
				d.ClosePercentage = pct * 100
			} else {
				return fmt.Errorf("invalid close percentage: %.2f", d.ClosePercentage)
			}
		}
		if pct > 1 {
			pct = 1
		}

		closeQty := pos.Quantity * pct
		if closeQty <= 0 {
			return fmt.Errorf("close quantity too small for %s", d.Symbol)
		}

		var pnl float64
		if pos.Side == "long" {
			pnl = (price - pos.EntryPrice) * closeQty
		} else {
			pnl = (pos.EntryPrice - price) * closeQty
		}

		closedMargin := pos.MarginUsed * pct

		b.account.AvailableBalance += closedMargin + pnl
		b.account.TotalPnL += pnl
		b.account.MarginUsed -= closedMargin
		if b.account.MarginUsed < 0 {
			b.account.MarginUsed = 0
		}

		pos.Quantity -= closeQty
		pos.MarginUsed -= closedMargin
		if pos.MarginUsed < 0 {
			pos.MarginUsed = 0
		}

		if pos.Quantity <= 0 || pos.MarginUsed == 0 {
			delete(b.positions, d.Symbol)
			b.account.PositionCount--
		} else {
			b.positions[d.Symbol] = pos
		}

		if b.History != nil {
			rec := TradeRecord{
				Time:       "",
				Symbol:     d.Symbol,
				Side:       pos.Side,
				Action:     "partial_close",
				EntryPrice: pos.EntryPrice,
				ExitPrice:  price,
				Quantity:   closeQty,
				PnL:        pnl,
				PnLPct:     pos.UnrealizedPnLPct,
				Reason:     d.Reasoning,
			}
			b.History.AddRecord(rec)
		}

	default:
		// 对于 wait/hold/update_stop_loss 等，当前版本在回测中忽略
	}

	return nil
}
