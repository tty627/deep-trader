package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"
)

// BacktestConfig 回测配置
type BacktestConfig struct {
	DataDir    string   `json:"data_dir"`
	Symbols    []string `json:"symbols"`
	StartDate  string   `json:"start_date"`  // YYYY-MM-DD
	EndDate    string   `json:"end_date"`    // YYYY-MM-DD
	InitialCap float64  `json:"initial_capital"`
	OutputDir  string   `json:"output_dir"`
}

// BacktestResult 回测结果
type BacktestResult struct {
	Config         BacktestConfig         `json:"config"`
	Summary        BacktestSummary        `json:"summary"`
	EquityCurve    []EquityPoint          `json:"equity_curve"`
	Trades         []TradeRecord          `json:"trades"`
	DailyReturns   []DailyReturn          `json:"daily_returns"`
	DrawdownCurve  []DrawdownPoint        `json:"drawdown_curve"`
	SymbolStats    map[string]SymbolStats `json:"symbol_stats"`
	GeneratedAt    time.Time              `json:"generated_at"`
}

// BacktestSummary 回测摘要
type BacktestSummary struct {
	InitialCapital   float64 `json:"initial_capital"`
	FinalEquity      float64 `json:"final_equity"`
	TotalReturn      float64 `json:"total_return"`      // 百分比
	TotalReturnUSD   float64 `json:"total_return_usd"`
	MaxDrawdown      float64 `json:"max_drawdown"`      // 百分比
	MaxDrawdownUSD   float64 `json:"max_drawdown_usd"`
	SharpeRatio      float64 `json:"sharpe_ratio"`
	SortinoRatio     float64 `json:"sortino_ratio"`
	WinRate          float64 `json:"win_rate"`          // 百分比
	ProfitFactor     float64 `json:"profit_factor"`
	TotalTrades      int     `json:"total_trades"`
	WinningTrades    int     `json:"winning_trades"`
	LosingTrades     int     `json:"losing_trades"`
	AvgWin           float64 `json:"avg_win"`
	AvgLoss          float64 `json:"avg_loss"`
	LargestWin       float64 `json:"largest_win"`
	LargestLoss      float64 `json:"largest_loss"`
	AvgHoldingPeriod string  `json:"avg_holding_period"`
	TradingDays      int     `json:"trading_days"`
	StartDate        string  `json:"start_date"`
	EndDate          string  `json:"end_date"`
}

// EquityPoint 净值点
type EquityPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Equity    float64   `json:"equity"`
	PnL       float64   `json:"pnl"`
	PnLPct    float64   `json:"pnl_pct"`
}

// DrawdownPoint 回撤点
type DrawdownPoint struct {
	Timestamp   time.Time `json:"timestamp"`
	Drawdown    float64   `json:"drawdown"`     // 百分比
	DrawdownUSD float64   `json:"drawdown_usd"`
	PeakEquity  float64   `json:"peak_equity"`
}

// DailyReturn 每日收益
type DailyReturn struct {
	Date      string  `json:"date"`
	Return    float64 `json:"return"`     // 百分比
	ReturnUSD float64 `json:"return_usd"`
	Equity    float64 `json:"equity"`
}

// SymbolStats 按币种统计
type SymbolStats struct {
	Symbol       string  `json:"symbol"`
	TotalTrades  int     `json:"total_trades"`
	WinRate      float64 `json:"win_rate"`
	TotalPnL     float64 `json:"total_pnl"`
	AvgPnL       float64 `json:"avg_pnl"`
	LargestWin   float64 `json:"largest_win"`
	LargestLoss  float64 `json:"largest_loss"`
}

// BacktestRunner 回测运行器
type BacktestRunner struct {
	config   BacktestConfig
	exchange *BacktestExchange
	brain    *AIBrain
	result   *BacktestResult
}

// NewBacktestRunner 创建回测运行器
func NewBacktestRunner(config BacktestConfig, aiConfig *Config) (*BacktestRunner, error) {
	// 创建回测交易所
	exchange, err := NewBacktestExchangeFromCSV(config.InitialCap, config.DataDir, config.Symbols)
	if err != nil {
		return nil, fmt.Errorf("create backtest exchange: %w", err)
	}

	// 创建AI大脑
	brain := NewAIBrain(aiConfig.AIAPIKey, aiConfig.AIAPIURL, aiConfig.AIModel, aiConfig.BinanceProxyURL)

	return &BacktestRunner{
		config:   config,
		exchange: exchange,
		brain:    brain,
		result: &BacktestResult{
			Config:      config,
			EquityCurve: make([]EquityPoint, 0),
			Trades:      make([]TradeRecord, 0),
			SymbolStats: make(map[string]SymbolStats),
		},
	}, nil
}

