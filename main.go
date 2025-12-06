package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	// CLI 子命令：手动设置某个交易对杠杆
	if len(os.Args) == 4 && os.Args[1] == "set-lev" {
		symbol := os.Args[2]
		lev, err := strconv.Atoi(os.Args[3])
		if err != nil {
			log.Fatalf("无效的杠杆倍数: %v", err)
		}

		cfg, err := LoadConfig()
		if err != nil {
			log.Fatalf("加载配置失败: %v", err)
		}
		if cfg.BinanceAPIKey == "" || cfg.BinanceSecretKey == "" {
			log.Fatalf("set-lev 只能在实盘模式下使用，请在 config.local.json 中配置 binance_api_key / binance_secret_key")
		}

		ex := NewBinanceExchange(cfg.BinanceAPIKey, cfg.BinanceSecretKey, cfg.BinanceProxyURL)
		if err := ex.SetLeverage(symbol, lev); err != nil {
			log.Fatalf("设置杠杆失败: %v", err)
		}
		fmt.Printf("已将 %s 杠杆设置为 %dx\n", symbol, lev)
		return
	}

	fmt.Println("╔═══════════════════════════════════════════════════╗")
	fmt.Println("║       Simple AI Trader (nofx-like core)           ║")
	fmt.Println("║       模拟账户 | 真实行情 | AI全权决策           ║")
	fmt.Println("╚═══════════════════════════════════════════════════╝")

	// 统一从本地配置文件 / 环境变量读取
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Println(err.Error())
		fmt.Println("👉 你可以直接在项目根目录创建 config.local.json 来配置 API")
		return
	}

	// 初始化组件
	var exchange Exchange
	binanceKey := cfg.BinanceAPIKey
	binanceSecret := cfg.BinanceSecretKey

	if binanceKey != "" && binanceSecret != "" {
		fmt.Println("🚀 使用真实币安交易所 (Real Trading Mode)")
		exchange = NewBinanceExchange(binanceKey, binanceSecret, cfg.BinanceProxyURL)
	} else {
		fmt.Println("🧪 使用模拟交易所 (Simulation Mode)")
		exchange = NewSimulatedExchange(1000.0) // 1000 U 初始资金
	}

	brain := NewAIBrain(cfg.AIAPIKey, cfg.AIAPIURL, cfg.AIModel, cfg.BinanceProxyURL)

	// 启动 Web 监控（携带默认循环周期配置）
	server := NewWebServer(cfg.LoopIntervalSeconds)
	server.Start(8080)

	// 从配置文件读取交易设置
	tradingCoins := cfg.TradingSymbols
	btcEthLeverage := cfg.BTCETHLeverage
	altcoinLeverage := cfg.AltcoinLeverage

	callCount := 0
	runtimeStart := time.Now()
	var equityHistory []float64

	for {
		callCount++
		fmt.Printf("\n%s\n", strings.Repeat("=", 60))
		fmt.Printf("⏰ 周期 #%d | 时间: %s\n", callCount, time.Now().Format("15:04:05"))
		fmt.Printf("%s\n", strings.Repeat("=", 60))

		// 1. 获取行情
		fmt.Print("📡 正在获取真实市场行情...")
		if err := exchange.FetchMarketData(tradingCoins); err != nil {
			log.Printf("获取行情失败: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		fmt.Println("完成")

		// 2. 构建上下文
		accountInfo := exchange.GetAccountInfo()
		
		// 更新权益历史并计算夏普比率
		equityHistory = append(equityHistory, accountInfo.TotalEquity)
		sharpeRatio := CalculateRuntimeSharpe(equityHistory)

		// 转换持仓信息
		positions := exchange.GetPositions()
		marketData := exchange.GetMarketData()

		ctx := &Context{
			CurrentTime:     time.Now().Format("2006-01-02 15:04:05"),
			RuntimeMinutes:  int(time.Since(runtimeStart).Minutes()),
			CallCount:       callCount,
			Account:         accountInfo,
			Positions:       positions,
			MarketDataMap:   marketData,
			Sectors:         calculateSectorHeat(marketData), // 计算板块热度
			BTCETHLeverage:  btcEthLeverage,
			AltcoinLeverage: altcoinLeverage,
			SharpeRatio:     sharpeRatio,
		}

		// 打印账户状态
		fmt.Printf("💰 账户: 净值 $%.2f | 可用 $%.2f | 盈亏 %+.2f%% | 夏普: %.2f\n", 
			accountInfo.TotalEquity, accountInfo.AvailableBalance, accountInfo.TotalPnLPct, sharpeRatio)
		if len(positions) > 0 {
			fmt.Println("📊 当前持仓:")
			for _, p := range positions {
				fmt.Printf("   - %s %s: 盈亏 $%.2f (%.2f%%)\n", p.Symbol, p.Side, p.UnrealizedPnL, p.UnrealizedPnLPct)
			}
		}

		// 3. AI 思考与决策
		fmt.Println("🧠 AI 正在思考中...")
		decision, err := brain.GetDecision(ctx)

		// 更新 Web 状态
		server.UpdateState(ctx, decision, marketData)

		if err != nil {
			log.Printf("AI 请求失败: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// 打印思维链
		fmt.Printf("\n%s\n", strings.Repeat("-", 60))
		fmt.Println("💭 [AI 思维链]:")
		fmt.Println(wrapText(decision.CoTTrace, 80))
		fmt.Printf("%s\n", strings.Repeat("-", 60))

		// 4. 验证与执行
		if len(decision.Decisions) == 0 {
			fmt.Println("😴 AI 决定观望 (Wait)")
		} else {
			fmt.Println("📋 [AI 决策列表]:")
			
			// 验证所有决策
			if err := ValidateDecisions(decision.Decisions, accountInfo.TotalEquity, btcEthLeverage, altcoinLeverage); err != nil {
				fmt.Printf("❌ 风控拒绝: %v\n", err)
			} else {
				// 执行决策（使用索引，方便在 FullDecision 中记录执行结果，供前端展示）
				for i := range decision.Decisions {
					d := &decision.Decisions[i]
					fmt.Printf("   👉 %s %s", d.Symbol, d.Action)
					if d.Action == "open_long" || d.Action == "open_short" {
						fmt.Printf(" | size: $%.0f | lev: %dx", d.PositionSizeUSD, d.Leverage)
					}
					
					if err := exchange.ExecuteDecision(*d); err != nil {
						fmt.Printf(" -> ❌ 失败: %v\n", err)
						d.ExecStatus = "failed"
						d.ExecError = err.Error()
					} else {
						fmt.Printf(" -> ✅ 成功\n")
						d.ExecStatus = "success"
						d.ExecError = ""
					}
				}
			}
		}

		// 再次更新 Web 状态，将实际执行结果也推送到前端
		// 获取历史记录（如果有）
		history := exchange.GetTradeHistory()
		server.UpdateState(ctx, decision, marketData)
		if history != nil {
			server.UpdateTradeHistory(history)
		}

		// 根据当前配置的循环周期休眠（前端可动态修改）
		intervalSec := server.GetLoopIntervalSeconds()
		if intervalSec <= 0 {
			intervalSec = cfg.LoopIntervalSeconds
		}
		fmt.Printf("\n⏳ 等待 %d 秒（%.2f 分钟）进入下一周期...\n", intervalSec, float64(intervalSec)/60.0)
		time.Sleep(time.Duration(intervalSec) * time.Second)
	}
}

// CalculateRuntimeSharpe 计算运行时夏普比率 (简化版)
func CalculateRuntimeSharpe(equityCurve []float64) float64 {
	if len(equityCurve) < 3 {
		return 0.0
	}

	// 计算收益率序列
	var returns []float64
	for i := 1; i < len(equityCurve); i++ {
		prev := equityCurve[i-1]
		curr := equityCurve[i]
		if prev > 0 {
			ret := (curr - prev) / prev
			returns = append(returns, ret)
		}
	}

	if len(returns) == 0 {
		return 0.0
	}

	// 计算平均收益率
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// 计算标准差
	var varianceSum float64
	for _, r := range returns {
		varianceSum += math.Pow(r-mean, 2)
	}
	stdDev := math.Sqrt(varianceSum / float64(len(returns)))

	if stdDev == 0 {
		if mean > 0 {
			return 10.0 // 只有正收益，波动为0 -> 完美
		}
		return 0.0
	}

	// 假设无风险利率为 0
	// 放大系数：通常夏普是年化的，这里是周期的，为了让数字好看点（接近常见范围），乘以 sqrt(周期数) 的某种因子
	// 这里简单返回 Mean / StdDev，AI 能理解相对大小即可
	return mean / stdDev
}

// wrapText wraps the text to the specified width.
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	var sb strings.Builder
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if i > 0 {
			sb.WriteString("\n")
		}

		if len(line) <= width {
			sb.WriteString(line)
			continue
		}

		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}

		currentLineLen := 0
		for _, word := range words {
			wordLen := len(word)
			if currentLineLen+wordLen+1 > width && currentLineLen > 0 {
				sb.WriteString("\n")
				currentLineLen = 0
			} else if currentLineLen > 0 {
				sb.WriteString(" ")
				currentLineLen++
			}
			sb.WriteString(word)
			currentLineLen += wordLen
		}
	}
	return sb.String()
}

