package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// 预编译正则表达式
var (
	reJSONFence      = regexp.MustCompile(`(?is)` + "```json\\s*(\\[\\s*\\{.*?\\}\\s*\\])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?is)\[\s*\{.*?\}\s*\]`)
	reArrayHead      = regexp.MustCompile(`^\[\s*\{`)
	reArrayOpenSpace = regexp.MustCompile(`^\[\s+\{`)
	reInvisibleRunes = regexp.MustCompile("[\u200B\u200C\u200D\uFEFF]")

	// XML标签提取
	reReasoningTag = regexp.MustCompile(`(?s)<reasoning>(.*?)</reasoning>`)
	reDecisionTag  = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
)

// AIBrain AI大脑
type AIBrain struct {
	APIKey  string
	APIURL  string
	Model   string
	Client  *http.Client
}

func NewAIBrain(apiKey, apiURL, model, proxyURL string) *AIBrain {
	// 配置 Transport
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, // 默认使用环境变量
	}

	// 如果指定了 Proxy，则使用指定的
	if proxyURL != "" {
		if pURL, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(pURL)
		} else {
			log.Printf("Warning: Invalid Proxy URL %s: %v", proxyURL, err)
		}
	}

	return &AIBrain{
		APIKey: apiKey,
		APIURL: apiURL,
		Model:  model,
		Client: &http.Client{
			Timeout:   120 * time.Second, // 增加超时时间到 120s
			Transport: transport,
		},
	}
}

// GetDecision 获取决策
func (b *AIBrain) GetDecision(ctx *Context) (*FullDecision, error) {
	// 1. 构建 Prompts
	systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	userPrompt := buildUserPrompt(ctx)

	// 2. 调用 AI
	response, err := b.callAI(systemPrompt, userPrompt)
	if err != nil {
		return nil, err
	}

	// 3. 解析响应
	fullDecision, err := parseAIResponse(response)
	if err != nil {
		return nil, err
	}

	fullDecision.SystemPrompt = systemPrompt
	fullDecision.UserPrompt = userPrompt
	fullDecision.Timestamp = time.Now()

	return fullDecision, nil
}

