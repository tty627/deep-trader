package main

import (
	"fmt"
	"log"
	"math"
)

// 全局风控常量（不随策略变化的部分）
const (
	// 建议的单笔最小名义仓位，用于避免交易所最小名义限制（约 10U）带来的报错
	minPositionSizeGeneral = 12.0

	// 预留 12% 安全边际，和 prompt 中的说明保持一致
	safetyReserveFactor = 0.88

	// Altcoin 专属单笔风险硬上限（USDT），避免山寨仓位风险与 BTC/ETH 等权。
	maxRiskUsdAltPerTrade = 12.0 // 单笔 Altcoin 理论最大亏损建议控制在 ~10–12U

	// Altcoin 单笔保证金占用更保守，避免与主流币同级别爆仓风险
	maxAltMarginUsagePerTrade = 0.40 // 40%

	// 绝对金额上的单笔风险硬上限（USDT），用于实现近似固定 risk_usd 风格
	// 例如在 ~100U 账户下，A+ 机会单笔允许亏损约 40–50U
	maxRiskUsdHardPerTrade = 50.0

	// 动态移动止损时，要求止损与当前价格之间保留的最小缓冲
	// 在保持高杠杆抗噪性的前提下略微放松：允许止损靠近一些，以便更积极锁定利润
	minStopDistancePctFloor  = 0.18 // 至少 0.18%
	minStopDistanceATRFactor = 0.35 // 至少保留约 35% 的 5m ATR 作为缓冲
)

// getRiskConfig 获取当前策略的风险配置，包含动态参数
func getRiskConfig() RiskConfig {
	sm := GetStrategyManager()
	if sm != nil {
		return sm.GetRiskConfig()
	}
	// 回退到默认配置
	return RiskConfig{
		MaxRiskPerTrade:     0.25,
		MaxTotalRisk:        0.40,
		MinRiskRewardRatio:  2.0,
		FixedLeverage:       15,
		MaxMarginUsage:      0.70,
		StopLossATRMultiple: 1.8,
	}
}

