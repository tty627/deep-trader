package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
)

const (
	DefaultQuantityPrecision = 3
	SOLQuantityPrecision     = 1
	DOGEQuantityPrecision    = 0
	DefaultPricePrecision    = 4

	defaultAPITimeout = 10 * time.Second
)

// newAPICtx creates a context with a default timeout for Binance API calls.
func newAPICtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultAPITimeout)
}

// BinanceExchange 真实币安交易所 (合约)
type BinanceExchange struct {
	Client           *futures.Client
	MarketData       map[string]*MarketData
	DualSidePosition bool               // true: Hedge mode, false: One-way mode
	InitialEquity    float64            // 本次程序运行期间的基准净值
	positionPeakPnL  map[string]float64 // 内存追踪持仓最高收益率
	// positionOpenTime 记录本次程序运行期间每个符号+方向的首次建仓时间（毫秒）
	// 用于在 brain.go 中计算真实的持仓时长，而不是每次轮询都重置为当前时间。
	positionOpenTime map[string]int64
	History          *TradeHistoryManager // 历史记录管理器
}

// formatQuantity 统一处理不同币种的下单数量精度
func (e *BinanceExchange) formatQuantity(symbol string, quantity float64) string {
	precision := DefaultQuantityPrecision

	// 根据币种设置更严格的精度，避免触发 Binance 的 -1111 精度报错
	if strings.Contains(symbol, "SOL") {
		precision = SOLQuantityPrecision
	} else if strings.Contains(symbol, "DOGE") {
		precision = DOGEQuantityPrecision
	} else if strings.Contains(symbol, "BNB") {
		// BNB 合约通常只支持到 2 位小数，3 位会触发 Precision is over the maximum 错误
		precision = 2
	}

	format := fmt.Sprintf("%%.%df", precision)
	return fmt.Sprintf(format, quantity)
}

// mapOrderSide 根据开/平仓动作和持仓方向映射到 Binance 的下单方向
// actionType: "open" 或 "close"
// positionSide: "LONG" 或 "SHORT"
func (e *BinanceExchange) mapOrderSide(actionType string, positionSide string) (futures.SideType, futures.PositionSideType) {
	actionType = strings.ToLower(actionType)
	pos := strings.ToUpper(positionSide)

	var side futures.SideType
	var posSide futures.PositionSideType

	switch pos {
	case "LONG":
		posSide = futures.PositionSideTypeLong
		if actionType == "open" {
			side = futures.SideTypeBuy
		} else {
			side = futures.SideTypeSell
		}
	case "SHORT":
		posSide = futures.PositionSideTypeShort
		if actionType == "open" {
			side = futures.SideTypeSell
		} else {
			side = futures.SideTypeBuy
		}
	default:
		// 默认按 LONG 处理，防止 positionSide 异常时崩溃
		posSide = futures.PositionSideTypeLong
		if actionType == "open" {
			side = futures.SideTypeBuy
		} else {
			side = futures.SideTypeSell
		}
	}

	return side, posSide
}

// SetLeverage 手动调整某个合约的杠杆
func (e *BinanceExchange) SetLeverage(symbol string, leverage int) error {
	if leverage <= 0 {
		return fmt.Errorf("leverage must be > 0, got %d", leverage)
	}

	ctx, cancel := newAPICtx()
	defer cancel()

	_, err := e.Client.NewChangeLeverageService().
		Symbol(symbol).
		Leverage(leverage).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("change leverage failed for %s: %w", symbol, err)
	}

	log.Printf("✅ Set leverage for %s to %dx", symbol, leverage)
	return nil
}