func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	// Read the prompt template file
	templateContent, err := os.ReadFile("extracted_prompts.md")
	if err != nil {
		log.Printf("Warning: Could not read extracted_prompts.md: %v. Using default short prompt.", err)
		sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\\n\\n")
	} else {
		sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\\n\\n")
		sb.WriteString(string(templateContent))
		sb.WriteString("\\n\\n")
	}
	// 硬约束（风险控制）
	sb.WriteString("# 硬约束（风险控制）\\n")
	sb.WriteString("1. 风险回报比: **默认必须 ≥ 1.5:1**（冒 1 单位风险，争取 1.5+ 单位收益）。在强趋势或小仓位时，可接受 **1.2:1** 左右的结构。\\n")
	sb.WriteString("2. 单笔风险上限: **不超过账户净值约 3%**（用于高杠杆日内模式；风控会按止损与仓位自动缩小过大的仓位）\\n")
	sb.WriteString(fmt.Sprintf("3. 杠杆限制: 山寨币最大%dx | BTC/ETH 最大%dx（如需更高杠杆，请在小仓位上试探，而非重仓梭哈）\\n", altcoinLeverage, btcEthLeverage))
	sb.WriteString("4. 保证金使用率：避免单笔吃掉全部可用保证金，留出流动性应对波动\\n")
	sb.WriteString("5. 开仓金额: 建议 ≥ 12 USDT（交易所最小名义价值约 10 USDT + 安全边际）\\n")
	sb.WriteString("6. 所有开仓必须设置明确的止损价（stop_loss）和止盈价（take_profit），禁止无止损裸奔。\\n\\n")

	// 交易频率与信号质量
	sb.WriteString("# ⏱️ 交易频率认知\\n\\n")
	sb.WriteString("- 优秀交易员：每天2-4笔 ≈ 每小时0.1-0.2笔\\n")
	sb.WriteString("- 每小时>2笔 = 过度交易\\n")
	sb.WriteString("- 单笔持仓时间≥30-60分钟\\n")
	sb.WriteString("如果你发现自己每个周期都在交易 → 标准过低；若持仓<30分钟就平仓且非止损 → 过于急躁。\\\\n\\\\n")

	sb.WriteString("# 🎯 开仓标准（5m/15m 日内模式）\\\\n\\\\n")
	sb.WriteString("你的核心决策必须基于 **5分钟 (5m)**、**15分钟 (15m)**，并参考 **1小时 (1h) / 4小时 (4h)** 背景。\\\\n")
	sb.WriteString("- **15m (Intraday Trend)**: 判断当前日内趋势方向（多头 / 空头 / 震荡），只在趋势清晰时进场。\\\\n")
	sb.WriteString("- **5m (Entry Timing)**: 在 15m 方向确认的前提下，用 5m 结构寻找突破/回踩/假突破失败等入场机会。\\\\n")
	sb.WriteString("- **1h/4h (Context)**: 用于识别更大级别支撑/阻力和波动环境，但**不要为了所谓“大趋势”而死扛明显错误的日内方向**。\\\\n")
	sb.WriteString("- **3m (Micro-structure)**: 仅用于理解微观形态，避免因为 1–2 根 3m K 线的噪音而频繁开平仓。\\\\n")
	sb.WriteString("开仓要求：在 **5m/15m 级别同时出现趋势方向一致 + 合理的止损/止盈结构 + Volume/OI/情绪配合** 时才考虑。\\\\n\\\\n")

	// 夏普比率驱动的自适应（稳健模式）
	sb.WriteString("# 🧬 夏普比率自我进化（稳健模式）\\n\\n")
	sb.WriteString("- Sharpe < -2.0：市场波动可能不利，提高标准，寻找信心度 > 75 的机会。\\n")
	sb.WriteString("- -2.0 ~ 0：**保持正常交易频率**，初期负夏普属正常现象。寻找信心度 > 60 的机会即可开仓，允许适度试错。\\n")
	sb.WriteString("- > 0：状态良好，继续保持或适当增加仓位。\\n\\n")

	// 额外提示：拥挤度与RSI
	sb.WriteString("# 💡 关键提示\\n\\n")
	sb.WriteString("- **拥挤度 (Crowded)**: \"Bullish_Crowded\" 并不意味着必须做空或观望。在强趋势中，拥挤是常态。如果价格行为（Price Action）配合，不要害怕顺势交易。\\n")
	sb.WriteString("- **RSI 超买/超卖**: 在强劲趋势中，RSI > 70 或 < 30 可能会持续很久（钝化）。不要单纯因为 RSI 超买就看空，除非有明确的反转K线结构。\\n\\n")

	// 决策流程提示
	sb.WriteString("# 💡 关键提示\\n\\n")
	sb.WriteString("- **拥挤度 (Crowded)**: \"Bullish_Crowded\" 并不意味着必须做空或观望。在强趋势中，拥挤是常态。如果价格行为（Price Action）配合，不要害怕顺势交易。\\n")
	sb.WriteString("- **RSI 超买/超卖**: 在强劲趋势中，RSI > 70 或 < 30 可能会持续很久（钝化）。不要单纯因为 RSI 超买就看空，除非有明确的反转K线结构。\\n\\n")

	// 决策流程提示
	sb.WriteString("# 📋 决策流程\\n\\n")
	sb.WriteString("1. 回顾夏普比率/盈亏 → 是否需要降频或暂停\\n")
	sb.WriteString("2. 检查持仓 → 是否该止盈/止损/调整\\n")
	sb.WriteString("3. 扫描候选币 + 多时间框 → 是否存在强信号\\n")
	sb.WriteString("4. 先写思维链，再输出结构化JSON\\n\\n")

	sb.WriteString("# 输出格式 (严格遵守)\\n")
	sb.WriteString("**必须使用XML标签 <reasoning> 和 <decision> 标签分隔思维链和决策JSON**\\n\\n")
	sb.WriteString("在 <decision> 中输出严格的 JSON 数组，每个元素代表一个交易决策。字段名必须与下面示例完全一致：symbol, action, leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, invalidation_condition, reasoning。\\n\\n")
	sb.WriteString("特别说明：\\n")
	sb.WriteString("- 当 action = 'update_stop_loss' 时，请只填写 `symbol`, `action`, `new_stop_loss`, `confidence`, `reasoning`，不要再使用 `stop_loss` 字段。\\n")
	sb.WriteString("- 当 action = 'update_take_profit' 时，请只填写 `symbol`, `action`, `new_take_profit`, `confidence`, `reasoning`。\\n")
	sb.WriteString("- 当 action = 'hold' 或 'wait' 时，不要填写价格/仓位字段（如 stop_loss/position_size_usd），只需给出 `symbol`(如适用)、`action`、`confidence`, `reasoning`。\\n\\n")
	sb.WriteString("<reasoning>\\n你的分析过程...\\n</reasoning>\\n\\n")
	sb.WriteString("<decision>\\n```json\\n[\\n")
	sb.WriteString("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_long\", \"leverage\": 5, \"position_size_usd\": 1000, \"stop_loss\": 90000, \"take_profit\": 95000, \"confidence\": 85, \"risk_usd\": 50, \"invalidation_condition\": \"RSI drops below 30\", \"reasoning\": \"...\"}\\n")
	sb.WriteString("]\\n```\\n</decision>\\n")

	return sb.String()
}

