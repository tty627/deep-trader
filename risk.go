package main

import (
	"fmt"
	"log"
	"math"
)

// 全局风控常量
const (
	// 建议的单笔最小名义仓位，用于避免交易所最小名义限制（约 10U）带来的报错
	minPositionSizeGeneral = 12.0


	// 预留 12% 安全边际，和 prompt 中的说明保持一致
	safetyReserveFactor = 0.88

	// 单笔最大风险（占账户净值的比例），用于高杠杆日内模式
	// 中度激进：单笔最多亏损约 3% 净值
	maxRiskPctPerTrade = 0.03 // 3%

	// 单笔交易最多使用可用余额的多少作为保证金，避免一次性 All‑in
	maxMarginUsagePerTrade = 0.3 // 30%

	// 固定杠杆模式：所有开仓统一使用 10x 杠杆，由仓位大小和止损控制真实风险
	fixedLeverage = 10

	// 动态移动止损时，要求止损与当前价格之间保留的最小缓冲
	// 由于循环周期是 2 分钟，如果止损离当前价过近，会被噪音频繁扫损
	// 原先的绝对下限是 0.15%，在 ETH 这类品种上的体感略偏紧，容易出现“刚想上移止损就被 1 根 K 线扫掉”的情况
	// 这里适度降低为 0.12%，并将 ATR 缓冲从 50% 降低到 20%，让追踪止损更灵活
	minStopDistancePctFloor  = 0.12 // 至少 0.12%
	minStopDistanceATRFactor = 0.20 // 至少保留约 20% 的 5m ATR 作为缓冲
)

// ValidateDecisions 验证所有决策
func ValidateDecisions(decisions []Decision, account AccountInfo, btcEthLeverage, altcoinLeverage int, mdMap map[string]*MarketData) error {
	for i := range decisions {
		if err := validateDecision(&decisions[i], account, btcEthLeverage, altcoinLeverage, mdMap); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, account AccountInfo, btcEthLeverage, altcoinLeverage int, mdMap map[string]*MarketData) error {
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

		// 固定杠杆模式：所有币种统一使用 20x，由 fixedLeverage 控制
		maxLeverage := fixedLeverage
		if maxLeverage <= 0 {
			return fmt.Errorf("无效的固定杠杆配置: %d", maxLeverage)
		}

		// 无论模型给出多少杠杆，这里都强制覆盖为 fixedLeverage，确保实盘和风控一致
		if d.Leverage != maxLeverage {
			log.Printf("⚠️ [Leverage Force] %s 强制使用固定杠杆 %dx (模型提出 %dx 已被覆盖)", d.Symbol, maxLeverage, d.Leverage)
			d.Leverage = maxLeverage
		}

		maxPositionValue := accountEquity * float64(maxLeverage)

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

		// ===== 计算近似入场价：优先使用当前市价，其次退回到区间内插值 =====
		var entryPrice float64
		if mdMap != nil {
			if md, ok := mdMap[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 {
				entryPrice = md.CurrentPrice
			}
		}
		// 回退：仍然使用止损/止盈之间的 20% 位置作为近似
		if entryPrice <= 0 {
			if d.Action == "open_long" {
				entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2
			} else {
				entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2
			}
		}
		if entryPrice <= 0 {
			return fmt.Errorf("无法估算入场价，用于风险评估失败")
		}

		// ===== 基于价格距离和仓位大小估算单笔风险 =====
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

		// 使用价格风险估算本次交易的资金风险（不依赖杠杆，名义价值 * 价格变动百分比）
		var estimatedRiskUsd, riskPctOfEquity float64
		if riskPercent > 0 && accountEquity > 0 {
			// 价格风险为负数说明 SL 在错误一侧
			if riskPercent < 0 {
				return fmt.Errorf("止损价格位置异常，导致风险为负: %.2f%%", riskPercent)
			}
			estimatedRiskUsd = d.PositionSizeUSD * math.Abs(riskPercent) / 100.0
			riskPctOfEquity = estimatedRiskUsd / accountEquity
			maxRiskUsd := accountEquity * maxRiskPctPerTrade
			if maxRiskUsd > 0 && estimatedRiskUsd > maxRiskUsd {
				// 自动缩小仓位以符合单笔风险上限
				newPos := maxRiskUsd * 100.0 / math.Abs(riskPercent)
				log.Printf("⚠️ [Risk Fallback] %s 单笔风险 %.2f USDT 超过上限 %.2f，自动缩小仓位到 %.2f USDT",
					d.Symbol, estimatedRiskUsd, maxRiskUsd, newPos)
				d.PositionSizeUSD = newPos
				// 更新缩仓后的风险估算
				estimatedRiskUsd = maxRiskUsd
				riskPctOfEquity = estimatedRiskUsd / accountEquity
			}
		}

		// 基于单笔风险占净值的大小，采用两档 RR 要求：
		//  - 小仓试探单（风险 <= 1.5% 净值）：允许更低的 RR，下限约 0.8:1
		//  - 正式仓位（风险 > 1.5% 净值）：要求 RR >= 1.3:1
		probeRiskPct := 0.015  // 1.5%
		strictMinRR := 1.3     // 从 2.0 降低到 1.3
		probeMinRR := 0.8

		// 如果无法估算风险（例如缺少价格），回退使用严格 RR
		minRR := strictMinRR
		if riskPctOfEquity > 0 && riskPctOfEquity <= probeRiskPct {
			minRR = probeMinRR
		}

		if riskRewardRatio < minRR {
			// 记录更详细的上下文，便于调试
			log.Printf("⚠️ [RR Reject] %s RR=%.2f:1 risk=%.2f%% reward=%.2f%% riskPctOfEquity=%.2f%% minRR=%.2f",
				d.Symbol, riskRewardRatio, riskPercent, rewardPercent, riskPctOfEquity*100, minRR)
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

		// 额外约束：新止损不能离当前市价过近，否则在 2 分钟循环下容易被噪音频繁扫损
		if mdMap != nil {
			if md, ok := mdMap[d.Symbol]; ok && md != nil && md.CurrentPrice > 0 {
				cur := md.CurrentPrice
				// 推断方向：正常情况下，做多止损 < 现价，做空止损 > 现价
				var distPct float64
				if d.NewStopLoss < cur {
					// 视为多单：要求当前价到止损至少有一定百分比的缓冲
					distPct = (cur - d.NewStopLoss) / cur * 100
				} else {
					// 视为空单：要求当前价到止损至少有一定百分比的缓冲
					distPct = (d.NewStopLoss - cur) / cur * 100
				}

				// 结合绝对下限 + ATR 估算的动态下限
				minPct := minStopDistancePctFloor
				if md.ATR14_5m > 0 && cur > 0 {
					atrPct := md.ATR14_5m / cur * 100
					buf := atrPct * minStopDistanceATRFactor
					if buf > minPct {
						minPct = buf
					}
				}

				if distPct > 0 && distPct < minPct {
					return fmt.Errorf("新止损 %.4f 离当前价格过近(约%.2f%%)，为了避免 2 分钟周期的噪音频繁扫损，至少需要保留 ≥ %.2f%% 的价格缓冲", d.NewStopLoss, distPct, minPct)
				}
			}
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