// calculateSectorHeat 计算板块热度
func calculateSectorHeat(dataMap map[string]*MarketData) []SectorInfo {
	// 定义板块 (你可以根据需要扩展)
	sectors := []SectorInfo{
		{Name: "Major", Symbols: []string{"BTCUSDT", "ETHUSDT", "BNBUSDT", "SOLUSDT"}},
		{Name: "Meme", Symbols: []string{"DOGEUSDT", "SHIBUSDT", "PEPEUSDT", "BONKUSDT", "WIFUSDT"}},
		{Name: "AI", Symbols: []string{"FETUSDT", "RNDRUSDT", "WLDUSDT", "ARKMUSDT"}},
		{Name: "L2", Symbols: []string{"ARBUSDT", "OPUSDT", "MATICUSDT"}},
	}

	var results []SectorInfo

	for _, sector := range sectors {
		var totalChange1h, totalChange4h float64
		var count int
		var maxChange float64 = -9999
		var leadingSymbol string

		for _, sym := range sector.Symbols {
			if data, ok := dataMap[sym]; ok {
				totalChange1h += data.PriceChange1h
				totalChange4h += data.PriceChange4h
				count++
				
				if data.PriceChange1h > maxChange {
					maxChange = data.PriceChange1h
					leadingSymbol = sym
				}
			}
		}

		if count > 0 {
			sector.AvgChange1h = totalChange1h / float64(count)
			sector.AvgChange4h = totalChange4h / float64(count)
			sector.LeadingSymbol = leadingSymbol
			results = append(results, sector)
		}
	}
	return results
}