func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("时间: %s | 运行: %d分钟 | 周期: #%d\n\n", ctx.CurrentTime, ctx.RuntimeMinutes, ctx.CallCount))
	
	// BTC 市场风向标 (类似 nofx)
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		lsStr := ""
		if btcData.LongShortRatio != nil {
			lsStr = fmt.Sprintf(" | LS Ratio: %.2f", btcData.LongShortRatio.Ratio)
		}
		sb.WriteString(fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f%s\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7, lsStr))
	}

	// 账户信息
	sb.WriteString(fmt.Sprintf("账户: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))
	
	// 夏普比率
	sb.WriteString(fmt.Sprintf("📊 运行时夏普比率: %.2f\n\n", ctx.SharpeRatio))

	// 板块热度 (新增)
	if len(ctx.Sectors) > 0 {
		sb.WriteString("## Sector Heatmap (1h/4h Change)\n")
		for _, sec := range ctx.Sectors {
			sb.WriteString(fmt.Sprintf("- %s: 1h %+.2f%% | 4h %+.2f%% | Lead: %s\n",
				sec.Name, sec.AvgChange1h, sec.AvgChange4h, sec.LeadingSymbol))
		}
		sb.WriteString("\n")
	}

	// 持仓信息
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60)
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓%d分钟", durationMin)
				} else {
					holdingDuration = fmt.Sprintf(" | 持仓%d小时%d分钟", durationMin/60, durationMin%60)
				}
			}

			// 计算仓位价值
			positionValue := math.Abs(pos.Quantity) * pos.MarkPrice

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场%.4f 当前%.4f | 数量%.4f | 价值%.0f U | 盈亏%+.2f U (%+.2f%%) | 最高%.2f%% | 杠杆%dx | 强平%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side), 
				pos.EntryPrice, pos.MarkPrice, pos.Quantity, positionValue,
				pos.UnrealizedPnL, pos.UnrealizedPnLPct, pos.PeakPnLPct, 
				pos.Leverage, pos.LiquidationPrice, holdingDuration))
			
			// 附带该持仓币种的最新市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(formatMarketData(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("当前持仓: 无\n\n")
	}

	// 候选币种 (排除已持仓的)
	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(ctx.MarketDataMap)-len(ctx.Positions)))
	displayedCount := 0
	
	// 先建立持仓索引
	holdingMap := make(map[string]bool)
	for _, p := range ctx.Positions {
		holdingMap[p.Symbol] = true
	}

	for symbol, data := range ctx.MarketDataMap {
		if holdingMap[symbol] {
			continue // 已在持仓部分展示过
		}
		displayedCount++
		
		// 模拟 nofx 的 Source 标签展示
		sourceTag := ""
		if data.Source != "" {
			sourceTag = fmt.Sprintf(" (%s)", data.Source)
		}

		sb.WriteString(fmt.Sprintf("### %d. %s%s\n", displayedCount, symbol, sourceTag))
		sb.WriteString(formatMarketData(data))
		sb.WriteString("\n")
	}

	sb.WriteString("---\n请分析并输出决策。\n")
	return sb.String()
}