// Run 运行回测
func (br *BacktestRunner) Run() (*BacktestResult, error) {
	log.Println("🚀 开始回测...")
	log.Printf("   数据目录: %s", br.config.DataDir)
	log.Printf("   交易对: %v", br.config.Symbols)
	log.Printf("   初始资金: $%.2f", br.config.InitialCap)

	startTime := time.Now()
	callCount := 0
	var peakEquity float64

	for {
		// 获取行情
		if err := br.exchange.FetchMarketData(br.config.Symbols); err != nil {
			if err == ErrBacktestFinished {
				log.Println("✅ 回测数据已走完")
				break
			}
			return nil, fmt.Errorf("fetch market data: %w", err)
		}

		callCount++
		accountInfo := br.exchange.GetAccountInfo()
		
		// 更新峰值
		if accountInfo.TotalEquity > peakEquity {
			peakEquity = accountInfo.TotalEquity
		}

		// 记录净值点
		br.result.EquityCurve = append(br.result.EquityCurve, EquityPoint{
			Timestamp: time.Now(),
			Equity:    accountInfo.TotalEquity,
			PnL:       accountInfo.TotalPnL,
			PnLPct:    accountInfo.TotalPnLPct,
		})

		// 记录回撤
		drawdown := 0.0
		if peakEquity > 0 {
			drawdown = (peakEquity - accountInfo.TotalEquity) / peakEquity * 100
		}
		br.result.DrawdownCurve = append(br.result.DrawdownCurve, DrawdownPoint{
			Timestamp:   time.Now(),
			Drawdown:    drawdown,
			DrawdownUSD: peakEquity - accountInfo.TotalEquity,
			PeakEquity:  peakEquity,
		})

		// 构建上下文
		positions := br.exchange.GetPositions()
		marketData := br.exchange.GetMarketData()

		ctx := &Context{
			CurrentTime:   time.Now().Format("2006-01-02 15:04:05"),
			CallCount:     callCount,
			Account:       accountInfo,
			Positions:     positions,
			MarketDataMap: marketData,
		}

		// 获取AI决策
		decision, err := br.brain.GetDecision(ctx)
		if err != nil {
			log.Printf("⚠️ AI决策失败 (周期 #%d): %v", callCount, err)
			continue
		}

		if decision == nil || len(decision.Decisions) == 0 {
			continue
		}

		// 执行决策
		for _, d := range decision.Decisions {
			if d.Action == "wait" || d.Action == "hold" {
				continue
			}

			if err := br.exchange.ExecuteDecision(d); err != nil {
				log.Printf("⚠️ 执行失败 %s %s: %v", d.Symbol, d.Action, err)
			}
		}

		// 每100个周期输出进度
		if callCount%100 == 0 {
			log.Printf("📊 回测进度: 周期 #%d | 净值: $%.2f | 盈亏: %+.2f%%",
				callCount, accountInfo.TotalEquity, accountInfo.TotalPnLPct)
		}
	}

	// 收集交易记录
	br.result.Trades = br.exchange.GetTradeHistory()

	// 计算统计数据
	br.calculateSummary()
	br.calculateSymbolStats()
	br.result.GeneratedAt = time.Now()

	elapsed := time.Since(startTime)
	log.Printf("✅ 回测完成! 耗时: %v | 周期数: %d", elapsed, callCount)

	return br.result, nil
}

// calculateSummary 计算回测摘要
func (br *BacktestRunner) calculateSummary() {
	summary := &br.result.Summary
	trades := br.result.Trades
	equityCurve := br.result.EquityCurve

	summary.InitialCapital = br.config.InitialCap
	summary.TotalTrades = len(trades)

	if len(equityCurve) > 0 {
		summary.FinalEquity = equityCurve[len(equityCurve)-1].Equity
		summary.TotalReturnUSD = summary.FinalEquity - summary.InitialCapital
		if summary.InitialCapital > 0 {
			summary.TotalReturn = summary.TotalReturnUSD / summary.InitialCapital * 100
		}
	}

	// 计算最大回撤
	var maxDrawdown, maxDrawdownUSD float64
	for _, dd := range br.result.DrawdownCurve {
		if dd.Drawdown > maxDrawdown {
			maxDrawdown = dd.Drawdown
			maxDrawdownUSD = dd.DrawdownUSD
		}
	}
	summary.MaxDrawdown = maxDrawdown
	summary.MaxDrawdownUSD = maxDrawdownUSD

	// 计算交易统计
	var totalWin, totalLoss float64
	var winCount, lossCount int
	var largestWin, largestLoss float64

	for _, t := range trades {
		if t.PnL > 0 {
			winCount++
			totalWin += t.PnL
			if t.PnL > largestWin {
				largestWin = t.PnL
			}
		} else if t.PnL < 0 {
			lossCount++
			totalLoss += math.Abs(t.PnL)
			if t.PnL < largestLoss {
				largestLoss = t.PnL
			}
		}
	}

	summary.WinningTrades = winCount
	summary.LosingTrades = lossCount
	summary.LargestWin = largestWin
	summary.LargestLoss = largestLoss

	if summary.TotalTrades > 0 {
		summary.WinRate = float64(winCount) / float64(summary.TotalTrades) * 100
	}

	if winCount > 0 {
		summary.AvgWin = totalWin / float64(winCount)
	}
	if lossCount > 0 {
		summary.AvgLoss = totalLoss / float64(lossCount)
	}

	if totalLoss > 0 {
		summary.ProfitFactor = totalWin / totalLoss
	}

	// 计算夏普比率
	summary.SharpeRatio = br.calculateSharpeRatio()
	summary.SortinoRatio = br.calculateSortinoRatio()

	summary.TradingDays = len(br.result.DailyReturns)
}

