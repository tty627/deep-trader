# Deep Trader - AI 加密货币交易系统

一个基于 AI 的全自动加密货币交易系统，支持实盘交易和模拟交易，带有 Web 监控界面。

## ✨ 功能特性

- 🤖 **AI 驱动决策**：使用大语言模型（DeepSeek/GPT 等）进行交易决策
- 📊 **多周期分析**：结合 3m、30m、1h、4h 多个时间周期的技术指标
- 🎯 **智能风控**：
  - 风险回报比 ≥ 3:1 硬约束
  - 单笔风险上限 1%-3% 账户净值
  - 动态杠杆管理（BTC/ETH 最高 10x，山寨币 5x）
  - 保证金使用率 ≤ 90%
  - **动态仓位管理**：支持 `position_percent`（按可用余额百分比开仓）和自动降级机制（仓位过大时自动调整为合规上限）
- 🧪 **双模式运行**：
  - **模拟模式**：使用虚拟资金测试策略
  - **实盘模式**：连接币安合约交易
- 📈 **实时监控**：Web 界面实时显示账户、持仓、决策思维链
- 🔄 **历史追踪**：记录所有交易历史和盈亏统计
- 🧬 **自适应策略**：基于夏普比率动态调整交易频率和风险偏好

## 🏗️ 技术架构

- **语言**：Go 1.22
- **交易所**：币安合约 (Binance Futures)
- **AI 模型**：支持 OpenAI API 兼容接口（DeepSeek、GPT、Claude 等）
- **Web 监控**：内置 HTTP 服务器（端口 8080）

## 📦 安装

### 前置要求

- Go 1.22 或更高版本
- （实盘模式）币安合约账户 + API Key

### 编译

```bash
go build -o deep_trader
```

## ⚙️ 配置

### 1. 创建配置文件

复制示例配置文件：

```bash
cp config.local.example.json config.local.json
```

### 2. 编辑配置

在 `config.local.json` 中填写以下配置：

```json
{
  "ai_api_key": "your_ai_api_key_here",
  "ai_api_url": "https://api.deepseek.com/v1/chat/completions",
  "ai_model": "deepseek-chat",
  "loop_interval_seconds": 150,
  "trading_symbols": ["BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "DOGEUSDT"],
  "btc_eth_leverage": 10,
  "altcoin_leverage": 5,
  "binance_api_key": "your_binance_api_key_here",
  "binance_secret_key": "your_binance_secret_key_here",
  "binance_proxy_url": "http://127.0.0.1:7890"
}
```

#### 配置说明

| 配置项 | 说明 | 必填 |
|--------|------|------|
| `ai_api_key` | AI API 密钥 | ✅ |
| `ai_api_url` | AI API 地址 | 可选（默认 DeepSeek） |
| `ai_model` | AI 模型名称 | 可选（默认 deepseek-chat） |
| `loop_interval_seconds` | 决策循环周期（秒），建议 30-900 | 可选（默认 150） |
| `trading_symbols` | 交易币种列表 | 可选（默认 5 个主流币） |
| `btc_eth_leverage` | BTC/ETH 最大杠杆 | 可选（默认 10） |
| `altcoin_leverage` | 山寨币最大杠杆 | 可选（默认 5） |
| `binance_api_key` | 币安 API Key | 实盘必填 |
| `binance_secret_key` | 币安 Secret Key | 实盘必填 |
| `binance_proxy_url` | 代理地址（如有需要） | 可选 |

### 3. 运行模式

#### 模拟模式（推荐新手）

不填写 `binance_api_key` 和 `binance_secret_key`，系统将使用模拟交易所：

```bash
./deep_trader
```

- 初始虚拟资金：1000 USDT
- 使用真实市场行情
- 所有交易仅在内存中模拟

#### 实盘模式（谨慎使用）

填写币安 API 配置后启动：

```bash
./deep_trader
```

⚠️ **风险警告**：实盘模式会真实交易，请确保：
- 已充分测试策略
- 理解并接受交易风险
- 设置合理的初始资金

## 🎮 使用指南

### 启动系统

```bash
./deep_trader
```

### 访问 Web 监控

浏览器打开：`http://localhost:8080`

Web 界面显示：
- 📊 账户净值和盈亏曲线
- 📋 当前持仓详情
- 🧠 AI 决策思维链
- 📈 市场行情数据
- 📜 历史交易记录
- ⚙️ 实时调整循环周期

