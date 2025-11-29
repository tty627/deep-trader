package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// AIBrain AI大脑
type AIBrain struct {
	APIKey  string
	APIURL  string
	Model   string
	Client  *http.Client
}

func NewAIBrain(apiKey, apiURL, model string) *AIBrain {
	return &AIBrain{
		APIKey: apiKey,
		APIURL: apiURL,
		Model:  model,
		Client: &http.Client{Timeout: 60 * time.Second},
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
	templateContent, err := os.ReadFile("prompt_template.txt")
	if err != nil {
		log.Printf("Warning: Could not read prompt_template.txt: %v. Using default short prompt.", err)
		sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\\n\\n")
		sb.WriteString("# 核心目标\\n最大化夏普比率。只在多重信号共振时开仓(信心度>=75)。\\n\\n")
	} else {
		sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\\n\\n")
		sb.WriteString(string(templateContent))
		sb.WriteString("\\n\\n")
	}

	sb.WriteString("# 硬约束（风险控制）\\n")
	sb.WriteString("1. 风险回报比: 必须 ≥ 1:3\n")
	sb.WriteString("2. 单笔风险上限: 账户净值的 1%-3%\n")
	sb.WriteString(fmt.Sprintf("3. 杠杆限制: 山寨币最大%dx | BTC/ETH最大%dx\n", altcoinLeverage, btcEthLeverage))
	sb.WriteString("4. 保证金使用率 ≤ 90%\n\n")

	sb.WriteString("# 输出格式 (严格遵守)\n")
	sb.WriteString("**必须使用XML标签 <reasoning> 和 <decision> 标签分隔思维链和决策JSON**\n\n")
	sb.WriteString("<reasoning>\n你的分析过程...\n</reasoning>\n\n")
	sb.WriteString("<decision>\n```json\n[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_long\", \"leverage\": 5, \"position_size_usd\": 1000, \"stop_loss\": 90000, \"profit_target\": 95000, \"confidence\": 85, \"risk_usd\": 50, \"invalidation_condition\": \"RSI drops below 30\", \"reasoning\": \"...\"}\n"))
	sb.WriteString("]\n```\n</decision>\n")

	return sb.String()
}

func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("时间: %s | 运行: %d分钟\n\n", ctx.CurrentTime, ctx.RuntimeMinutes))
	
	// 账户信息
	sb.WriteString(fmt.Sprintf("账户: 净值%.2f | 可用%.2f | 盈亏%+.2f%%\n\n",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.TotalPnLPct))

	// 持仓信息
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for _, pos := range ctx.Positions {
			sb.WriteString(fmt.Sprintf("%s %s | 入场%.4f 当前%.4f | 盈亏%+.2f U (%+.2f%%) | 杠杆%dx\n",
				pos.Symbol, strings.ToUpper(pos.Side), pos.EntryPrice, pos.MarkPrice,
				pos.UnrealizedPnL, pos.UnrealizedPnLPct, pos.Leverage))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("当前持仓: 无\n\n")
	}

	// 市场数据
	sb.WriteString("## 市场数据\n")
	for symbol, data := range ctx.MarketDataMap {
		sb.WriteString(fmt.Sprintf("### %s\n", symbol))
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
	sb.WriteString(fmt.Sprintf("current_price = %s, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n\n",
		priceStr, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		oiLatestStr := formatPriceWithDynamicPrecision(data.OpenInterest.Latest)
		oiAverageStr := formatPriceWithDynamicPrecision(data.OpenInterest.Average)
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %s Average: %s\n\n",
			oiLatestStr, oiAverageStr))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	if data.IntradaySeries != nil {
		sb.WriteString("Intraday series (3‑minute intervals, oldest → latest):\n\n")

		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}

		if len(data.IntradaySeries.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20‑period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		}

		if len(data.IntradaySeries.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		}

		if len(data.IntradaySeries.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		}

		if len(data.IntradaySeries.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		}

		if len(data.IntradaySeries.Volume) > 0 {
			sb.WriteString(fmt.Sprintf("Volume: %s\n\n", formatFloatSlice(data.IntradaySeries.Volume)))
		}

		sb.WriteString(fmt.Sprintf("3m ATR (14‑period): %.3f\n\n", data.IntradaySeries.ATR14))
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

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API Error: %s", string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.Unmarshal(body, &result)
	
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("No response from AI")
	}

	return result.Choices[0].Message.Content, nil
}

func parseAIResponse(response string) (*FullDecision, error) {
	// 1. 提取 Reasoning
	reasoning := extractTagContent(response, "reasoning")
	if reasoning == "" {
		// Fallback: if no tags, try to extract before JSON
		idx := strings.Index(response, "```json")
		if idx > 0 {
			reasoning = response[:idx]
		} else {
			reasoning = response // worst case
		}
	}

	// 2. 提取 Decision JSON
	decisionContent := extractTagContent(response, "decision")
	if decisionContent == "" {
		decisionContent = response // Fallback
	}
	
	// 提取 JSON 代码块
	reJSON := regexp.MustCompile("(?s)```json\\s*(.*?)\\s*```")
	match := reJSON.FindStringSubmatch(decisionContent)
	var jsonStr string
	if len(match) > 1 {
		jsonStr = match[1]
	} else {
		// 尝试直接查找 []
		start := strings.Index(decisionContent, "[")
		end := strings.LastIndex(decisionContent, "]")
		if start != -1 && end != -1 && end > start {
			jsonStr = decisionContent[start : end+1]
		}
	}

	var decisions []Decision
	if jsonStr != "" {
		// 简单的全角转半角修复
		jsonStr = strings.ReplaceAll(jsonStr, "，", ",")
		jsonStr = strings.ReplaceAll(jsonStr, "：", ":")
		
		if err := json.Unmarshal([]byte(jsonStr), &decisions); err != nil {
			log.Printf("JSON解析失败，原始内容: %s", jsonStr)
			// 不返回 error，而是返回空决策，保证程序不崩
		}
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

// Helper
func min(a, b int) int {
	if a < b { return a }
	return b
}
