package main

import (
    "encoding/json"
    "fmt"
    "os"
    "strconv"
)

// Config 用于本地配置 API Key 等敏感信息
// 建议复制一份 config.local.example.json 为 config.local.json 然后填写
// 该文件一般应加入 .gitignore，避免提交到仓库

type Config struct {
    // AI 调用相关
    AIAPIKey string `json:"ai_api_key"`
    AIAPIURL string `json:"ai_api_url"`
    AIModel  string `json:"ai_model"`

    // AI 循环周期（秒），用于控制主循环的休眠时间
    // 建议范围：30 - 900 秒。默认 150 秒（2.5 分钟）
    LoopIntervalSeconds int `json:"loop_interval_seconds"`

    // 交易配置
    TradingSymbols  []string `json:"trading_symbols"`   // 交易币种列表
    BTCETHLeverage  int      `json:"btc_eth_leverage"`  // BTC/ETH 最大杠杆
    AltcoinLeverage int      `json:"altcoin_leverage"`  // 山寨币最大杠杆

    // 币安实盘相关（可选，不填则使用模拟盘）
    BinanceAPIKey    string `json:"binance_api_key"`
    BinanceSecretKey string `json:"binance_secret_key"`
    BinanceProxyURL  string `json:"binance_proxy_url"`
}

// LoadConfig 先尝试从 config.local.json 读取；如果没有该文件，则退回到环境变量
func LoadConfig() (*Config, error) {
    cfg := &Config{}

    // 1. 优先从本地文件读取
    if data, err := os.ReadFile("config.local.json"); err == nil {
        if err := json.Unmarshal(data, cfg); err != nil {
            return nil, fmt.Errorf("解析 config.local.json 失败: %w", err)
        }
    }

    // 2. 如果文件中没填，继续用环境变量兜底
    if cfg.AIAPIKey == "" {
        cfg.AIAPIKey = os.Getenv("AI_API_KEY")
    }
    if cfg.AIAPIURL == "" {
        cfg.AIAPIURL = os.Getenv("AI_API_URL")
    }
    if cfg.AIModel == "" {
        cfg.AIModel = os.Getenv("AI_MODEL")
    }

    if cfg.BinanceAPIKey == "" {
        cfg.BinanceAPIKey = os.Getenv("BINANCE_API_KEY")
    }
    if cfg.BinanceSecretKey == "" {
        cfg.BinanceSecretKey = os.Getenv("BINANCE_SECRET_KEY")
    }
    if cfg.BinanceProxyURL == "" {
        cfg.BinanceProxyURL = os.Getenv("BINANCE_PROXY_URL")
    }

    // 循环周期：支持环境变量 AI_LOOP_INTERVAL_SECONDS 覆盖
    if cfg.LoopIntervalSeconds == 0 {
        if v := os.Getenv("AI_LOOP_INTERVAL_SECONDS"); v != "" {
            if sec, err := strconv.Atoi(v); err == nil && sec > 0 {
                cfg.LoopIntervalSeconds = sec
            }
        }
    }
    // 默认值 150 秒（2.5 分钟）
    if cfg.LoopIntervalSeconds <= 0 {
        cfg.LoopIntervalSeconds = 150
    }

    // 交易配置默认值
    if len(cfg.TradingSymbols) == 0 {
        cfg.TradingSymbols = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "DOGEUSDT"}
    }
    if cfg.BTCETHLeverage <= 0 {
        cfg.BTCETHLeverage = 10
    }
    if cfg.AltcoinLeverage <= 0 {
        cfg.AltcoinLeverage = 5
    }

    // 至少要有 AIAPIKey
    if cfg.AIAPIKey == "" {
        return nil, fmt.Errorf("请在 config.local.json 或环境变量中配置 AI_API_KEY")
    }

    // 给出示例默认值（例如 DeepSeek），仅当没配时才使用
    if cfg.AIAPIURL == "" {
        cfg.AIAPIURL = "https://api.deepseek.com/v1/chat/completions"
    }
    if cfg.AIModel == "" {
        cfg.AIModel = "deepseek-chat"
    }

    return cfg, nil
}