// calculateSharpeRatio 计算夏普比率
func (br *BacktestRunner) calculateSharpeRatio() float64 {
	if len(br.result.EquityCurve) < 2 {
		return 0
	}

	var returns []float64
	for i := 1; i < len(br.result.EquityCurve); i++ {
		prev := br.result.EquityCurve[i-1].Equity
		curr := br.result.EquityCurve[i].Equity
		if prev > 0 {
			ret := (curr - prev) / prev
			returns = append(returns, ret)
		}
	}

	if len(returns) == 0 {
		return 0
	}

	// 计算平均收益
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
		return 0
	}

	// 年化 (假设每天一个数据点)
	return mean / stdDev * math.Sqrt(252)
}

// calculateSortinoRatio 计算 Sortino 比率
func (br *BacktestRunner) calculateSortinoRatio() float64 {
	if len(br.result.EquityCurve) < 2 {
		return 0
	}

	var returns []float64
	var negativeReturns []float64

	for i := 1; i < len(br.result.EquityCurve); i++ {
		prev := br.result.EquityCurve[i-1].Equity
		curr := br.result.EquityCurve[i].Equity
		if prev > 0 {
			ret := (curr - prev) / prev
			returns = append(returns, ret)
			if ret < 0 {
				negativeReturns = append(negativeReturns, ret)
			}
		}
	}

	if len(returns) == 0 || len(negativeReturns) == 0 {
		return 0
	}

	// 计算平均收益
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))

	// 计算下行标准差
	var varianceSum float64
	for _, r := range negativeReturns {
		varianceSum += math.Pow(r, 2)
	}
	downDev := math.Sqrt(varianceSum / float64(len(negativeReturns)))

	if downDev == 0 {
		return 0
	}

	return mean / downDev * math.Sqrt(252)
}

// calculateSymbolStats 计算按币种统计
func (br *BacktestRunner) calculateSymbolStats() {
	stats := make(map[string]*SymbolStats)

	for _, t := range br.result.Trades {
		if stats[t.Symbol] == nil {
			stats[t.Symbol] = &SymbolStats{Symbol: t.Symbol}
		}

		s := stats[t.Symbol]
		s.TotalTrades++
		s.TotalPnL += t.PnL

		if t.PnL > 0 {
			if t.PnL > s.LargestWin {
				s.LargestWin = t.PnL
			}
		} else if t.PnL < s.LargestLoss {
			s.LargestLoss = t.PnL
		}
	}

	// 计算统计数据
	for symbol, s := range stats {
		if s.TotalTrades > 0 {
			s.AvgPnL = s.TotalPnL / float64(s.TotalTrades)

			// 计算胜率
			winCount := 0
			for _, t := range br.result.Trades {
				if t.Symbol == symbol && t.PnL > 0 {
					winCount++
				}
			}
			s.WinRate = float64(winCount) / float64(s.TotalTrades) * 100
		}
		br.result.SymbolStats[symbol] = *s
	}
}

