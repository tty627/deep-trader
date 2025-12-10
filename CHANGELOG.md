# 更新日志 / Changelog

## 2025-12-10 — 多 AI 模型 + 策略系统 + 通知/存储/导出模块

### 多 AI 模型支持 (`ai_manager.go`)
- **AIManager 多模型管理器**：支持配置多个 AI 模型（不同 API 端点/模型名）并行或按优先级调用：
  - `primary` 模式：仅使用第一个启用的模型；
  - `vote` 模式：并行调用所有模型，按权重投票合并决策；
  - `compare` 模式：对比各模型输出（不执行交易），用于评估模型差异。
- 支持运行时切换模式，日志输出各模型决策耗时与结果。

### 策略模板系统 (`strategy.go`, `strategies/`)
- **StrategyManager 策略管理器**：支持多策略模板，每个策略包含独立的风控参数和 prompt 文件：
  - `balanced`：中等风险（15x 杠杆，单笔风险 25%，RR ≥ 2.0）；
  - `aggressive`：高风险高收益（30x 杠杆，单笔风险 50%，RR ≥ 1.8）；
  - `conservative`：低风险稳健（5x 杠杆，单笔风险 10%，RR ≥ 2.5）；
  - `scalping`：超短线快进快出（20x 杠杆，单笔风险 5%，RR ≥ 1.5）。
- 策略可通过 Web UI 或 API 动态切换，`brain.go` 自动加载当前策略的 `RiskConfig` 注入 prompt。
- 策略目录 `strategies/` 包含各策略的 `.md` prompt 文件。

### 存储系统 (`storage.go`)
- **JSON 文件存储**：新增轻量级持久化层 `Storage`，存储路径默认 `data/storage.json`：
  - 净值快照（`EquitySnapshot`）：定期保存账户净值、PnL、PnL%；
  - 交易记录（`TradeRecord`）；
  - AI 决策记录（`AIDecisionRecord`）：含 CoT 思维链、system/user prompt；
  - 配置快照（`ConfigSnapshot`）。
- 原子写入（先写 `.tmp` 再 rename），防止数据损坏。
- `main.go` 启动时调用 `InitGlobalStorage("data/storage.db")` 初始化。

### 通知系统 (`notifier.go`)
- **NotifyManager 通知管理器**：支持多通道推送交易事件：
  - **Telegram**：通过 Bot Token + Chat ID 发送格式化消息；
  - **Discord**：通过 Webhook URL 发送 Embed 消息；
  - **Email**：通过 SMTP 发送邮件。
- 事件类型：开仓 / 平仓 / 止损触发 / 止盈触发 / 风控拒绝 / 系统启停 / 高回撤警告等。

### 数据导出 (`export.go`)
- **Exporter 数据导出器**：支持将历史数据导出为 CSV 或 JSON：
  - `ExportEquityHistory`：净值曲线；
  - `ExportTradeRecords`：交易记录；
  - `ExportAIDecisions`：AI 决策历史；
  - `ExportFullReport`：完整报告（含以上全部 + 统计）。
- 支持时间范围筛选，输出目录可配置（默认 `exports/`）。

### Docker 支持
- 新增 `Dockerfile` 和 `docker-compose.yml`，支持容器化部署。
- 新增 `.dockerignore` 排除不必要文件。

### 其他改进
- **brain.go**：`buildSystemPrompt` 重构，自动从 `StrategyManager` 加载当前策略的风控参数并注入 prompt。
- **main.go**：
  - 新增 `peakEquity` 变量用于回撤熔断（Drawdown Kill Switch）；
  - 启动时初始化 `GlobalStorage` 和 `GlobalStrategyManager`。
- **.gitignore**：新增 `exports/`、Python 缓存、IDE 配置、OS 系统文件等条目。

---

## 2025-12-09 — 极端高杠杆 30x 趋势模式 + 强化风险上限

