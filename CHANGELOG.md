# 更新日志 / Changelog

## 2025-12-07

### 风控与实盘执行
- **动态止损距离保护（第一版）**：
  - 在 `risk.go` 中为 `update_stop_loss` 增加最小价格缓冲校验，结合当前价与 5m ATR：
    - 新止损与当前价格之间至少保留 `max(0.15%, 0.35 * ATR14_5m%)` 的距离。
    - 目的是在 2 分钟循环节奏下，避免止损被频繁贴得过近、被短期噪音反复扫出局。

- **挂单清理：仓位结束后的 SL/TP 自动撤单**：
  - 在 `main.go` 主循环中，针对真实币安模式(`*BinanceExchange`)增加兜底逻辑：
    - 每个周期结束时，如果某个交易对在 `trading_symbols` 中但已无持仓，则自动调用：
      - `CancelStopLossOrders(symbol)` 清理遗留止损单。
      - `CancelTakeProfitOrders(symbol)` 清理遗留止盈单。
    - 解决了由交易所自动触发 SL/TP 平仓后，另一侧挂单可能残留的问题。

### Prompt 与行为约束
- 在 `extracted_prompts.md` 中补充/强调：
  - **止损移动频率约束**：明确提示模型不要在每个 2 分钟周期小幅上移/下移止损，只有当价格走出新的结构（约 0.5–1 个 5m ATR）时，才考虑“跳档式”调整止损。
  - **反模式说明**：新增“频繁小幅挪动止损”作为需要避免的行为，提示该行为在当前循环周期下非常容易被短线噪音频繁扫损。

---

### 2025-12-07 (晚间) — 固定 20x 高杠杆模式 + 止损缓冲调优

#### 1. 固定 20x 杠杆模式
- 在 `risk.go` 中引入固定杠杆常量，并在开仓风控中强制使用：
  - 新增常量：`fixedLeverage = 20`。
  - 开仓验证时不再按照 BTC/ETH / Altcoin 区分杠杆上限，而是：
    - 所有 `open_long` / `open_short` 决策统一使用 `fixedLeverage` 作为实际杠杆。
    - 无论模型在 JSON 中给出多少 `leverage`，都会被覆盖为 `20`，并输出日志：
      - `⚠️ [Leverage Force] SYMBOL 强制使用固定杠杆 20x (模型提出 xx 已被覆盖)`。
  - 全局名义仓位上限仍然按 `accountEquity * fixedLeverage` 计算，并通过 `[Size Fallback]` 自动缩仓，而不是直接拒单。

- 在配置层确保默认杠杆与风控一致：
  - `config.go`：
    - `BTCETHLeverage` 默认值保持 `20`。
    - `AltcoinLeverage` 默认值从 `15` 提升为 `20`，用于 UI / 上下文展示。
  - `config.local.example.json`：
    - 示例配置中将 `altcoin_leverage` 从 `15` 更新为 `20`，与实盘固定 20x 模式对齐。

- 币安实盘执行路径：
  - `BinanceExchange.ExecuteDecision` 仍然基于 `d.Leverage` 调用 `NewChangeLeverageService` 设置合约杠杆。
  - 由于风控层已经将 `d.Leverage` 统一强制为 `20`，实盘合约杠杆也会同步固定在 20x。

#### 2. 单笔保证金占用上限下调（配合 20x）
- 在 `risk.go` 中，将单笔保证金占用上限从 50% 下调至 30%：
  - 新常量：`maxMarginUsagePerTrade = 0.3`（原为 `0.5`）。
  - 风控逻辑：
    - 计算本单所需保证金：`marginRequired = PositionSizeUSD / leverage`。
    - 计算单笔最大允许保证金：`maxMarginPerTrade = available * maxMarginUsagePerTrade * safetyReserveFactor`。
    - 若 `marginRequired` 超过上限，则自动缩小 `PositionSizeUSD`，并记录日志：
      - `⚠️ [Margin Fallback] SYMBOL 需要保证金 X 超过单笔上限 Y，自动缩小仓位到 Z USDT`。
- 作用：
  - 在固定 20x 模式下，避免单笔交易占用过多可用保证金，保证账户在多笔并行或连续亏损场景下仍有充足缓冲空间。

#### 3. 动态止损最小距离进一步调宽
- 结合实盘体验（ETH 单在 2 分钟循环下“刚想抬 SL 就被下一根 K 线扫掉”），对 `update_stop_loss` 的最小距离做了二次调优：
  - 绝对最小缓冲从 `0.15%` 提升到 `0.25%`：
    - `minStopDistancePctFloor = 0.25`。
  - 相对 ATR 的缓冲因子从 `0.35` 提升到 `0.50`：
    - `minStopDistanceATRFactor = 0.50`。
  - 校验逻辑保持不变：
    - 计算新止损到当前价的百分比距离 `distPct`；
    - 计算 ATR 基准缓冲 `atrPct * minStopDistanceATRFactor`；
    - 要求 `distPct >= max(minStopDistancePctFloor, atrPct * factor)`，否则直接拒绝更新并返回详细错误信息。
- 目的：
  - 让所有追踪止损更“钝”一些，减少在 2 分钟节奏下因轻微波动频繁触发 SL 的情况，尤其是 ETH/BTC 这类主流币的窄波动环境。

#### 4. Prompt 同步为“固定 20x 杠杆”认知
- 在 `extracted_prompts.md` 中更新了对杠杆和仓位计算的描述，使 LLM 与实盘逻辑完全一致：
  - 核心目标段落：
    - 从“中高杠杆（BTC/ETH 常用 5x–10x…）”改为：
      - “使用 **固定 20x 杠杆** 放大收益，你不能通过调节杠杆来控制风险，只能通过仓位大小和止损位置来控制单笔最大亏损（后端会把单笔最大亏损严格控制在账户净值约 3% 以内）。”
  - 仓位大小计算部分：
    - 明确说明 `position_size_usd` 是包含**固定 20x** 杠杆的名义价值；
    - 模型“不需要、也不能手动调整杠杆”，只能通过 `position_percent / position_size_usd` + SL 距离来控制风险。
  - 计算示例：
    - 将示例从“5x 杠杆”改为“固定 20x 杠杆”，名义价值 = 可用保证金 × 20，便于模型形成直觉：
      - 可用资金 500U → 可用保证金 440U → `position_size_usd = 440 * 20 = 8800`。

> 总体而言，本轮更新将系统切换为“**名义全 20x，高杠杆日内模式**”：
> - 杠杆数字固定为 20x，但通过仓位和止损严格控制单笔最大亏损；
> - 单笔保证金占用更保守（30% 上限），降低爆仓风险；
> - 移动止损更“抗噪音”，减少因为 2 分钟循环和窄 SL 导致的频繁被扫。 