// formatMarketData 格式化输出市场数据 (仿照 nofx)
func formatMarketData(data *MarketData) string {
	var sb strings.Builder

	// 使用动态精度格式化价格
	priceStr := formatPriceWithDynamicPrecision(data.CurrentPrice)
	sb.WriteString(fmt.Sprintf("current_price = %s, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n",
		priceStr, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

	sb.WriteString(fmt.Sprintf("Bollinger Bands (20, 2.0): Upper=%s, Mid=%s, Lower=%s\n\n",
		formatPriceWithDynamicPrecision(data.BollingerUpper),
		formatPriceWithDynamicPrecision(data.BollingerMiddle),
		formatPriceWithDynamicPrecision(data.BollingerLower)))

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest, long/short ratio and funding rate for perps:\n\n",
		data.Symbol))

	if data.LongShortRatio != nil {
		sb.WriteString(fmt.Sprintf("Top Trader LS Ratio: %.2f (Longs: %.1f%%, Shorts: %.1f%%)\\n",
			data.LongShortRatio.Ratio, data.LongShortRatio.LongPct*100, data.LongShortRatio.ShortPct*100))
	}

	if data.Liquidation != nil {
		sb.WriteString(fmt.Sprintf("Estimated Liquidation (1h): $%.0f (Side Ratio: %.1f >1 means Longs Rekt)\\n",
			data.Liquidation.Amount1h, data.Liquidation.SideRatio))
	}

	if data.OpenInterest != nil {
		oiLatestStr := formatPriceWithDynamicPrecision(data.OpenInterest.Latest)
		oiAverageStr := formatPriceWithDynamicPrecision(data.OpenInterest.Average)
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %s Average: %s (1h Chg: %+.2f%%, 4h Chg: %+.2f%%)\\n\\n",
			oiLatestStr, oiAverageStr, data.OpenInterest.Change1h, data.OpenInterest.Change4h))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\\n\\n", data.FundingRate))

	// 成交量与情绪分析
	if data.VolumeAnalysis != nil {
		va := data.VolumeAnalysis
		sb.WriteString("Volume & Flow: ")
		if va.RelativeVolume3m > 0 {
			sb.WriteString(fmt.Sprintf("3m Relative Volume = %.2fx avg", va.RelativeVolume3m))
		}
		if va.IsVolumeSpike {
			sb.WriteString(" (VOLUME SPIKE)")
		}
		if va.TakerBuySellRatio != 0 {
			sb.WriteString(fmt.Sprintf(", taker buy/sell ratio = %.2f (>1 = aggressive buying)", va.TakerBuySellRatio))
		}
		sb.WriteString("\\n\\n")
	}

	if data.Sentiment != nil {
		st := data.Sentiment
		if st.FearGreedLabel != "" {
			sb.WriteString(fmt.Sprintf("Local Fear/Greed: %s (%d/100)\\n", st.FearGreedLabel, st.FearGreedIndex))
		}
		if st.LocalSentiment != "" {
			sb.WriteString(fmt.Sprintf("Local Sentiment Tag: %s\\n", st.LocalSentiment))
		}
		if st.Volatility1h > 0 {
			sb.WriteString(fmt.Sprintf("1h Realized Vol (approx): %.4f\\n", st.Volatility1h))
		}
		sb.WriteString("\\n")
	}

	if data.IntradaySeries != nil {
		// 将 3m 序列折叠/弱化描述
		sb.WriteString("Micro‑structure (3m) for timing only (ignore noise):\\n")
		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}
		sb.WriteString("\\n")
	}

	// 30m 主力指标（高亮展示）
	if data.EMA20_30m != 0 || data.MACD_30m != 0 || data.RSI14_30m != 0 || data.ATR14_30m != 0 {
		sb.WriteString("⭐️ **Intraday Wave Context (30‑minute timeframe)**:\\n\\n")
		sb.WriteString(fmt.Sprintf("EMA20 (30m): %.3f | MACD (30m): %.3f | RSI14 (30m): %.3f | ATR14 (30m): %.3f\\n\\n",
			data.EMA20_30m, data.MACD_30m, data.RSI14_30m, data.ATR14_30m))
	}

	// 1h 中周期指标
	if data.EMA20_1h != 0 || data.MACD_1h != 0 || data.RSI14_1h != 0 || data.ATR14_1h != 0 {
		sb.WriteString("Mid‑term context (1‑hour timeframe):\\n\\n")
		sb.WriteString(fmt.Sprintf("EMA20 (1h): %.3f | MACD (1h): %.3f | RSI14 (1h): %.3f | ATR14 (1h): %.3f\\n\\n",
			data.EMA20_1h, data.MACD_1h, data.RSI14_1h, data.ATR14_1h))
	}

	if data.LongerTermContext != nil {
		sb.WriteString("Longer‑term context (4‑hour timeframe):\n\n")

		sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))

		sb.WriteString(fmt.Sprintf("3‑Period ATR: %.3f vs. 14‑Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))

		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))

		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		}

		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		}
	}

	return sb.String()
}