// NewBinanceExchange 创建带可选代理和状态跟踪的 BinanceExchange 实例
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
		Client:           client,
		MarketData:       make(map[string]*MarketData),
		positionPeakPnL:  make(map[string]float64),
		positionOpenTime: make(map[string]int64),
		History:          NewTradeHistoryManager(),
	}

	// 尝试从本地恢复历史开仓时间（用于跨重启维持持仓时长）
	ex.loadPositionOpenTimes()

	// 尝试同步最近的成交历史（用于前端展示），并定期刷新，捕捉外部平仓/止盈/止损
	go func() {
		// 启动时先同步一次
		ex.SyncTradeHistory()
		// 之后每 2 分钟同步一次
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ex.SyncTradeHistory()
		}
	}()

	// 检查当前持仓模式（单向 / 对冲），用于后续是否使用 positionSide / reduceOnly
	ctx, cancel := newAPICtx()
	defer cancel()
	if pm, err := client.NewGetPositionModeService().Do(ctx); err != nil {
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
		// 1. 获取 3m K线 (用于日内数据，micro-structure)
		klines3m, err := e.fetchKlines(symbol, "3m", 60)
		if err != nil {
			log.Printf("Fetch 3m klines failed for %s: %v", symbol, err)
			continue
		}

		// 1.1 获取 5m K线（用于更稳定的入场周期）
		klines5m, err := e.fetchKlines(symbol, "5m", 60)
		if err != nil {
			log.Printf("Fetch 5m klines failed for %s: %v", symbol, err)
			klines5m = nil
		}

		// 2. 获取 1h K线 (用于中期趋势)
		klines1h, err := e.fetchKlines(symbol, "1h", 60)
		if err != nil {
			log.Printf("Fetch 1h klines failed for %s: %v", symbol, err)
			// 不中断，后续仅缺少 1h 指标
			klines1h = nil
		}

		// 3. 获取 4h K线 (用于长期趋势)
		klines4h, err := e.fetchKlines(symbol, "4h", 60)
		if err != nil {
			log.Printf("Fetch 4h klines failed for %s: %v", symbol, err)
			continue
		}

		if len(klines3m) == 0 || len(klines4h) == 0 {
			continue
		}

		// 4. 计算基础数据
		currentKline := klines3m[len(klines3m)-1]
		currentPrice := currentKline.Close

		// 5. 计算日内(3m)指标
		ema20 := calculateEMA(klines3m, 20)
		macd := calculateMACD(klines3m)
		rsi7 := calculateRSI(klines3m, 7)

		// 5.1 计算 5m 指标（若可用）
		var ema20_5m, macd_5m, rsi14_5m, atr14_5m float64
		if len(klines5m) > 0 {
			ema20_5m = calculateEMA(klines5m, 20)
			macd_5m = calculateMACD(klines5m)
			rsi14_5m = calculateRSI(klines5m, 14)
			atr14_5m = calculateATR(klines5m, 14)
		}

		// 6. 聚合 15m / 30m K线并计算指标 (5 * 3m = 15m, 10 * 3m = 30m)
		klines15m := aggregateKlines(klines3m, 5)
		klines30m := aggregateKlines(klines3m, 10)
		var ema20_15m, macd_15m, rsi14_15m, atr14_15m float64
		if len(klines15m) > 0 {
			ema20_15m = calculateEMA(klines15m, 20)
			macd_15m = calculateMACD(klines15m)
			rsi14_15m = calculateRSI(klines15m, 14)
			atr14_15m = calculateATR(klines15m, 14)
		}
		var ema20_30m, macd_30m, rsi14_30m, atr14_30m float64
		if len(klines30m) > 0 {
			ema20_30m = calculateEMA(klines30m, 20)
			macd_30m = calculateMACD(klines30m)
			rsi14_30m = calculateRSI(klines30m, 14)
			atr14_30m = calculateATR(klines30m, 14)
		}

		// 7. 计算 1h 指标（如果可用）
		var ema20_1h, macd_1h, rsi14_1h, atr14_1h float64
		if len(klines1h) > 0 {
			ema20_1h = calculateEMA(klines1h, 20)
			macd_1h = calculateMACD(klines1h)
			rsi14_1h = calculateRSI(klines1h, 14)
			atr14_1h = calculateATR(klines1h, 14)
		}

		// 7. 计算价格变化
		priceChange1h := 0.0
		if len(klines1h) >= 2 {
			// 1h 变化: 最近两根 1h K 线
			prev := klines1h[len(klines1h)-2].Close
			if prev > 0 {
				priceChange1h = (currentPrice - prev) / prev * 100
			}
		} else if len(klines3m) >= 21 {
			// 回退方案：仍然基于 3m 近似 1h 变化
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

		// 日内变化: 计算从当日 00:00 UTC 开始的价格变化
		priceChangeDay := 0.0
		if dayOpenPrice, err := e.fetchDayOpenPrice(symbol); err == nil && dayOpenPrice > 0 {
			priceChangeDay = (currentPrice - dayOpenPrice) / dayOpenPrice * 100
		}

		// 8. 获取资金费率和持仓量
		fundingRate, _ := e.fetchFundingRate(symbol)
		oiData, _ := e.fetchOpenInterest(symbol)
		lsRatio, _ := e.fetchLongShortRatio(symbol)

		// 记录并计算 OI 变动
		if oiData != nil {
			tracker.RecordOI(symbol, oiData.Latest)
			oiData.Change1h = tracker.GetOIChange(symbol, 1*time.Hour)
			oiData.Change4h = tracker.GetOIChange(symbol, 4*time.Hour)
		}

		// 9. 计算序列数据
		intraday := calculateIntradaySeries(klines3m)
		longerTerm := calculateLongerTermData(klines4h)

		// 估算爆仓数据 (New) - 必须在 intraday 计算后
		var liqData *LiquidationData
		if oiData != nil {
			liqData = e.estimateLiquidation(symbol, oiData.Change1h, intraday.ATR14)
		}

		// 计算布林带 (3m)
		bbUpper, bbMid, bbLower := calculateBollingerBands(klines3m, 20, 2.0)

		// 成交量与情绪分析
		volAnalysis := calculateVolumeAnalysis(klines3m, 20)

		// 使用 1h K 线近似计算已实现波动率
		vol1h := 0.0
		if len(klines1h) > 0 {
			vol1h = calculateRealizedVol(klines1h, 20)
		}

		// 构造情绪数据（基于资金费率、多空比和波动率的简单本地 Fear & Greed）
		sentiment := &SentimentData{
			Volatility1h: vol1h,
		}

		// 简单打分：从50出发，根据资金费率和多空比调整
		fgScore := 50
		if fundingRate > 0 {
			fgScore += 5
		}
		if fundingRate > 0.0005 {
			fgScore += 10
		}
		if fundingRate < 0 {
			fgScore -= 5
		}
		if fundingRate < -0.0005 {
			fgScore -= 10
		}
		localSentiment := "Neutral"
		if lsRatio != nil {
			if lsRatio.Ratio > 1.2 {
				fgScore += 10
				localSentiment = "Bullish_Crowded"
			} else if lsRatio.Ratio < 0.8 {
				fgScore -= 10
				localSentiment = "Bearish_Crowded"
			}
		}
		if fgScore < 0 {
			fgScore = 0
		}
		if fgScore > 100 {
			fgScore = 100
		}
		sentiment.FearGreedIndex = fgScore
		sentiment.LocalSentiment = localSentiment
		// 根据分数生成标签
		switch {
		case fgScore <= 20:
			sentiment.FearGreedLabel = "Extreme Fear"
		case fgScore <= 40:
			sentiment.FearGreedLabel = "Fear"
		case fgScore < 60:
			sentiment.FearGreedLabel = "Neutral"
		case fgScore < 80:
			sentiment.FearGreedLabel = "Greed"
		default:
			sentiment.FearGreedLabel = "Extreme Greed"
		}

		// 10. 构建 MarketData
			e.MarketData[symbol] = &MarketData{
				Symbol:         symbol,
				CurrentPrice:   currentPrice,
				PriceChange1h:  priceChange1h,
				PriceChange4h:  priceChange4h,
				PriceChangeDay: priceChangeDay,
				Volume24h:      0, // 不再单独请求24h ticker，节省API额度

				// 3m / 日内快照
				CurrentEMA20: ema20,
				CurrentMACD:  macd,
				CurrentRSI7:  rsi7,

				// 5m 入场周期
				EMA20_5m:  ema20_5m,
				MACD_5m:   macd_5m,
				RSI14_5m:  rsi14_5m,
				ATR14_5m:  atr14_5m,

				// 15m 日内趋势周期
				EMA20_15m: ema20_15m,
				MACD_15m:  macd_15m,
				RSI14_15m: rsi14_15m,
				ATR14_15m: atr14_15m,

				// 1h 背景趋势
				EMA20_1h: ema20_1h,
				MACD_1h:  macd_1h,
				RSI14_1h: rsi14_1h,
				ATR14_1h: atr14_1h,

				// 30m 辅助周期
				EMA20_30m: ema20_30m,
				MACD_30m:  macd_30m,
				RSI14_30m: rsi14_30m,
				ATR14_30m: atr14_30m,

			BollingerUpper:  bbUpper,
			BollingerMiddle: bbMid,
			BollingerLower:  bbLower,

			FundingRate:  fundingRate,
			OpenInterest: oiData,

			LongShortRatio: lsRatio,
			Liquidation:    liqData,

			VolumeAnalysis:    volAnalysis,
			Sentiment:         sentiment,
			IntradaySeries:    intraday,
			LongerTermContext: longerTerm,
		}
	}
	return nil
}

