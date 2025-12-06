package main

import (
	"math"
)

// Kline K线数据
type Kline struct {
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
	// TakerBuyVolume is only used in backtest_exchange for CSV parsing
	// Not used in live/simulated exchanges
	TakerBuyVolume float64 // optional, only for backtest
}

// calculateEMA 计算EMA
func calculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	// 计算SMA作为初始EMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[i].Close
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(klines); i++ {
		ema = (klines[i].Close-ema)*multiplier + ema
	}

	return ema
}

// calculateMACD 计算MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}

	// 计算12期和26期EMA
	ema12 := calculateEMA(klines, 12)
	ema26 := calculateEMA(klines, 26)

	// MACD = EMA12 - EMA26
	return ema12 - ema26
}

// calculateRSI 计算RSI
func calculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	// 计算初始平均涨跌幅
	for i := 1; i <= period; i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	// 使用Wilder平滑方法计算后续RSI
	for i := period + 1; i < len(klines); i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// calculateATR 计算ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// 计算初始ATR
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder平滑
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateIntradaySeries 计算日内系列数据
func calculateIntradaySeries(klines []Kline) *IntradayData {
	data := &IntradayData{
		MidPrices:   make([]float64, 0, 10),
		EMA20Values: make([]float64, 0, 10),
		MACDValues:  make([]float64, 0, 10),
		RSI7Values:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
		Volume:      make([]float64, 0, 10),
	}

	// 获取最近10个数据点
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)
		data.Volume = append(data.Volume, klines[i].Volume)

		// 计算每个点的EMA20
		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		// 计算每个点的MACD
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		// 计算每个点的RSI
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	// 计算3m ATR14
	data.ATR14 = calculateATR(klines, 14)

	return data
}

// calculateBollingerBands 计算布林带 (基于SMA)
// 返回: upper, middle, lower
func calculateBollingerBands(klines []Kline, period int, stdDevMultiplier float64) (float64, float64, float64) {
	if len(klines) < period {
		return 0, 0, 0
	}

	// 1. 计算 SMA (Middle Band)
	// 取最近 period 个点
	subset := klines[len(klines)-period:]
	sum := 0.0
	for _, k := range subset {
		sum += k.Close
	}
	sma := sum / float64(period)

	// 2. 计算标准差
	varianceSum := 0.0
	for _, k := range subset {
		diff := k.Close - sma
		varianceSum += diff * diff
	}
	variance := varianceSum / float64(period)
	stdDev := math.Sqrt(variance)

	// 3. 计算 Upper 和 Lower
	upper := sma + (stdDev * stdDevMultiplier)
	lower := sma - (stdDev * stdDevMultiplier)

	return upper, sma, lower
}

// calculateLongerTermData 计算长期数据（假定传入的是目标时间框架的K线，例如4h）
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		MACDValues:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	// 计算EMA
	data.EMA20 = calculateEMA(klines, 20)
	data.EMA50 = calculateEMA(klines, 50)

	// 计算ATR
	data.ATR3 = calculateATR(klines, 3)
	data.ATR14 = calculateATR(klines, 14)

	// 计算成交量
	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		// 计算平均成交量
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	// 计算MACD和RSI序列
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// aggregateKlines 将低周期K线按固定数量聚合为高周期K线，例如 20 根3m 聚合为 1 根1h。
func aggregateKlines(klines []Kline, groupSize int) []Kline {
	if groupSize <= 1 || len(klines) == 0 {
		return klines
	}

	totalGroups := len(klines) / groupSize
	if totalGroups == 0 {
		return nil
	}

	res := make([]Kline, 0, totalGroups)
	for g := 0; g < totalGroups; g++ {
		start := g * groupSize
		end := start + groupSize
		if end > len(klines) {
			end = len(klines)
		}
		group := klines[start:end]
		if len(group) == 0 {
			continue
		}

		open := group[0].Open
		close := group[len(group)-1].Close
		high := group[0].High
		low := group[0].Low
		volume := 0.0
		for _, k := range group {
			if k.High > high {
				high = k.High
			}
			if k.Low < low {
				low = k.Low
			}
			volume += k.Volume
		}
		closeTime := group[len(group)-1].CloseTime

		res = append(res, Kline{
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: closeTime,
		})
	}

	return res
}

// calculateVolumeAnalysis 基于一段K线计算相对成交量和主动买卖比
func calculateVolumeAnalysis(klines []Kline, lookback int) *VolumeAnalysis {
	if len(klines) == 0 {
		return nil
	}

	last := klines[len(klines)-1]
	currentVol := last.Volume
	if currentVol <= 0 {
		return &VolumeAnalysis{}
	}

	// 计算过去 lookback 根K线的平均成交量（包含当前这根）
	start := len(klines) - lookback
	if start < 0 {
		start = 0
	}
	sumVol := 0.0
	count := 0
	for i := start; i < len(klines); i++ {
		if klines[i].Volume <= 0 {
			continue
		}
		sumVol += klines[i].Volume
		count++
	}

	relative := 0.0
	if count > 0 && sumVol > 0 {
		avgVol := sumVol / float64(count)
		if avgVol > 0 {
			relative = currentVol / avgVol
		}
	}

	// Taker 买卖比，基于 TakerBuyVolume / (TotalVolume - TakerBuyVolume)
	buyVol := last.TakerBuyVolume
	var ratio float64
	if buyVol <= 0 {
		ratio = 0
	} else if buyVol >= currentVol {
		// 全部视为买盘
		ratio = 999
	} else {
		sellVol := currentVol - buyVol
		if sellVol <= 0 {
			ratio = 999
		} else {
			ratio = buyVol / sellVol
		}
	}

	va := &VolumeAnalysis{
		RelativeVolume3m:  relative,
		TakerBuySellRatio: ratio,
		IsVolumeSpike:     relative >= 2.5,
	}
	return va
}

// calculateRealizedVol 计算最近 lookback 根K线的简单已实现波动率（基于收盘价收益标准差）
func calculateRealizedVol(klines []Kline, lookback int) float64 {
	if len(klines) < lookback+1 {
		return 0
	}

	start := len(klines) - lookback - 1
	if start < 0 {
		start = 0
	}

	var returns []float64
	for i := start; i < len(klines)-1; i++ {
		p0 := klines[i].Close
		p1 := klines[i+1].Close
		if p0 <= 0 || p1 <= 0 {
			continue
		}
		ret := (p1 - p0) / p0
		returns = append(returns, ret)
	}

	if len(returns) == 0 {
		return 0
	}

	// 计算均值
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// 计算标准差
	var varSum float64
	for _, r := range returns {
		diff := r - mean
		varSum += diff * diff
	}
	std := math.Sqrt(varSum / float64(len(returns)))

	return std
}