// formatPriceWithDynamicPrecision 根据价格动态调整精度
func formatPriceWithDynamicPrecision(price float64) string {
	switch {
	case price < 0.0001:
		return fmt.Sprintf("%.8f", price)
	case price < 0.001:
		return fmt.Sprintf("%.6f", price)
	case price < 0.01:
		return fmt.Sprintf("%.6f", price)
	case price < 1.0:
		return fmt.Sprintf("%.4f", price)
	case price < 100:
		return fmt.Sprintf("%.4f", price)
	default:
		return fmt.Sprintf("%.2f", price)
	}
}

// formatFloatSlice 格式化float切片
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = formatPriceWithDynamicPrecision(v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

func (b *AIBrain) callAI(systemPrompt, userPrompt string) (string, error) {
	requestBody, _ := json.Marshal(map[string]interface{}{
		"model": b.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.1,
	})

	req, _ := http.NewRequest("POST", b.APIURL, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.APIKey)

	resp, err := b.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API Error (Status %d): %s", resp.StatusCode, string(body))
	}

	// 首先按 OpenAI/DeepSeek 兼容结构解析
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("AI JSON 解析失败: %v, body=%s", err, string(body))
		return "", fmt.Errorf("AI response parse error")
	}
	
	if len(result.Choices) == 0 {
		// 打印原始响应，帮助诊断是配额/鉴权还是其他错误
		log.Printf("AI 返回了空 choices，原始响应: %s", string(body))
		return "", fmt.Errorf("No response from AI: empty choices")
	}

	return result.Choices[0].Message.Content, nil
}

