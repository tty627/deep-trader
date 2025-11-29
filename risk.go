package main

import (
	"fmt"
	"log"
)

// ValidateDecisions 验证所有决策
func ValidateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
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
		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage          // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 1.5 // 山寨币最多1.5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}

		// Fallback 机制：杠杆超限时自动修正为上限值
		if d.Leverage <= 0 {
			return fmt.Errorf("杠杆必须大于0: %d", d.Leverage)
		}
		if d.Leverage > maxLeverage {
			log.Printf("⚠️ [Leverage Fallback] %s 杠杆超限 (%dx > %dx)，自动调整为上限值 %dx",
				d.Symbol, d.Leverage, maxLeverage, maxLeverage)
			d.Leverage = maxLeverage // 自动修正为上限值
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}

		// 验证最小开仓金额
		const minPositionSizeGeneral = 12.0
		if d.PositionSizeUSD < minPositionSizeGeneral {
			return fmt.Errorf("开仓金额过小(%.2f USDT)，必须≥%.2f USDT", d.PositionSizeUSD, minPositionSizeGeneral)
		}

		// 验证仓位价值上限
		tolerance := maxPositionValue * 0.01 // 1%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			return fmt.Errorf("仓位价值过大(%.2f)，超过限制(%.2f)", d.PositionSizeUSD, maxPositionValue)
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

		// 验证风险回报比（必须≥1:3）
		// 计算入场价（假设当前市价）
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

		// 验证单笔风险金额上限 (Max Risk per Trade)
		// 风险金额 = 仓位大小 * 风险百分比 / 100
		estimatedRiskUSD := d.PositionSizeUSD * (riskPercent / 100.0)
		maxAllowedRiskUSD := accountEquity * 0.03 // 最大允许亏损账户净值的 3%

		if estimatedRiskUSD > maxAllowedRiskUSD {
			return fmt.Errorf("单笔风险金额过高($%.2f)，超过账户净值的3%%($%.2f) [Risk: %.2f%%]",
				estimatedRiskUSD, maxAllowedRiskUSD, riskPercent)
		}

		// 硬约束：风险回报比必须≥3.0
		if riskRewardRatio < 3.0 {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥3.0:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	// 动态调整止损验证
	if d.Action == "update_stop_loss" {
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