// fetchKlines 获取K线数据
func (e *BinanceExchange) fetchKlines(symbol, interval string, limit int) ([]Kline, error) {
	ctx, cancel := newAPICtx()
	defer cancel()

	klines, err := e.Client.NewKlinesService().Symbol(symbol).Interval(interval).Limit(limit).Do(ctx)
	if err != nil {
		return nil, err
	}

	var res []Kline
	for _, k := range klines {
		open, err := strconv.ParseFloat(k.Open, 64)
		if err != nil {
			log.Printf("⚠️ failed to parse kline open for %s: %v", symbol, err)
			continue
		}
		high, err := strconv.ParseFloat(k.High, 64)
		if err != nil {
			log.Printf("⚠️ failed to parse kline high for %s: %v", symbol, err)
			continue
		}
		low, err := strconv.ParseFloat(k.Low, 64)
		if err != nil {
			log.Printf("⚠️ failed to parse kline low for %s: %v", symbol, err)
			continue
		}
		close, err := strconv.ParseFloat(k.Close, 64)
		if err != nil {
			log.Printf("⚠️ failed to parse kline close for %s: %v", symbol, err)
			continue
		}
		volume, err := strconv.ParseFloat(k.Volume, 64)
		if err != nil {
			log.Printf("⚠️ failed to parse kline volume for %s: %v", symbol, err)
			continue
		}

		takerBuyVol := 0.0
		if k.TakerBuyBaseAssetVolume != "" {
			if v, err := strconv.ParseFloat(k.TakerBuyBaseAssetVolume, 64); err == nil {
				takerBuyVol = v
			} else {
				log.Printf("⚠️ failed to parse taker buy volume for %s: %v", symbol, err)
			}
		}

		res = append(res, Kline{
			Open:           open,
			High:           high,
			Low:            low,
			Close:          close,
			Volume:         volume,
			CloseTime:      k.CloseTime,
			TakerBuyVolume: takerBuyVol,
		})
	}
	return res, nil
}