### 手动设置杠杆

如需手动设置特定交易对的杠杆（仅实盘模式）：

```bash
./deep_trader set-lev BTCUSDT 5
```

### 停止系统

按 `Ctrl+C` 优雅退出

## 📊 监控指标

系统会实时计算和显示：

- **账户净值**：总权益（可用余额 + 保证金 + 未实现盈亏）
- **总盈亏百分比**：相对初始资金的收益率
- **夏普比率**：风险调整后收益指标（Sharpe Ratio）
- **保证金使用率**：当前仓位占用的保证金比例
- **持仓盈亏**：每个持仓的未实现盈亏和百分比

## 🧠 AI 决策机制

系统采用 **思维链（Chain-of-Thought）** + **结构化决策** 的两阶段输出：

1. **分析阶段**：AI 输出详细的市场分析、持仓评估、风险考量
2. **决策阶段**：输出结构化 JSON，包含具体交易指令

### 决策类型

- `open_long` / `open_short`：开多/开空
- `close_long` / `close_short`：平多/平空
- `update_stop_loss` / `update_take_profit`：调整止损/止盈
- `partial_close`：部分平仓
- `hold` / `wait`：持仓观望/空仓观望

### 风控约束

- 风险回报比通常 ≥ 2:1（更偏好 2.5–3:1）
- 单笔风险不超过账户净值约 3%
- 开仓金额建议 ≥ 12 USDT
- 杠杆严格限制（BTC/ETH ≤ 10x，山寨币 ≤ 5x）

## 📁 项目结构

```
deep_trader/
├── main.go                 # 主程序入口
├── config.go               # 配置加载
├── types.go                # 数据结构定义
├── brain.go                # AI 决策引擎
├── risk.go                 # 风控验证
├── exchange_interface.go   # 交易所接口
├── binance_exchange.go     # 币安实盘实现
├── simulated_exchange.go   # 模拟交易所
├── backtest_exchange.go    # 回测引擎
├── indicators.go           # 技术指标计算
├── leverage.go             # 杠杆管理
├── history.go              # 历史数据管理
├── web_server.go           # Web 监控服务
├── errors.go               # 错误定义
├── web/
│   └── index.html          # 监控界面
├── extracted_prompts.md    # AI Prompt 模板
└── OPTIMIZATION_GUIDE.md   # 代码优化指南
```

## 🔧 高级功能

### 自定义交易币种

在配置文件中修改 `trading_symbols`：

```json
{
  "trading_symbols": ["BTCUSDT", "ETHUSDT", "SOLUSDT", "ARBUSDT", "OPUSDT"]
}
```

### 调整决策频率

- 通过配置文件：修改 `loop_interval_seconds`
- 通过 Web 界面：实时调整循环周期
- 通过环境变量：`export AI_LOOP_INTERVAL_SECONDS=120`

建议范围：90-900 秒（1.5-15 分钟），避免小于 LLM 响应时间导致请求堆积。

### 板块分析

系统会自动计算板块热度：
- Major（主流币）
- Meme（迷因币）
- AI（AI 概念）
- L2（Layer 2）

## ⚠️ 风险提示

1. **加密货币交易高风险**：可能损失全部本金
2. **AI 决策非完美**：需要人工监督和干预
3. **网络和 API 风险**：可能出现延迟、错误、中断
4. **杠杆交易高风险**：放大盈利也放大亏损
5. **仅供学习参考**：不构成投资建议

## 📝 常见问题

### Q: 如何验证配置是否正确？

A: 先使用模拟模式测试，观察系统是否正常获取行情和执行决策。

### Q: AI 决策失败怎么办？

A: 检查 AI API Key 是否正确、网络是否通畅、API 配额是否充足。

### Q: 如何优化策略表现？

A: 
- 调整循环周期（更长周期 = 更稳健）
- 修改 `extracted_prompts.md` 中的 Prompt
- 观察夏普比率，系统会自动调整

### Q: 可以同时运行多个实例吗？

A: 不建议。多实例可能导致订单冲突和风控失效。

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

本项目仅供学习和研究使用。

---

**免责声明**：本软件按"原样"提供，不提供任何明示或暗示的保证。使用本软件进行交易的任何损失，作者不承担责任。