// SaveReport 保存回测报告
func (br *BacktestRunner) SaveReport() error {
	if br.config.OutputDir == "" {
		br.config.OutputDir = "backtest_reports"
	}

	// 创建输出目录
	if err := os.MkdirAll(br.config.OutputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")

	// 保存JSON报告
	jsonPath := filepath.Join(br.config.OutputDir, fmt.Sprintf("report_%s.json", timestamp))
	jsonData, err := json.MarshalIndent(br.result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("write json: %w", err)
	}

	// 保存HTML报告
	htmlPath := filepath.Join(br.config.OutputDir, fmt.Sprintf("report_%s.html", timestamp))
	htmlContent := br.generateHTMLReport()
	if err := os.WriteFile(htmlPath, []byte(htmlContent), 0644); err != nil {
		return fmt.Errorf("write html: %w", err)
	}

	log.Printf("📄 报告已保存:")
	log.Printf("   JSON: %s", jsonPath)
	log.Printf("   HTML: %s", htmlPath)

	return nil
}

// generateHTMLReport 生成HTML报告
func (br *BacktestRunner) generateHTMLReport() string {
	s := br.result.Summary

	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Deep Trader 回测报告</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 0; padding: 20px; background: #0f172a; color: #e2e8f0; }
        .container { max-width: 1200px; margin: 0 auto; }
        h1 { color: #f8fafc; border-bottom: 2px solid #6366f1; padding-bottom: 10px; }
        h2 { color: #a5b4fc; margin-top: 30px; }
        .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 15px; margin: 20px 0; }
        .card { background: #1e293b; padding: 15px; border-radius: 8px; border: 1px solid #334155; }
        .card-title { font-size: 12px; color: #94a3b8; text-transform: uppercase; }
        .card-value { font-size: 24px; font-weight: bold; margin-top: 5px; }
        .positive { color: #22c55e; }
        .negative { color: #ef4444; }
        table { width: 100%%; border-collapse: collapse; margin: 20px 0; }
        th, td { padding: 10px; text-align: left; border-bottom: 1px solid #334155; }
        th { background: #1e293b; color: #94a3b8; font-weight: 500; }
        tr:hover { background: #1e293b; }
        .footer { margin-top: 40px; text-align: center; color: #64748b; font-size: 12px; }
    </style>
</head>
<body>
    <div class="container">
        <h1>🤖 Deep Trader 回测报告</h1>
        <p>生成时间: %s</p>
        
        <h2>📊 回测摘要</h2>
        <div class="grid">
            <div class="card">
                <div class="card-title">初始资金</div>
                <div class="card-value">$%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">最终净值</div>
                <div class="card-value">$%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">总收益</div>
                <div class="card-value %s">%+.2f%% ($%+.2f)</div>
            </div>
            <div class="card">
                <div class="card-title">最大回撤</div>
                <div class="card-value negative">-%.2f%%</div>
            </div>
            <div class="card">
                <div class="card-title">夏普比率</div>
                <div class="card-value">%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">胜率</div>
                <div class="card-value">%.2f%%</div>
            </div>
            <div class="card">
                <div class="card-title">盈亏比</div>
                <div class="card-value">%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">总交易次数</div>
                <div class="card-value">%d</div>
            </div>
        </div>

        <h2>📈 交易统计</h2>
        <div class="grid">
            <div class="card">
                <div class="card-title">盈利交易</div>
                <div class="card-value positive">%d</div>
            </div>
            <div class="card">
                <div class="card-title">亏损交易</div>
                <div class="card-value negative">%d</div>
            </div>
            <div class="card">
                <div class="card-title">平均盈利</div>
                <div class="card-value positive">$%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">平均亏损</div>
                <div class="card-value negative">$%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">最大单笔盈利</div>
                <div class="card-value positive">$%.2f</div>
            </div>
            <div class="card">
                <div class="card-title">最大单笔亏损</div>
                <div class="card-value negative">$%.2f</div>
            </div>
        </div>

        <div class="footer">
            <p>Deep Trader - AI 加密货币交易系统</p>
        </div>
    </div>
</body>
</html>`,
		br.result.GeneratedAt.Format("2006-01-02 15:04:05"),
		s.InitialCapital,
		s.FinalEquity,
		getColorClass(s.TotalReturn),
		s.TotalReturn, s.TotalReturnUSD,
		s.MaxDrawdown,
		s.SharpeRatio,
		s.WinRate,
		s.ProfitFactor,
		s.TotalTrades,
		s.WinningTrades,
		s.LosingTrades,
		s.AvgWin,
		s.AvgLoss,
		s.LargestWin,
		s.LargestLoss,
	)
}

func getColorClass(value float64) string {
	if value >= 0 {
		return "positive"
	}
	return "negative"
}

// RunBacktestCLI 命令行回测入口
func RunBacktestCLI(dataDir string, symbols []string, initialCap float64, outputDir string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if len(symbols) == 0 {
		symbols = cfg.TradingSymbols
	}

	btConfig := BacktestConfig{
		DataDir:    dataDir,
		Symbols:    symbols,
		InitialCap: initialCap,
		OutputDir:  outputDir,
	}

	runner, err := NewBacktestRunner(btConfig, cfg)
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	_, err = runner.Run()
	if err != nil {
		return fmt.Errorf("run backtest: %w", err)
	}

	return runner.SaveReport()
}