// fetchFundingRate 获取资金费率
func (e *BinanceExchange) fetchFundingRate(symbol string) (float64, error) {
	ctx, cancel := newAPICtx()
	defer cancel()

	res, err := e.Client.NewPremiumIndexService().Symbol(symbol).Do(ctx)
	if err != nil {
		return 0, err
	}
	if len(res) > 0 {
		rate, err := strconv.ParseFloat(res[0].LastFundingRate, 64)
		if err != nil {
			log.Printf("⚠️ failed to parse funding rate for %s: %v", symbol, err)
			return 0, err
		}
		return rate, nil
	}
	return 0, fmt.Errorf("no data")
}

// fetchOpenInterest 获取持仓量
func (e *BinanceExchange) fetchOpenInterest(symbol string) (*OIData, error) {
	ctx, cancel := newAPICtx()
	defer cancel()

	res, err := e.Client.NewGetOpenInterestService().Symbol(symbol).Do(ctx)
	if err != nil {
		return nil, err
	}
	val, err := strconv.ParseFloat(res.OpenInterest, 64)
	if err != nil {
		log.Printf("⚠️ failed to parse open interest for %s: %v", symbol, err)
		return nil, err
	}
	return &OIData{Latest: val, Average: val}, nil
}

// fetchLongShortRatio 获取大户持仓多空比 (Accounts)
func (e *BinanceExchange) fetchLongShortRatio(symbol string) (*LongShortData, error) {
	// 尝试使用 NewTopLongShortAccountRatioService (去掉 Get)
	// 如果库版本不支持，这里可能会依然报错，备选方案是暂不获取
	ctx, cancel := newAPICtx()
	defer cancel()

	res, err := e.Client.NewTopLongShortAccountRatioService().
		Symbol(symbol).
		Period("5m").
		Limit(1).
		Do(ctx)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("no data")
	}

	r := res[0]
	ratio, err := strconv.ParseFloat(r.LongShortRatio, 64)
	if err != nil {
		log.Printf("⚠️ failed to parse long/short ratio for %s: %v", symbol, err)
		return nil, err
	}
	longVal, err := strconv.ParseFloat(r.LongAccount, 64)
	if err != nil {
		log.Printf("⚠️ failed to parse long account ratio for %s: %v", symbol, err)
		return nil, err
	}
	shortVal, err := strconv.ParseFloat(r.ShortAccount, 64)
	if err != nil {
		log.Printf("⚠️ failed to parse short account ratio for %s: %v", symbol, err)
		return nil, err
	}

	return &LongShortData{
		Ratio:    ratio,
		LongPct:  longVal,
		ShortPct: shortVal,
	}, nil
}

// estimateLiquidation 估算爆仓数据 (Mock/Simulated for now)
// 真实全网爆仓需要 websocket 聚合，这里通过 OI 下降 + 价格剧烈波动来模拟估算
func (e *BinanceExchange) estimateLiquidation(symbol string, oiChange float64, priceVolatility float64) *LiquidationData {
	// 只有当 OI 显著下降 (例如 < -0.5%) 且 价格波动较大时，才判定为爆仓主导
	if oiChange >= -0.5 {
		return nil
	}
	
	// 模拟算法：OI 减少量的一定比例视为爆仓
	// 假设 1% OI 减少对应约 500k - 5M 不等的爆仓 (取决于币种市值，这里简化)
	estimatedAmount := math.Abs(oiChange) * 100000 // 基础系数
	if strings.Contains(symbol, "BTC") || strings.Contains(symbol, "ETH") {
		estimatedAmount *= 10 
	}

	return &LiquidationData{
		Symbol: symbol,
		Amount1h: estimatedAmount,
		Amount4h: estimatedAmount * 2.5, // 简单外推
		SideRatio: 1.5, // 默认假设
	}
}

