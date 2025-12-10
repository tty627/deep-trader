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
	systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity)
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

func buildSystemPrompt(accountEquity float64) string {
	var sb strings.Builder

	// 获取当前策略配置
	var riskCfg RiskConfig
	var strategyName, strategyDesc string
	sm := GetStrategyManager()
	if sm != nil {
		riskCfg = sm.GetRiskConfig()
		if strategy := sm.GetActiveStrategy(); strategy != nil {
			strategyName = strategy.Name
			strategyDesc = strategy.Description
		}
	} else {
		// 回退默认配置
		riskCfg = RiskConfig{
			MaxRiskPerTrade:    0.25,
			MaxTotalRisk:       0.40,
			MinRiskRewardRatio: 2.0,
			FixedLeverage:      15,
			MaxMarginUsage:     0.70,
		}
		strategyName = "balanced"
		strategyDesc = "中等风险，稳健交易"
	}

	// 1. 尝试加载策略专属 prompt 模板
	promptLoaded := false
	if sm != nil {
		if promptContent, err := sm.GetPromptContent(); err == nil && promptContent != "" {
			sb.WriteString(promptContent)
			sb.WriteString("\n")
			promptLoaded = true
		}
	}

	// 2. 如果没有策略专属 prompt，使用通用模板
	if !promptLoaded {
		templateContent, err := os.ReadFile("extracted_prompts.md")
		if err != nil {
			log.Printf("Warning: Could not read extracted_prompts.md: %v. Using fallback system prompt.", err)
			// 简化的回退提示
			sb.WriteString("你是专业的加密货币交易 AI，在币安 USDT 永续市场执行有纪律的风险控制。\n")
		} else {
			sb.WriteString(string(templateContent))
			sb.WriteString("\n")
		}
	}

	// 3. 追加当前策略信息和风控参数（动态生成）
	sb.WriteString("\n# 当前策略配置\n")
	sb.WriteString(fmt.Sprintf("策略名称: %s (%s)\n\n", strategyName, strategyDesc))
	
	sb.WriteString("## 风控参数（由后端强制执行）\n")
	sb.WriteString(fmt.Sprintf("- **固定杠杆**: %dx（你不能通过改变杠杆来控制风险，只能通过仓位大小和止损位置）\n", riskCfg.FixedLeverage))
	sb.WriteString(fmt.Sprintf("- **单笔最大风险**: %.0f%% 账户净值\n", riskCfg.MaxRiskPerTrade*100))
	sb.WriteString(fmt.Sprintf("- **总风险上限**: %.0f%% 账户净值（一轮内所有新开仓合计）\n", riskCfg.MaxTotalRisk*100))
	sb.WriteString(fmt.Sprintf("- **最小风险回报比**: %.1f:1\n", riskCfg.MinRiskRewardRatio))
	sb.WriteString(fmt.Sprintf("- **最大保证金使用率**: %.0f%%\n", riskCfg.MaxMarginUsage*100))
	sb.WriteString(fmt.Sprintf("- **止损 ATR 倍数参考**: %.1fx\n", riskCfg.StopLossATRMultiple))
	
	// 根据策略类型添加特定指导
	sb.WriteString("\n## 策略指导\n")
	switch strategyName {
	case "aggressive":
		sb.WriteString("- 当前为激进模式：追求高收益，接受较高风险\n")
		sb.WriteString("- 可以在趋势明确时重仓进攻，但必须严格执行止损\n")
		sb.WriteString("- 优先寻找突破和趋势延续机会\n")
	case "conservative":
		sb.WriteString("- 当前为保守模式：追求稳定收益，严格控制风险\n")
		sb.WriteString("- 只在高确定性机会入场，宁可错过也不要错进\n")
		sb.WriteString("- 优先寻找回调到支撑/阻力位的低风险入场点\n")
	case "scalping":
		sb.WriteString("- 当前为剥头皮模式：超短线快进快出\n")
		sb.WriteString("- 小仓位多次尝试，快速止盈止损\n")
		sb.WriteString("- 关注 5m/15m 级别的微观结构和成交量异动\n")
	default: // balanced
		sb.WriteString("- 当前为平衡模式：中等风险，稳健交易\n")
		sb.WriteString("- 在趋势明确且风险回报足够大时重仓试错，没有高质量机会就观望\n")
		sb.WriteString("- 主要使用 日线/4h 判断大级别趋势，4h/1h 寻找回调上车机会\n")
	}

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