### 协议 & 执行稳健性增强（LLM 合约 / 部分平仓 / 止损兼容）
- **Decision 结构调整：支持浮点信心度 & 更健壮的 JSON 解析**：
  - 将 `Decision.Confidence` 类型从 `int` 升级为 `float64`，允许模型输出 `0.65` / `0.80` 等 0–1 小数或 0–100 分数，不再触发 `cannot unmarshal number ... of type int` 一类解析错误；
  - `parseAIResponse` 在解析失败时仅记录日志，不再清空已成功解析的决策，确保单字段异常不会导致整轮完全观望。
- **决策 action 归一化与宽松处理**：
  - 在 `main.go` 中新增 `normalizeDecisionActions`，在进入风控前自动将部分别名归一化为标准 action：
    - `close_position` → 根据当前持仓方向映射为 `close_long` / `close_short`；
    - `open_position` + `side=long/short/buy/sell` → 映射为 `open_long` / `open_short`；
  - 在 `risk.go` 中放宽未知 action 的处理方式：
    - 使用 `validActions` 白名单；对于未列出的 action，不再让整批决策失败，而是打出 `[Action Reject]` 日志并将该条决策视为 `wait`，同批其它合法决策继续执行。
- **部分平仓协议扩展：同时支持百分比和金额两种写法**：
  - 在 `risk.go` 中对 `partial_close` 调整参数校验逻辑：
    - 优先使用 `close_percentage` (0–100)；
    - 若 `close_percentage` 缺失但提供了 `position_size_usd > 0`，则记录 `[Partial Fallback]` 日志，并允许执行层按金额推导平仓比例；
  - 在 `BacktestExchange.ExecuteDecision` 的 `partial_close` 分支中：
    - 当 `close_percentage <= 0` 但 `position_size_usd > 0` 时，根据当前持仓名义价值推导 `pct ≈ position_size_usd / notional`，并回写为 `ClosePercentage`，同时调整数量 / 保证金 / 历史记录；
  - 在 `BinanceExchange.handlePartialClose` 中：
    - 同样兼容仅给 `position_size_usd` 的情况，根据 `currentPos.Quantity * currentPos.MarkPrice` 推导 `ClosePercentage`，并在日志中以 `Partial Close ... (xx.x%%)` 的形式展示最终平仓比例。
- **Binance 止损/止盈兼容性处理（-4120 错误）**：
  - 为 `BinanceExchange` 增加 `stopOrderSupported` 标记，默认为 `true`；
  - 在 `SetStopLoss` / `SetTakeProfit` 中：
    - 若 Binance 返回 `*futures.APIError` 且 `Code == -4120`（提示“Order type not supported for this endpoint, please use Algo Order API”），则：
      - 打出 `[SL Unsupported]` / `[TP Unsupported]` 日志，说明当前环境不再支持通过标准下单接口创建 STOP/TP；
      - 将 `stopOrderSupported` 置为 `false`，后续不再向交易所发送止损/止盈挂单，只依赖程序端的 `partial_close` 与 `close_*` 控制风险；
    - 当 `stopOrderSupported == false` 时，再次调用 `SetStopLoss` / `SetTakeProfit` 会直接记录 `[SL Disabled]` / `[TP Disabled]` 日志并返回 `nil`，避免控制台被重复的 -4120 错误刷屏。

### 风控核心变更（第二阶段：极端攻击型账户）
- **统一固定杠杆 30x**（延续上一版）：
  - 在 `risk.go` 中保持全局 `fixedLeverage = 30`，所有 `open_long` / `open_short` 决策的杠杆都会被风控层强制覆盖为 30x，并输出日志：
    - `⚠️ [Leverage Force] SYMBOL 强制使用固定杠杆 30x (模型提出 xx 已被覆盖)`。