// GetAccountInfo 获取账户信息
func (e *BinanceExchange) GetAccountInfo() AccountInfo {
	ctx, cancel := newAPICtx()
	defer cancel()

	acc, err := e.Client.NewGetAccountService().Do(ctx)
	if err != nil {
		log.Printf("获取账户信息失败: %v", err)
		return AccountInfo{}
	}

	var totalWalletBalance, totalUnrealizedPnL, totalMarginUsed float64

	totalWalletBalance, err = strconv.ParseFloat(acc.TotalWalletBalance, 64)
	if err != nil {
		log.Printf("⚠️ 解析 TotalWalletBalance 失败: %v", err)
	}
	totalUnrealizedPnL, err = strconv.ParseFloat(acc.TotalUnrealizedProfit, 64)
	if err != nil {
		log.Printf("⚠️ 解析 TotalUnrealizedProfit 失败: %v", err)
	}
	totalMarginUsed, err = strconv.ParseFloat(acc.TotalInitialMargin, 64)
	if err != nil {
		log.Printf("⚠️ 解析 TotalInitialMargin 失败: %v", err)
	}

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

// positionOpenTimeFile 为本地持久化文件名（相对于程序工作目录）
const positionOpenTimeFile = "position_open_time.json"

// loadPositionOpenTimes 从本地 JSON 文件加载 positionOpenTime，忽略不存在/解析错误
func (e *BinanceExchange) loadPositionOpenTimes() {
	data, err := os.ReadFile(positionOpenTimeFile)
	if err != nil {
		// 文件不存在或其它读错误时静默忽略，只在 debug 时看日志即可
		if !os.IsNotExist(err) {
			log.Printf("⚠️ 读取 %s 失败: %v", positionOpenTimeFile, err)
		}
		return
	}

	var m map[string]int64
	if err := json.Unmarshal(data, &m); err != nil {
		log.Printf("⚠️ 解析 %s 失败: %v", positionOpenTimeFile, err)
		return
	}

	if e.positionOpenTime == nil {
		e.positionOpenTime = make(map[string]int64)
	}
	for k, v := range m {
		// 只接受合理的时间戳（>0），防止脏数据
		if v > 0 {
			e.positionOpenTime[k] = v
		}
	}
}

// savePositionOpenTimes 将当前的 positionOpenTime 持久化到本地 JSON 文件
func (e *BinanceExchange) savePositionOpenTimes() {
	if e.positionOpenTime == nil {
		return
	}
	data, err := json.MarshalIndent(e.positionOpenTime, "", "  ")
	if err != nil {
		log.Printf("⚠️ 序列化 positionOpenTime 失败: %v", err)
		return
	}
	if err := os.WriteFile(positionOpenTimeFile, data, 0o644); err != nil {
		log.Printf("⚠️ 写入 %s 失败: %v", positionOpenTimeFile, err)
	}
}

func (e *BinanceExchange) getPositionsFromRisk() []PositionInfo {
	ctx, cancel := newAPICtx()
	defer cancel()

	risks, err := e.Client.NewGetPositionRiskService().Do(ctx)
	if err != nil {
		log.Printf("GetPositionRisk Error: %v", err)
		return nil
	}

	// 使用当前时间作为本轮扫描的时间基准
	nowMs := time.Now().UnixMilli()
	activeKeys := make(map[string]bool)
	var result []PositionInfo

	for _, p := range risks {
		amt, err := strconv.ParseFloat(p.PositionAmt, 64)
		if err != nil {
			log.Printf("⚠️ Failed to parse PositionAmt for %s: %v", p.Symbol, err)
			continue
		}
		if amt == 0 {
			continue
		}

		side := "long"
		if amt < 0 {
			side = "short"
			amt = -amt
		}

		entryPrice, err := strconv.ParseFloat(p.EntryPrice, 64)
		if err != nil {
			log.Printf("⚠️ Failed to parse EntryPrice for %s: %v", p.Symbol, err)
			continue
		}
		markPrice, err := strconv.ParseFloat(p.MarkPrice, 64)
		if err != nil {
			log.Printf("⚠️ Failed to parse MarkPrice for %s: %v", p.Symbol, err)
			continue
		}
		unRealizedProfit, err := strconv.ParseFloat(p.UnRealizedProfit, 64)
		if err != nil {
			log.Printf("⚠️ Failed to parse UnRealizedProfit for %s: %v", p.Symbol, err)
			continue
		}
		leverage, err := strconv.Atoi(p.Leverage)
		if err != nil {
			log.Printf("⚠️ Failed to parse Leverage for %s: %v", p.Symbol, err)
			continue
		}
		liquidationPrice, err := strconv.ParseFloat(p.LiquidationPrice, 64)
		if err != nil {
			log.Printf("⚠️ Failed to parse LiquidationPrice for %s: %v", p.Symbol, err)
			continue
		}

		// Calculate margin used approx
		marginUsed := (amt * markPrice) / float64(leverage)
		unrealizedPnLPct := 0.0
		if marginUsed > 0 {
			unrealizedPnLPct = (unRealizedProfit / marginUsed) * 100
		}

		// 更新并获取最高收益率
		currentPeak := e.positionPeakPnL[p.Symbol]
		if unrealizedPnLPct > currentPeak {
			e.positionPeakPnL[p.Symbol] = unrealizedPnLPct
			currentPeak = unrealizedPnLPct
		}

		// 使用 "side:symbol" 作为 key 跟踪首次建仓时间，区分多空方向
		openKey := fmt.Sprintf("%s:%s", side, p.Symbol)
		activeKeys[openKey] = true
		openTime, ok := e.positionOpenTime[openKey]
		if !ok || openTime == 0 {
			openTime = nowMs
			e.positionOpenTime[openKey] = openTime
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
			// 这里的 UpdateTime 表示本次持仓方向的首次建仓时间（本程序运行期间）
			UpdateTime:       openTime,
		}

		result = append(result, info)
	}

	// 清理已经平仓的符号+方向，防止内存泄漏或错误继承旧的开仓时间
	for key := range e.positionOpenTime {
		if !activeKeys[key] {
			delete(e.positionOpenTime, key)
		}
	}

	// 每次刷新持仓后，把最新的开仓时间字典落盘，以便重启后还能恢复持仓时长
	go e.savePositionOpenTimes()

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

// GetTradeHistory 获取历史记录
func (e *BinanceExchange) GetTradeHistory() []TradeRecord {
	if e.History != nil {
		return e.History.GetHistory()
	}
	return nil
}

// SyncTradeHistory 从币安同步最近的成交历史
func (e *BinanceExchange) SyncTradeHistory() {
	// 只获取主要关注的几个币种，避免 API 滥用
	symbols := []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "DOGEUSDT"}
	
	for _, symbol := range symbols {
		// Binance futures uses NewListAccountTradeService instead of NewListTradesService
		ctx, cancel := newAPICtx()
		trades, err := e.Client.NewListAccountTradeService().Symbol(symbol).Limit(10).Do(ctx)
		cancel()
		if err != nil {
			log.Printf("⚠️ 同步 %s 历史成交失败: %v", symbol, err)
			continue
		}

		// 倒序遍历（API返回是旧->新，我们希望最新的先被添加，AddRecord 会自动放在最前）
		// 但 AddRecord 是 prepend，所以正序遍历添加，最后最新的会在最前面？
		// 不，AddRecord 是 prepend (insert at 0)。
		// API 返回 [Oldest, ..., Newest]。
		// 如果我们按顺序添加：Add(Oldest) -> [Oldest], Add(Newest) -> [Newest, Oldest]。这是我们想要的。
		for _, t := range trades {
			price, err := strconv.ParseFloat(t.Price, 64)
			if err != nil {
				log.Printf("⚠️ 解析成交价格失败 %s: %v", symbol, err)
				continue
			}
			qty, err := strconv.ParseFloat(t.Quantity, 64)
			if err != nil {
				log.Printf("⚠️ 解析成交数量失败 %s: %v", symbol, err)
				continue
			}
			pnl, err := strconv.ParseFloat(t.RealizedPnl, 64)
			if err != nil {
				log.Printf("⚠️ 解析成交 PnL 失败 %s: %v", symbol, err)
				pnl = 0
			}
			
			// 转换时间
			tm := time.UnixMilli(t.Time)
			
			action := "buy"
			if t.Side == "SELL" {
				action = "sell"
			}
			
			// 尝试判断是开仓还是平仓（仅做简单推断，基于 realizedPnl）
			// 如果 realizedPnl != 0，通常是平仓（或部分平仓）
			// 严格来说 Maker/Taker 都会有 PnL 吗？不，只有减仓才有 Realized PnL。
			// 开仓时 Realized PnL 通常为 0 (扣除手续费前)。
			
			displayAction := "open_" + action // 默认
			if pnl != 0 {
				// 实际上 Binance API 的 Side 是方向。
				// 这里的 action 只是展示用。
				// 如果有已实现盈亏，标记为 close/partial
				if t.Side == "BUY" {
					displayAction = "close_short" // 买入平空？不一定，也可能是开多。
					// 简化：如果有 PnL，就假设是平仓操作
					displayAction = "close/profit"
				} else {
					displayAction = "close/profit"
				}
			} else {
				// PnL 为 0，可能是开仓
				if t.Side == "BUY" {
					displayAction = "open_long"
				} else {
					displayAction = "open_short"
				}
			}

			rec := TradeRecord{
				Time:       tm.Format("2006-01-02 15:04:05"),
				Symbol:     t.Symbol,
				Side:       strings.ToLower(string(t.Side)),
				Action:     displayAction,
				EntryPrice: price, // 这里只能用成交价代替
				ExitPrice:  price,
				Quantity:   qty,
				PnL:        pnl,
				PnLPct:     0, // 无法准确计算历史百分比
				Reason:     "Synced from Binance",
			}
			
			// 添加到历史记录
			if e.History != nil {
				e.History.AddRecord(rec)
			}
		}
	}
	log.Println("✅ 历史成交记录同步完成")
}

// ExecuteDecision 执行交易决策
func (e *BinanceExchange) ExecuteDecision(d Decision) error {
	symbol := d.Symbol
	
	// 0. 过滤无效决策
	if d.Action == "wait" || d.Symbol == "NONE" || d.Symbol == "" {
		return nil
	}

	// 1. 调整杠杆（如果给了建议杠杆，且是开仓操作）
	// 只有在 open_long / open_short 时才去调整杠杆，避免平仓或观望时触发
	if d.Leverage > 0 && (d.Action == "open_long" || d.Action == "open_short") {
		// 检查当前杠杆是否已经是目标值（简单优化）
		ctx, cancel := newAPICtx()
		_, err := e.Client.NewChangeLeverageService().Symbol(symbol).Leverage(d.Leverage).Do(ctx)
		cancel()
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

	// 使用统一的数量精度处理函数
	qtyStr := e.formatQuantity(symbol, quantity)

	// 对于 close_* 指令，忽略 AI 的 position_size_usd，直接按实际持仓全平
	if strings.HasPrefix(d.Action, "close") {
		positions := e.GetPositions()
		found := false
		var currentPos PositionInfo

		for _, p := range positions {
			if p.Symbol != symbol {
				continue
			}
			// 匹配方向
			if (d.Action == "close_long" && p.Side == "long") || (d.Action == "close_short" && p.Side == "short") {
				currentPos = p
				// 全平当前仓位，使用统一格式化
				qtyStr = e.formatQuantity(symbol, p.Quantity)
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("no matching position to %s for %s", d.Action, symbol)
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
		ctx, cancel := newAPICtx()
		defer cancel()
		_, err := service.Do(ctx)
		if err != nil {
			return fmt.Errorf("Binance Order Failed: %v", err)
		}

		log.Printf("Binance Executed: %s %s Qty:%s (dualSide=%v)", d.Action, symbol, qtyStr, e.DualSidePosition)

		// 记录历史
		if e.History != nil {
			rec := TradeRecord{
				Time:       time.Now().Format("15:04:05"),
				Symbol:     symbol,
				Side:       currentPos.Side,
				Action:     d.Action,
				EntryPrice: currentPos.EntryPrice,
				ExitPrice:  md.CurrentPrice, // 近似为当前市价
				Quantity:   currentPos.Quantity,
				PnL:        currentPos.UnrealizedPnL, // 近似为未实现盈亏 (市价单滑点可能导致差异)
				PnLPct:     currentPos.UnrealizedPnLPct,
				Reason:     d.Reasoning,
			}
			e.History.AddRecord(rec)
		}

		// 如果是平仓操作，重置最高收益率记录，并尝试清理相关止盈/止损挂单
		delete(e.positionPeakPnL, symbol)

		// 平仓后尽量清理所有止损/止盈挂单，避免遗留订单
		if err := e.CancelStopLossOrders(symbol); err != nil {
			log.Printf("⚠️ 平仓后取消止损挂单失败 %s: %v", symbol, err)
		}
		if err := e.CancelTakeProfitOrders(symbol); err != nil {
			log.Printf("⚠️ 平仓后取消止盈挂单失败 %s: %v", symbol, err)
		}
	}

	// 5. 执行开仓下单 (此前代码漏掉了这一步)
	if d.Action == "open_long" || d.Action == "open_short" {
		service := e.Client.NewCreateOrderService().
			Symbol(symbol).
			Side(side).
			Type(futures.OrderTypeMarket).
			Quantity(qtyStr)

		if usePositionSide {
			service = service.PositionSide(positionSide)
		}

		ctx, cancel := newAPICtx()
		defer cancel()
		_, err := service.Do(ctx)
		if err != nil {
			return fmt.Errorf("Binance Open Order Failed: %v", err)
		}
		log.Printf("✅ Binance Open Order Success: %s %s Qty:%s", d.Action, symbol, qtyStr)
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

// handleUpdateStopLoss handles stop loss updates
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
	
	// 使用统一精度格式化
	qtyStr := e.formatQuantity(symbol, closeQty)

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

	ctx, cancel := newAPICtx()
	defer cancel()
	_, err := service.Do(ctx)
	if err != nil {
		return fmt.Errorf("partial close failed: %v", err)
	}

	log.Printf("✅ Partial Close %s %s: %s (%.1f%%)", symbol, currentPos.Side, qtyStr, pct)

	// 记录历史 (部分平仓)
	if e.History != nil {
		// 估算部分平仓的 PnL
		pnl := currentPos.UnrealizedPnL * (pct / 100.0)
		rec := TradeRecord{
			Time:       time.Now().Format("15:04:05"),
			Symbol:     symbol,
			Side:       currentPos.Side,
			Action:     "partial_close",
			EntryPrice: currentPos.EntryPrice,
			ExitPrice:  currentPos.MarkPrice, // 使用标记价格近似
			Quantity:   closeQty,
			PnL:        pnl,
			PnLPct:     currentPos.UnrealizedPnLPct,
			Reason:     d.Reasoning,
		}
		e.History.AddRecord(rec)
	}
	
	// 如果是 100% 平仓，也清理记录并尝试清理止盈/止损挂单
	if pct >= 99.9 {
		delete(e.positionPeakPnL, symbol)

		if err := e.CancelStopLossOrders(symbol); err != nil {
			log.Printf("⚠️ 部分平仓(≈100%%)后取消止损挂单失败 %s: %v", symbol, err)
		}
		if err := e.CancelTakeProfitOrders(symbol); err != nil {
			log.Printf("⚠️ 部分平仓(≈100%%)后取消止盈挂单失败 %s: %v", symbol, err)
		}
	}
	return nil
}

// CancelStopLossOrders 取消所有止损单
func (e *BinanceExchange) CancelStopLossOrders(symbol string) error {
	ctx, cancel := newAPICtx()
	defer cancel()

	openOrders, err := e.Client.NewListOpenOrdersService().Symbol(symbol).Do(ctx)
	if err != nil {
		return err
	}
	for _, o := range openOrders {
		if o.Type == futures.OrderTypeStopMarket || o.Type == futures.OrderTypeStop {
			cCtx, cCancel := newAPICtx()
			_, _ = e.Client.NewCancelOrderService().Symbol(symbol).OrderID(o.OrderID).Do(cCtx)
			cCancel()
		}
	}
	return nil
}

// CancelTakeProfitOrders 取消所有止盈单
func (e *BinanceExchange) CancelTakeProfitOrders(symbol string) error {
	ctx, cancel := newAPICtx()
	defer cancel()

	openOrders, err := e.Client.NewListOpenOrdersService().Symbol(symbol).Do(ctx)
	if err != nil {
		return err
	}
	for _, o := range openOrders {
		if o.Type == futures.OrderTypeTakeProfitMarket || o.Type == futures.OrderTypeTakeProfit {
			cCtx, cCancel := newAPICtx()
			_, _ = e.Client.NewCancelOrderService().Symbol(symbol).OrderID(o.OrderID).Do(cCtx)
			cCancel()
		}
	}
	return nil
}

// CancelAllOrders 取消该币种的所有挂单
func (e *BinanceExchange) CancelAllOrders(symbol string) error {
	ctx, cancel := newAPICtx()
	defer cancel()
	return e.Client.NewCancelAllOpenOrdersService().Symbol(symbol).Do(ctx)
}

// SetStopLoss 设置止损单
func (e *BinanceExchange) SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error {
	// 使用统一的方向映射
	side, posSide := e.mapOrderSide("close", positionSide)

	// 使用统一的数量格式化
	qtyStr := e.formatQuantity(symbol, quantity)

	ctx, cancel := newAPICtx()
	defer cancel()

	_, err := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.OrderTypeStopMarket).
		StopPrice(fmt.Sprintf("%.4f", stopPrice)). // 简化处理，全部保留4位小数
		Quantity(qtyStr).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true). // 触发后平仓
		Do(ctx)

	return err
}

// SetTakeProfit 设置止盈单
func (e *BinanceExchange) SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error {
	// 使用统一的方向映射
	side, posSide := e.mapOrderSide("close", positionSide)

	// 使用统一的数量格式化
	qtyStr := e.formatQuantity(symbol, quantity)

	ctx, cancel := newAPICtx()
	defer cancel()

	_, err := e.Client.NewCreateOrderService().
		Symbol(symbol).
		Side(side).
		PositionSide(posSide).
		Type(futures.OrderTypeTakeProfitMarket).
		StopPrice(fmt.Sprintf("%.4f", takeProfitPrice)).
		Quantity(qtyStr).
		WorkingType(futures.WorkingTypeContractPrice).
		ClosePosition(true).
		Do(ctx)

	return err
}

// fetchDayOpenPrice 获取当日开盘价 (从 00:00 UTC 开始的第一根 1d K 线的开盘价)
func (e *BinanceExchange) fetchDayOpenPrice(symbol string) (float64, error) {
	ctx, cancel := newAPICtx()
	defer cancel()

	// 获取最近的日线K线，只需要最近的两根
	klines, err := e.Client.NewKlinesService().Symbol(symbol).Interval("1d").Limit(2).Do(ctx)
	if err != nil {
		return 0, err
	}
	if len(klines) == 0 {
		return 0, fmt.Errorf("no daily kline data for %s", symbol)
	}

	// 最后一根K线就是当日的K线，其开盘价就是当日 00:00 UTC 的价格
	lastKline := klines[len(klines)-1]
	openPrice, err := strconv.ParseFloat(lastKline.Open, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse day open price for %s: %w", symbol, err)
	}

	return openPrice, nil
}
