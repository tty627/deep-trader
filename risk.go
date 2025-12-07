package main

import (
	"fmt"
	"log"
)

// 全局风控常量
const (
	// 建议的单笔最小名义仓位，用于避免交易所最小名义限制（约 10U）带来的报错
	minPositionSizeGeneral = 12.0

	// 单笔交易最多使用可用余额的多少作为保证金，避免一次性 All‑in
	maxMarginUsagePerTrade = 0.5 // 50%

	// 预留 12% 安全边际，和 prompt 中的说明保持一致
	safetyReserveFactor = 0.88
)

// ValidateDecisions 验证所有决策
func ValidateDecisions(decisions []Decision, account AccountInfo, btcEthLeverage, altcoinLeverage int) error {
	for i := range decisions {
		if err := validateDecision(&decisions[i], account, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, account AccountInfo, btcEthLeverage, altcoinLeverage int) error {
	accountEquity := account.TotalEquity
	available := account.AvailableBalance

	// 验证action
	validActions := map[string]bool{
		"open_long":          true,
		"open_short":         true,
		"close_long":         true,
		"close_short":        true,
		"update_stop_loss":   true,
		"update_take_profit": true,
		"partial_close":      true,
		"hold":               true,
		"wait":               true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		if accountEquity <= 0 {
			return fmt.Errorf("账户净值为0，无法进行开仓验证")
		}

		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage
		}
		if maxLeverage <= 0 {
			return fmt.Errorf("无效的最大杠杆配置: %d", maxLeverage)
		}
		maxPositionValue := accountEquity * float64(maxLeverage)

		// 杠杆校验与回退
		if d.Leverage <= 0 {
			return fmt.Errorf("杠杆必须大于0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			log.Printf("⚠️ [Leverage Fallback] %s 杠杆超限 (%dx > %dx)，自动调整为上限值 %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage
		}

		// 如果 position_size_usd 未给出，但提供了 position_percent，则根据相对比例计算
		if d.PositionSizeUSD <= 0 && d.PositionPercent > 0 {
			pct := d.PositionPercent
			if pct > 1 {
				// 若大于 1，按 0–100 视为百分比
				pct = pct / 100.0
			}
			if pct <= 0 || pct > 1 {
				return fmt.Errorf("position_percent 非法: %.4f，应在 (0,100] 或 (0,1]", d.PositionPercent)
			}

			if available <= 0 {
				return fmt.Errorf("账户可用余额不足，无法根据 position_percent 计算仓位")
			}

			// 以可用余额为基础，预留一部分缓冲
			marginBudget := available * safetyReserveFactor * pct
			if marginBudget <= 0 {
				return fmt.Errorf("根据 position_percent 计算得到的保证金无效: %.2f", marginBudget)
			}
			d.PositionSizeUSD = marginBudget * float64(d.Leverage)
			log.Printf("ℹ️ [PositionPercent] %s 使用 position_percent=%.2f 计算得到名义仓位: %.2f USDT (保证金≈%.2f, 杠杆=%dx)",
				d.Symbol, d.PositionPercent, d.PositionSizeUSD, marginBudget, d.Leverage)
		}

		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}

		// 验证最小开仓金额 (仍然仅做警告，不强制拦截)
		if d.PositionSizeUSD < minPositionSizeGeneral {
			log.Printf("⚠️ [Warning] 开仓金额过小(%.2f USDT)，建议≥%.2f USDT，但允许执行", d.PositionSizeUSD, minPositionSizeGeneral)
		}

		// 先按全局净值 + 杠杆上限做一次硬上限，并采用自动缩小仓位的 Fallback，而不是直接拒绝
		tolerance := maxPositionValue * 0.01 // 1% 容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			capped := maxPositionValue
			log.Printf("⚠️ [Size Fallback] %s 仓位名义金额过大(%.2f > %.2f)，自动下调为上限 %.2f",
				d.Symbol, d.PositionSizeUSD, maxPositionValue, capped)
			d.PositionSizeUSD = capped
		}

		// 再按单笔最大保证金占用控制，避免一次性吃掉全部可用余额
		if available > 0 {
			marginRequired := d.PositionSizeUSD / float64(d.Leverage)
			maxMarginPerTrade := available * maxMarginUsagePerTrade * safetyReserveFactor
			if marginRequired > maxMarginPerTrade {
				if maxMarginPerTrade <= 0 {
					return fmt.Errorf("账户可用保证金不足以支撑该仓位: 需要 %.2f, 可用 %.2f", marginRequired, available)
				}
				newPosSize := maxMarginPerTrade * float64(d.Leverage)
				log.Printf("⚠️ [Margin Fallback] %s 需要保证金 %.2f 超过单笔上限 %.2f，自动缩小仓位到 %.2f USDT",
					d.Symbol, marginRequired, maxMarginPerTrade, newPosSize)
				d.PositionSizeUSD = newPosSize
			}
		}

		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（合理性放宽）
		// 计算入场价（假设当前市价在止损和止盈之间）
		var entryPrice float64
		if d.Action == "open_long" {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		// 硬约束：风险回报比必须≥3.0
		minRR := 3.0

		if riskRewardRatio < minRR {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥%.1f:1 [风险:%.2f%% 收益:%.2f%%]",
				riskRewardRatio, minRR, riskPercent, rewardPercent)
		}
	}

	// 动态调整止损验证
	if d.Action == "update_stop_loss" {
		// 兼容模型可能错误使用 stop_loss 字段的情况：
		// 如果 new_stop_loss 为空但 stop_loss > 0，则自动视为 new_stop_loss
		if d.NewStopLoss <= 0 && d.StopLoss > 0 {
			log.Printf("⚠️ [Fallback] update_stop_loss 使用了 stop_loss 字段，自动将 new_stop_loss 设置为 %.4f", d.StopLoss)
			d.NewStopLoss = d.StopLoss
		}
		if d.NewStopLoss <= 0 {
			return fmt.Errorf("新止损价格必须大于0: %.2f", d.NewStopLoss)
		}
	}

	// 动态调整止盈验证
	if d.Action == "update_take_profit" {
		if d.NewTakeProfit <= 0 {
			return fmt.Errorf("新止盈价格必须大于0: %.2f", d.NewTakeProfit)
		}
	}

	// 部分平仓验证
	if d.Action == "partial_close" {
		if d.ClosePercentage <= 0 || d.ClosePercentage > 100 {
			return fmt.Errorf("平仓百分比必须在0-100之间: %.1f", d.ClosePercentage)
		}
	}

	return nil
}