- **单笔最大风险上限提升至约 50% 净值 / 50 USDT（取更小者）**：
  - 将 `maxRiskPctPerTrade` 从 `0.10` 提升至 **`0.50`**，允许单笔理论最大亏损达到账户净值的 50%。
  - 新增绝对金额上限常量 `maxRiskUsdHardPerTrade = 50.0`，在风险评估时取两者中的较小值作为本单最大允许亏损：
    - `maxRiskUsd = min(accountEquity * maxRiskPctPerTrade, maxRiskUsdHardPerTrade)`。
  - 一旦估算的价格风险超过该上限，风控会通过 `[Risk Fallback]` 自动缩小 `PositionSizeUSD`，使本单风险回落到该硬上限之内。
- **全局风险上限同步提升至约 50% 净值**：
  - 将 `globalMaxRiskPct` 从 `0.20` 提升至 **`0.50`**，在同一轮决策中累计所有“新开仓”的风险占比：
    - 若即将超出 50% 净值，则对当前单自动按剩余风险预算缩仓，并记录 `[Global Risk Fallback]` 日志；
    - 若预算已用尽，则直接拒绝新的开仓决策，返回错误提示。
- **单笔最大保证金占用进一步提升至 95%**：
  - 将 `maxMarginUsagePerTrade` 从 80% 提升至 **95%**，允许单笔交易在高置信度趋势下调动几乎全部可用保证金进行压注，仅保留少量流动性缓冲。

### 止损与 RR 要求调整
- **最小止损缓冲加宽以适配高杠杆**：
  - 将 `minStopDistancePctFloor` 从 `0.12%` 提升到 **`0.20%`**；
  - 将 `minStopDistanceATRFactor` 从 `0.20` 提升到 **`0.40`**，即最小距离需要达到约 0.4×ATR14_5m；
  - 目的是在 30x 杠杆下减少“贴脸止损”在 2 分钟循环中频繁被市场噪音扫掉的情况。
- **RR 要求更激进但更合理**：
  - 小仓试探单（风险 ≤ 1.5% 净值）：最低 RR 从 0.8:1 提升到 **1.0:1**；
  - 正式仓位（风险 > 1.5% 净值）：RR 下限从 1.3:1 提升到 **2.0:1**，确保高风险仓位只在有足够回报空间时才被接受。

### Prompt 更新：从“日内”到“高杠杆趋势跟随”
- `extracted_prompts.md` 重大改写：
  - 角色定位改为：
    - “你是专业的加密货币**高杠杆趋势跟随 / 波段交易 AI**，可以使用较高杠杆（后端统一约 30x），在 4h/日线趋势中吃住大的波段。”
  - 时间框架权重调整：
    - 以 **4h/日线** 作为大级别趋势判定；
    - 以 **4h/1h** 作为回调上车和加仓/减仓的主要依据；
    - 5m/3m 仅用于入场/加仓微调，不再驱动频繁完全平仓。
  - 信号打分体系重构：
    - 大级别趋势最高 6 分（4h+1h 共振）；
    - 回调质量最高 2 分（围绕 4h EMA20 ± 0.5×ATR14_4h）；
    - 成交量与订单流 1 分（基于 4h 相对成交量）；
    - 拥挤度 1 分（LS Ratio 在 [1,3] 时加分，>4 时在仓位管理阶段强制减半）。
- 风险与仓位管理描述更新：
-    - （第一阶段）单笔理论风险上限从 3% 提升到 **10%** 净值、所有新开仓合计风险通常控制在 **20%** 净值以内；
-    - （第二阶段，本次更新）在 prompt 中明确了极端高杠杆模式下的 risk_usd 档位：试探仓约 5–10U、普通好机会约 20–25U、A+ 级机会约 40–50U，对应后端单笔约 50% / 50USDT 上限与全局约 50% 净值上限；
-    - 建议持仓时间从 “30–60 分钟” 放宽到 “8–24 小时，趋势好时 2–7 天”；
-    - 明确允许在趋势中承受 50–70% 浮盈回吐，以换取吃住更大趋势段。

---

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
