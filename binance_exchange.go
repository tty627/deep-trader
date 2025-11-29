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
	Client     *futures.Client
	MarketData map[string]*MarketData
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

	return &BinanceExchange{
		Client:     client,
		MarketData: make(map[string]*MarketData),
	}
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
	
	// Calculate PnL Pct roughly based on wallet balance (this might need adjustment based on initial deposit tracking)
	// For now, just show current status
	
	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return AccountInfo{
		TotalEquity:      totalEquity,
		AvailableBalance: totalWalletBalance - totalMarginUsed, // Approx
		UnrealizedPnL:    totalUnrealizedPnL,
		TotalPnL:         0, // Hard to track without DB
		TotalPnLPct:      0,
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

		info := PositionInfo{
			Symbol:           p.Symbol,
			Side:             side,
			EntryPrice:       entryPrice,
			MarkPrice:        markPrice,
			Quantity:         amt,
			Leverage:         leverage,
			UnrealizedPnL:    unRealizedProfit,
			UnrealizedPnLPct: 0, // Need calc
			LiquidationPrice: liquidationPrice,
			MarginUsed:       marginUsed,
			UpdateTime:       time.Now().UnixMilli(),
		}
		
		if marginUsed > 0 {
			info.UnrealizedPnLPct = (unRealizedProfit / marginUsed) * 100
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
	
	// 1. 调整杠杆
	if d.Leverage > 0 {
		_, err := e.Client.NewChangeLeverageService().Symbol(symbol).Leverage(d.Leverage).Do(context.Background())
		if err != nil {
			log.Printf("调整杠杆失败 %s: %v", symbol, err)
			// Don't return error, try to proceed
		}
	}

	var side futures.SideType

	switch d.Action {
	case "open_long":
		side = futures.SideTypeBuy
	case "open_short":
		side = futures.SideTypeSell
	case "close_long":
		side = futures.SideTypeSell
	case "close_short":
		side = futures.SideTypeBuy
	default:
		return nil // Ignore others for now
	}

	// Calculate Quantity
	// Binance requires quantity in COIN (e.g. 0.001 BTC), but decision gives USD size
	// We need current price to convert
	md, ok := e.MarketData[symbol]
	if !ok {
		return fmt.Errorf("No market data for %s", symbol)
	}
	
	quantity := d.PositionSizeUSD / md.CurrentPrice
	
	// Format quantity precision (Simplification: assume 3 decimals for now, in real app need ExchangeInfo)
	// A robust way is to fetch ExchangeInfo, but let's try to format string
	qtyStr := fmt.Sprintf("%.3f", quantity) 
	if strings.Contains(symbol, "BTC") { qtyStr = fmt.Sprintf("%.3f", quantity) }
	if strings.Contains(symbol, "ETH") { qtyStr = fmt.Sprintf("%.3f", quantity) }
	if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", quantity) }
	if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", quantity) }

    // Just for close operation, we might want to close all?
    // The logic in simulated exchange was "close existing pos".
    // Here we should check if we have position first.
    if strings.HasPrefix(d.Action, "close") {
        // Find position
        positions := e.GetPositions()
        found := false
        for _, p := range positions {
            if p.Symbol == symbol {
                // Check side
                if (d.Action == "close_long" && p.Side == "long") || (d.Action == "close_short" && p.Side == "short") {
                     qtyStr = fmt.Sprintf("%f", p.Quantity) // Close all
                     found = true
                     // Apply basic formatting again
                     if strings.Contains(symbol, "BTC") { qtyStr = fmt.Sprintf("%.3f", p.Quantity) }
                     if strings.Contains(symbol, "ETH") { qtyStr = fmt.Sprintf("%.3f", p.Quantity) }
                     if strings.Contains(symbol, "SOL") { qtyStr = fmt.Sprintf("%.1f", p.Quantity) }
                     if strings.Contains(symbol, "DOGE") { qtyStr = fmt.Sprintf("%.0f", p.Quantity) }
                }
            }
        }
        if !found {
            return nil // Nothing to close
        }
    }

	service := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		Type(futures.OrderTypeMarket).
		Quantity(qtyStr)

	if strings.HasPrefix(d.Action, "close") {
		service.ReduceOnly(true)
	}

	_, err := service.Do(context.Background())
	if err != nil {
		return fmt.Errorf("Binance Order Failed: %v", err)
	}
	
	log.Printf("Binance Executed: %s %s Qty:%s", d.Action, symbol, qtyStr)
	return nil
}
