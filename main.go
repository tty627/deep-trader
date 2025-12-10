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
	// 运行期最高净值，用于计算回撤并触发 Drawdown Kill Switch
	var peakEquity float64
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

	// 初始化全局存储
	if err := InitGlobalStorage("data/storage.db"); err != nil {
		log.Printf("⚠️ 初始化存储失败: %v (部分功能可能不可用)", err)
	} else {
		log.Println("✅ 存储系统已初始化")
	}

	// 初始化全局策略管理器
	InitGlobalStrategyManager("strategies")
	log.Println("✅ 策略管理器已初始化")

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

		// 运行期高点与回撤跟踪（用于 Drawdown Kill Switch）
		if peakEquity <= 0 || accountInfo.TotalEquity > peakEquity {
			peakEquity = accountInfo.TotalEquity
		}
		drawdown := 0.0
		if peakEquity > 0 {
			drawdown = 1 - accountInfo.TotalEquity/peakEquity
		}
		
		// 更新权益历史并计算夏普比率
		equityHistory = append(equityHistory, accountInfo.TotalEquity)
		sharpeRatio := CalculateRuntimeSharpe(equityHistory)

		// 转换持仓信息
		positions := exchange.GetPositions()
		marketData := exchange.GetMarketData()

		// 在进入 AI 决策前执行一次硬止损检查：如果某些持仓浮亏已深于阈值，则直接由后端强制止损平仓。
		enforceHardStopLoss(positions, exchange)

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

		// 如果本轮 AI 请求失败或未返回有效决策，避免空指针崩溃，记录错误并跳过执行阶段。
		if err != nil || decision == nil {
			if err != nil {
				log.Printf("AI 请求失败: %v", err)
			} else {
				log.Printf("AI 请求失败: 决策结果为空 (nil FullDecision)")
			}
			// 仍然更新 Web 状态，便于前端看到最新上下文和行情（但本轮无决策）。
			server.UpdateState(ctx, nil, marketData)
			time.Sleep(5 * time.Second)
			continue
		}

		// 在风控验证和实盘执行之前，先对 AI 输出的 action 做一层宽松归一化：
		// - 将 close_position 根据当前持仓方向映射为 close_long/close_short；
		// - 将 open_position + side=long/short 映射为 open_long/open_short；
		// - 其余未知别名保持不变，由风控层再做兜底处理。
		normalizeDecisionActions(decision.Decisions, positions)

		// 若当前回撤过大，进入防御模式：不再允许新开仓，所有 open_long/open_short 自动视为 wait。
		defensiveMode := peakEquity > 0 && drawdown >= 0.25
		if defensiveMode {
			for i := range decision.Decisions {
				d := &decision.Decisions[i]
				if d.Action == "open_long" || d.Action == "open_short" {
					log.Printf("⚠️ [Drawdown Wait] 回撤已达 %.1f%%, 自动忽略新开仓 %s %s (size=%.2f)", drawdown*100, d.Symbol, d.Action, d.PositionSizeUSD)
					d.Action = "wait"
				}
			}
		}

		// 更新 Web 状态（带上本轮 AI 决策，便于前端展示）
		server.UpdateState(ctx, decision, marketData)

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
			
			// 验证所有决策（传入当前市场价格，用于风险评估和全局风险控制）
			if err := ValidateDecisions(decision.Decisions, accountInfo, marketData); err != nil {
				fmt.Printf("❌ 风控拒绝: %v\n", err)
			} else {
				// 执行决策（使用索引，方便在 FullDecision 中记录执行结果，供前端展示）
				for i := range decision.Decisions {
					d := &decision.Decisions[i]
					
					// 对于非交易类动作，直接标记并跳过执行，避免调用交易所API
					if d.Action == "wait" {
						fmt.Printf("   ⏸️  %s: 观望 (Wait)\n", d.Symbol)
						d.ExecStatus = "success"
						continue
					}
					if d.Action == "hold" {
						fmt.Printf("   ✊  %s: 持仓 (Hold)\n", d.Symbol)
						d.ExecStatus = "success"
						continue
					}

					fmt.Printf("   👉 %s %s", d.Symbol, d.Action)
					if d.Action == "open_long" || d.Action == "open_short" {
						fmt.Printf(" | size: $%.0f | lev: %dx", d.PositionSizeUSD, d.Leverage)
						// 简单打印预估风险/收益百分比，便于人工监督
						if md, ok := marketData[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 && d.StopLoss > 0 && d.TakeProfit > 0 {
							entry := md.CurrentPrice
							var riskPct, rewardPct float64
							if d.Action == "open_long" {
								riskPct = (entry - d.StopLoss) / entry * 100
								rewardPct = (d.TakeProfit - entry) / entry * 100
							} else {
								riskPct = (d.StopLoss - entry) / entry * 100
								rewardPct = (entry - d.TakeProfit) / entry * 100
							}
							if riskPct > 0 {
								fmt.Printf(" | RR≈%.2f:1 (risk≈%.2f%%, reward≈%.2f%%)", rewardPct/riskPct, riskPct, rewardPct)
							}
						}
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

		// 将完整的终端输出内容保存到详细日志文件
		if err := appendDetailedLog("detailed_log.txt", ctx, decision, marketData); err != nil {
			log.Printf("⚠️ 写入详细日志失败: %v", err)
		}

		// 保存数据到 Storage（如果已初始化）
		if storage := GetStorage(); storage != nil {
			// 保存净值快照
			if err := storage.SaveEquitySnapshot(accountInfo.TotalEquity, accountInfo.AvailableBalance, accountInfo.UnrealizedPnL); err != nil {
				log.Printf("⚠️ 保存净值快照失败: %v", err)
			}

			// 保存 AI 决策记录
			if decision != nil && len(decision.Decisions) > 0 {
				if err := storage.SaveAIDecision(decision); err != nil {
					log.Printf("⚠️ 保存 AI 决策记录失败: %v", err)
				}
			}

			// 保存交易记录（如果有新的平仓记录）
			if history != nil && len(history) > 0 {
				for _, record := range history {
					if err := storage.SaveTradeRecord(record); err != nil {
						log.Printf("⚠️ 保存交易记录失败: %v", err)
					}
				}
			}
		}

		// 如果是在真实币安模式下：当某个交易对已经没有持仓时，清理遗留的止损/止盈挂单
		if be, ok := exchange.(*BinanceExchange); ok {
			positionMap := make(map[string]bool)
			for _, p := range positions {
				positionMap[p.Symbol] = true
			}
			for _, sym := range tradingCoins {
				if !positionMap[sym] {
					if err := be.CancelStopLossOrders(sym); err != nil {
						log.Printf("⚠️ Cleanup StopLoss orders failed for %s: %v", sym, err)
					}
					if err := be.CancelTakeProfitOrders(sym); err != nil {
						log.Printf("⚠️ Cleanup TakeProfit orders failed for %s: %v", sym, err)
					}
				}
			}
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

// appendDetailedLog 将终端输出的完整内容保存到文本日志文件
func appendDetailedLog(path string, ctx *Context, full *FullDecision, marketData map[string]*MarketData) error {
	var sb strings.Builder
	
	// 周期头部
	sb.WriteString("\n" + strings.Repeat("=", 60) + "\n")
	sb.WriteString(fmt.Sprintf("⏰ 周期 #%d | 时间: %s\n", ctx.CallCount, ctx.CurrentTime))
	sb.WriteString(strings.Repeat("=", 60) + "\n")
	
	// 账户状态
	sb.WriteString(fmt.Sprintf("💰 账户: 净值 $%.2f | 可用 $%.2f | 盈亏 %+.2f%% | 夏普: %.2f\n",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.TotalPnLPct, ctx.SharpeRatio))
	
	// 当前持仓
	if len(ctx.Positions) > 0 {
		sb.WriteString("📊 当前持仓:\n")
		for _, p := range ctx.Positions {
			sb.WriteString(fmt.Sprintf("   - %s %s: 盈亏 $%.2f (%.2f%%)\n", p.Symbol, p.Side, p.UnrealizedPnL, p.UnrealizedPnLPct))
		}
	}
	
	if full != nil {
		// AI 思维链
		sb.WriteString("\n" + strings.Repeat("-", 60) + "\n")
		sb.WriteString("💭 [AI 思维链]:\n")
		sb.WriteString(full.CoTTrace + "\n")
		sb.WriteString(strings.Repeat("-", 60) + "\n")
		
		// AI 决策列表
		if len(full.Decisions) == 0 {
			sb.WriteString("😴 AI 决定观望 (Wait)\n")
		} else {
			sb.WriteString("📋 [AI 决策列表]:\n")
			for _, d := range full.Decisions {
				if d.Action == "wait" {
					sb.WriteString(fmt.Sprintf("   ⏸️  %s: 观望 (Wait)\n", d.Symbol))
					continue
				}
				if d.Action == "hold" {
					sb.WriteString(fmt.Sprintf("   ✊  %s: 持仓 (Hold)\n", d.Symbol))
					continue
				}
				
				sb.WriteString(fmt.Sprintf("   👉 %s %s", d.Symbol, d.Action))
				if d.Action == "open_long" || d.Action == "open_short" {
					sb.WriteString(fmt.Sprintf(" | size: $%.0f | lev: %dx", d.PositionSizeUSD, d.Leverage))
					// 计算风险回报比
					if md, ok := marketData[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 && d.StopLoss > 0 && d.TakeProfit > 0 {
						entry := md.CurrentPrice
						var riskPct, rewardPct float64
						if d.Action == "open_long" {
							riskPct = (entry - d.StopLoss) / entry * 100
							rewardPct = (d.TakeProfit - entry) / entry * 100
						} else {
							riskPct = (d.StopLoss - entry) / entry * 100
							rewardPct = (entry - d.TakeProfit) / entry * 100
						}
						if riskPct > 0 {
							sb.WriteString(fmt.Sprintf(" | RR≈%.2f:1 (risk≈%.2f%%, reward≈%.2f%%)", rewardPct/riskPct, riskPct, rewardPct))
						}
					}
				}
				
				// 执行结果
				if d.ExecStatus == "success" {
					sb.WriteString(" -> ✅ 成功\n")
				} else if d.ExecStatus == "failed" {
					sb.WriteString(fmt.Sprintf(" -> ❌ 失败: %s\n", d.ExecError))
				} else {
					sb.WriteString("\n")
				}
			}
		}
		
		// 添加 Prompts (可选，便于复现)
		sb.WriteString("\n" + strings.Repeat("-", 60) + "\n")
		sb.WriteString("📝 [System Prompt]:\n")
		sb.WriteString(full.SystemPrompt + "\n")
		sb.WriteString("\n" + strings.Repeat("-", 60) + "\n")
		sb.WriteString("📝 [User Prompt]:\n")
		sb.WriteString(full.UserPrompt + "\n")
	}
	
	sb.WriteString("\n")
	
	// 追加写入文件
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	
	if _, err := f.WriteString(sb.String()); err != nil {
		return err
	}
	return nil
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

// normalizeDecisionActions 对 AI 输出的决策做宽松兼容处理，避免因为少量 action 别名导致整批决策失效。
// 当前主要处理：
//   - action == "close_position" 时，根据当前持仓方向自动映射为 close_long / close_short；
//   - action == "open_position" 且提供了 side 字段（long/short/buy/sell）时，映射为 open_long / open_short；
//   - 若无法安全判断，则将该决策视为观望（wait），不影响同一批中的其它决策。
func normalizeDecisionActions(decisions []Decision, positions []PositionInfo) {
	if len(decisions) == 0 {
		return
	}

	// 建立 symbol -> side 的索引，便于后续快速查找当前持仓方向
	posSide := make(map[string]string)
	for _, p := range positions {
		// Binance 返回的 Side 已经是 "long" / "short"，统一转为小写
		if p.Symbol == "" {
			continue
		}
		posSide[p.Symbol] = strings.ToLower(p.Side)
	}

	for i := range decisions {
		d := &decisions[i]

		switch d.Action {
		case "close_position":
			if d.Symbol == "" {
				log.Printf("⚠️ [Action Reject] close_position 缺少 symbol，已忽略（视为 wait）")
				d.Action = "wait"
				continue
			}

			if side, ok := posSide[d.Symbol]; ok {
				switch side {
				case "long":
					log.Printf("⚠️ [Action Fallback] %s 使用 close_position，自动映射为 close_long", d.Symbol)
					d.Action = "close_long"
				case "short":
					log.Printf("⚠️ [Action Fallback] %s 使用 close_position，自动映射为 close_short", d.Symbol)
					d.Action = "close_short"
				default:
					log.Printf("⚠️ [Action Reject] %s close_position 但持仓方向未知(%s)，已忽略（视为 wait）", d.Symbol, side)
					d.Action = "wait"
				}
			} else {
				// 当前无持仓，close_position 没有意义
				log.Printf("⚠️ [Action Reject] %s close_position 但当前无持仓，已忽略（视为 wait）", d.Symbol)
				d.Action = "wait"
			}

		case "open_position":
			// 兼容 open_position + side 方案，仅在 side 明确时做映射
			side := strings.ToLower(d.Side)
			if side == "" {
				log.Printf("⚠️ [Action Reject] %s 使用 open_position 但未提供 side 字段，已忽略（视为 wait）", d.Symbol)
				d.Action = "wait"
				continue
			}

			switch side {
			case "long", "buy":
				log.Printf("⚠️ [Action Fallback] %s 使用 open_position+side=%s，自动映射为 open_long", d.Symbol, d.Side)
				d.Action = "open_long"
			case "short", "sell":
				log.Printf("⚠️ [Action Fallback] %s 使用 open_position+side=%s，自动映射为 open_short", d.Symbol, d.Side)
				d.Action = "open_short"
			default:
				log.Printf("⚠️ [Action Reject] %s 使用 open_position 但 side=%s 无法识别，已忽略（视为 wait）", d.Symbol, d.Side)
				d.Action = "wait"
			}
		}
	}
}

// calculateSectorHeat 计算板块热度
// enforceHardStopLoss 对当前持仓执行一次硬止损检查：当浮亏超过预设阈值时，直接由后端强制平仓，避免单笔仓位出现极深回撤。
func enforceHardStopLoss(positions []PositionInfo, exchange Exchange) {
	for _, p := range positions {
		// 只关注有亏损的仓位
		if p.UnrealizedPnLPct >= 0 {
			continue
		}

		// 区分主流币与 Altcoin
		isMajor := p.Symbol == "BTCUSDT" || p.Symbol == "ETHUSDT"
		threshold := -30.0 // 主流币默认 -30%
		if !isMajor {
			threshold = -25.0 // Altcoin 更保守 -25%
		}

		if p.UnrealizedPnLPct <= threshold {
			action := "close_long"
			if strings.ToLower(p.Side) == "short" {
				action = "close_short"
			}
			log.Printf("⚠️ [Hard SL] %s %s 浮亏 %.2f%% 低于阈值 %.2f%%, 触发硬止损强制平仓", p.Symbol, p.Side, p.UnrealizedPnLPct, threshold)

			if err := exchange.ExecuteDecision(Decision{
				Symbol:    p.Symbol,
				Action:    action,
				Reasoning: "Hard stop loss triggered by backend due to deep unrealized loss",
			}); err != nil {
				log.Printf("❌ [Hard SL Error] %s %s 平仓失败: %v", p.Symbol, action, err)
			}
		}
	}
}

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