// ValidateDecisions 验证所有决策
func ValidateDecisions(decisions []Decision, account AccountInfo, mdMap map[string]*MarketData) error {
	var totalRiskPct float64
	for i := range decisions {
		if err := validateDecision(&decisions[i], account, mdMap, &totalRiskPct); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, account AccountInfo, mdMap map[string]*MarketData, totalRiskPct *float64) error {
	accountEquity := account.TotalEquity
	available := account.AvailableBalance

	// 兼容模型可能输出的少量别名 / 不支持的 action，做宽松处理
	switch d.Action {
	case "increase_position":
		// 将 increase_position 视为在当前方向上追加仓位，这里统一按 open_long 处理
		// （当前策略几乎只做多，如需做空加仓应在 prompt 中明确使用 open_short）
		log.Printf("⚠️ [Action Fallback] %s 使用未支持的 action increase_position，自动按 open_long 处理", d.Symbol)
		d.Action = "open_long"
	case "limit_order", "limit_long", "limit_short":
		// 暂不支持真实挂限价单，避免与当前市价执行逻辑冲突：直接忽略为观望
		log.Printf("⚠️ [Action Reject] %s 使用未支持的限价类 action=%s，已忽略（视为 wait）", d.Symbol, d.Action)
		d.Action = "wait"
	}

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
		// 不再让单个未知 action 让整批决策失败：记录日志并将其视为观望
		log.Printf("⚠️ [Action Reject] %s 不支持的action=%s，已自动忽略（视为 wait）", d.Symbol, d.Action)
		d.Action = "wait"
		return nil
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		// 判断是否为 Altcoin（非 BTC/ETH），用于后续专属风险控制
		isAlt := d.Symbol != "BTCUSDT" && d.Symbol != "ETHUSDT"
		if accountEquity <= 0 {
			return fmt.Errorf("账户净值为0，无法进行开仓验证")
		}

		// 从当前策略获取风险配置
		riskCfg := getRiskConfig()
		
		// 固定杠杆模式：所有币种统一使用策略配置的杠杆
		maxLeverage := riskCfg.FixedLeverage
		if maxLeverage <= 0 {
			maxLeverage = 15 // 安全默认值
		}

		// 无论模型给出多少杠杆，这里都强制覆盖为策略配置的杠杆，确保实盘和风控一致
		if d.Leverage != maxLeverage {
			log.Printf("⚠️ [Leverage Force] %s 强制使用策略杠杆 %dx (模型提出 %dx 已被覆盖)", d.Symbol, maxLeverage, d.Leverage)
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
				maxMarginPerTrade := available * riskCfg.MaxMarginUsage * safetyReserveFactor
				if isAlt {
					maxMarginPerTrade = available * maxAltMarginUsagePerTrade * safetyReserveFactor
				}
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

			// 单笔风险上限：既限制百分比，也限制绝对金额（近似固定 risk_usd 风格）
			maxRiskUsdByPct := accountEquity * riskCfg.MaxRiskPerTrade
			maxRiskUsd := maxRiskUsdByPct
			if maxRiskUsdHardPerTrade > 0 && maxRiskUsd > maxRiskUsdHardPerTrade {
				maxRiskUsd = maxRiskUsdHardPerTrade
			}

			if maxRiskUsd > 0 && estimatedRiskUsd > maxRiskUsd {
				// 自动缩小仓位以符合单笔风险上限
				newPos := maxRiskUsd * 100.0 / math.Abs(riskPercent)
				log.Printf("⚠️ [Risk Fallback] %s 单笔风险 %.2f USDT 超过单笔上限 %.2f，自动缩小仓位到 %.2f USDT",
					d.Symbol, estimatedRiskUsd, maxRiskUsd, newPos)
				d.PositionSizeUSD = newPos
				// 更新缩仓后的风险估算
				estimatedRiskUsd = maxRiskUsd
				riskPctOfEquity = estimatedRiskUsd / accountEquity
			}

			// Altcoin 进一步收紧单笔风险上限，使其更偏向“小仓战术单”而非主仓
			if isAlt && maxRiskUsdAltPerTrade > 0 && estimatedRiskUsd > maxRiskUsdAltPerTrade {
				newPos := maxRiskUsdAltPerTrade * 100.0 / math.Abs(riskPercent)
				log.Printf("⚠️ [Alt Risk Fallback] %s Altcoin 单笔风险 %.2f USDT 超过 Alt 上限 %.2f，自动缩小仓位到 %.2f USDT",
					d.Symbol, estimatedRiskUsd, maxRiskUsdAltPerTrade, newPos)
				d.PositionSizeUSD = newPos
				estimatedRiskUsd = maxRiskUsdAltPerTrade
				riskPctOfEquity = estimatedRiskUsd / accountEquity
			}
		}

		if totalRiskPct != nil && riskPctOfEquity > 0 {
			currentTotal := *totalRiskPct
			proposedTotal := currentTotal + riskPctOfEquity
			if proposedTotal > riskCfg.MaxTotalRisk {
				remaining := riskCfg.MaxTotalRisk - currentTotal
				if remaining <= 0 {
					return fmt.Errorf("所有新开仓风险之和已达到全局上限 %.2f%%，拒绝 %s", riskCfg.MaxTotalRisk*100, d.Symbol)
				}
				// 将本次仓位缩小到仅使用剩余风险预算
				allowedRiskUsd := remaining * accountEquity
				newPos := allowedRiskUsd * 100.0 / math.Abs(riskPercent)
				log.Printf("⚠️ [Global Risk Fallback] %s 总风险将超出上限，调整本单风险从 %.2f USDT 降至 %.2f USDT，仓位从 %.2f USDT 降至 %.2f USDT",
					d.Symbol, estimatedRiskUsd, allowedRiskUsd, d.PositionSizeUSD, newPos)
				d.PositionSizeUSD = newPos
				estimatedRiskUsd = allowedRiskUsd
				riskPctOfEquity = remaining
				proposedTotal = currentTotal + remaining
			}
			*totalRiskPct = proposedTotal
		}

		// 基于单笔风险占净值的大小，采用两档 RR 要求：
		//  - 小仓试探单（风险 <= 1.5% 净值）：允许略低的 RR，下限约 1.0:1
		//  - 正式仓位（风险 > 策略配置的阈值）：使用策略配置的最小 RR
		probeRiskPct := 0.015 // 1.5%
		strictMinRR := riskCfg.MinRiskRewardRatio
		if strictMinRR <= 0 {
			strictMinRR = 1.8 // 安全默认值
		}
		probeMinRR := 1.0

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
					// 不再视为硬错误，而是记录信息并将本次止损调整视为 no-op，避免打断整批决策执行。
					log.Printf("ℹ️ [SL Noop] %s 新止损 %.4f 距当前价约 %.2f%% (< 最小缓冲 %.2f%%)，为避免 2 分钟周期噪音频繁扫损，本轮放弃调整止损，保持原止损不变", d.Symbol, d.NewStopLoss, distPct, minPct)
					// 将本次动作视为 hold，后续执行层不会真正下发 update_stop_loss 指令。
					d.Action = "hold"
					return nil
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
		// 部分平仓本质上是减仓行为，不增加风险，这里只做参数合法性校验
		// 优先使用 close_percentage；若缺失但给出了 position_size_usd，则允许执行层按金额推导比例
		if d.ClosePercentage > 0 && d.ClosePercentage <= 100 {
			// 正常按百分比部分平仓
		} else if d.ClosePercentage <= 0 && d.PositionSizeUSD > 0 {
			// 仅提供了按金额部分平仓的信息：在 ExecuteDecision 阶段根据当前持仓名义价值推导百分比
			log.Printf("⚠️ [Partial Fallback] %s partial_close 未提供 close_percentage，仅提供 position_size_usd=%.2f，将在执行层按金额推导比例", d.Symbol, d.PositionSizeUSD)
		} else {
			return fmt.Errorf("平仓百分比必须在0-100之间: %.1f", d.ClosePercentage)
		}
	}

	return nil
}
