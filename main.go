package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

func main() {
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

	brain := NewAIBrain(cfg.AIAPIKey, cfg.AIAPIURL, cfg.AIModel)

	// 启动 Web 监控
	server := NewWebServer()
	server.Start(8080)

	// 交易币种
	tradingCoins := []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "DOGEUSDT"}

	btcEthLeverage := 10
	altcoinLeverage := 5

	callCount := 0
	runtimeStart := time.Now()

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
			BTCETHLeverage:  btcEthLeverage,
			AltcoinLeverage: altcoinLeverage,
		}

		// 打印账户状态
		fmt.Printf("💰 账户: 净值 $%.2f | 可用 $%.2f | 盈亏 %+.2f%%\n", 
			accountInfo.TotalEquity, accountInfo.AvailableBalance, accountInfo.TotalPnLPct)
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
                // 执行决策
                for _, d := range decision.Decisions {
                    fmt.Printf("   👉 %s %s", d.Symbol, d.Action)
                    if d.Action == "open_long" || d.Action == "open_short" {
                        fmt.Printf(" | size: $%.0f | lev: %dx", d.PositionSizeUSD, d.Leverage)
                    }
                    
                    if err := exchange.ExecuteDecision(d); err != nil {
                        fmt.Printf(" -> ❌ 失败: %v\n", err)
                    } else {
                        fmt.Printf(" -> ✅ 成功\n")
                    }
                }
            }
		}

		// 休眠
		fmt.Println("\n⏳ 等待 30 秒进入下一周期...")
		time.Sleep(30 * time.Second)
	}
}

// SimulatedExchange 模拟交易所，实现 Exchange 接口
type SimulatedExchange struct {
	account    AccountInfo
	positions  map[string]PositionInfo
	marketData map[string]*MarketData
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
		positions:  make(map[string]PositionInfo),
		marketData: make(map[string]*MarketData),
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
	}

	return nil
}

func wrapText(text string, width int) string {
	if len(text) < width {
		return text
	}
    // 简单换行处理
    return text 
}