func parseAIResponse(response string) (*FullDecision, error) {
	// 1. 提取 Reasoning
	reasoning := extractTagContent(response, "reasoning")
	if reasoning == "" {
		// Fallback: if no tags, try to extract before JSON or decision tag
		if decisionIdx := strings.Index(response, "<decision>"); decisionIdx > 0 {
			reasoning = response[:decisionIdx]
		} else if idx := strings.Index(response, "```json"); idx > 0 {
			reasoning = response[:idx]
		} else {
			reasoning = response // worst case
		}
	}
	reasoning = strings.TrimSpace(reasoning)

	// 2. 提取 Decision JSON
	// 预清洗：去零宽/BOM
	s := removeInvisibleRunes(response)
	s = strings.TrimSpace(s)
	// 修复全角字符
	s = fixMissingQuotes(s)

	var jsonPart string
	if match := reDecisionTag.FindStringSubmatch(s); match != nil && len(match) > 1 {
		jsonPart = strings.TrimSpace(match[1])
	} else {
		jsonPart = s
	}

	// 修复 jsonPart 中的全角字符 (二次确保)
	jsonPart = fixMissingQuotes(jsonPart)

	var jsonContent string
	if m := reJSONFence.FindStringSubmatch(jsonPart); m != nil && len(m) > 1 {
		jsonContent = strings.TrimSpace(m[1])
	} else {
		// Fallback: 查找 JSON 数组
		jsonContent = strings.TrimSpace(reJSONArray.FindString(jsonPart))
	}

	var decisions []Decision
	if jsonContent != "" {
		// 规整格式
		jsonContent = compactArrayOpen(jsonContent)
		jsonContent = fixMissingQuotes(jsonContent)

		if err := validateJSONFormat(jsonContent); err != nil {
			log.Printf("JSON格式验证失败: %v, Content: %s", err, jsonContent)
			// Fallback to empty decisions instead of crashing
		} else {
			if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
				log.Printf("JSON解析失败: %v, Content: %s", err, jsonContent)
			}
		}
	}

	// 安全回退：如果解析失败或为空，生成保底决策
	if len(decisions) == 0 {
		if reasoning == "" {
			reasoning = "Failed to parse AI response."
		}
		// 我们返回空决策列表，由上层处理（Wait）
	}

	return &FullDecision{
		CoTTrace:  reasoning,
		Decisions: decisions,
	}, nil
}

func extractTagContent(text, tag string) string {
	re := regexp.MustCompile(fmt.Sprintf("(?s)<%s>(.*?)</%s>", tag, tag))
	match := re.FindStringSubmatch(text)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// removeInvisibleRunes 去除零宽字符和 BOM
func removeInvisibleRunes(s string) string {
	return reInvisibleRunes.ReplaceAllString(s, "")
}

// compactArrayOpen 规整开头的 "[ {" -> "[{"
func compactArrayOpen(s string) string {
	return reArrayOpenSpace.ReplaceAllString(strings.TrimSpace(s), "[{")
}

// fixMissingQuotes 替换中文引号和全角字符
func fixMissingQuotes(jsonStr string) string {
	// 替换中文引号
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")  // '
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")  // '

	// 替换全角符号
	jsonStr = strings.ReplaceAll(jsonStr, "［", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "］", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "｛", "{")
	jsonStr = strings.ReplaceAll(jsonStr, "｝", "}")
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":")
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",")
	jsonStr = strings.ReplaceAll(jsonStr, "【", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "】", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "、", ",")
	jsonStr = strings.ReplaceAll(jsonStr, "　", " ")

	return jsonStr
}

// validateJSONFormat 验证 JSON 格式
func validateJSONFormat(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)
	if !reArrayHead.MatchString(trimmed) {
		if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed[:min(20, len(trimmed))], "{") {
			return fmt.Errorf("invalid decision array (must contain objects)")
		}
		return fmt.Errorf("JSON must start with [{")
	}
	// 不再强行禁止使用 "~"，避免误杀正常字符串；具体内容交由 JSON 解析和业务校验处理
	return nil
}

// Helper
func min(a, b int) int {
	if a < b { return a }
	return b
}
