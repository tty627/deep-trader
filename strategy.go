package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// RiskConfig 风险配置
type RiskConfig struct {
	MaxRiskPerTrade     float64 `json:"max_risk_per_trade"`      // 单笔最大风险比例
	MaxTotalRisk        float64 `json:"max_total_risk"`          // 总风险上限
	MinRiskRewardRatio  float64 `json:"min_risk_reward_ratio"`   // 最小风险回报比
	FixedLeverage       int     `json:"fixed_leverage"`          // 固定杠杆
	MaxMarginUsage      float64 `json:"max_margin_usage"`        // 最大保证金使用率
	StopLossATRMultiple float64 `json:"stop_loss_atr_multiple"`  // 止损 ATR 倍数
}

// Strategy 策略模板
type Strategy struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	PromptFile  string     `json:"prompt_file"`   // prompt 模板文件路径
	Symbols     []string   `json:"symbols"`       // 可选：覆盖默认交易对
	RiskParams  RiskConfig `json:"risk_params"`
	Active      bool       `json:"active"`
}

// DefaultStrategies 内置策略列表
var DefaultStrategies = []Strategy{
	{
		Name:        "balanced",
		Description: "中等风险，稳健交易",
		PromptFile:  "balanced.md",
		RiskParams: RiskConfig{
			MaxRiskPerTrade:     0.25,
			MaxTotalRisk:        0.40,
			MinRiskRewardRatio:  2.0,
			FixedLeverage:       15,
			MaxMarginUsage:      0.70,
			StopLossATRMultiple: 1.8,
		},
	},
	{
		Name:        "aggressive",
		Description: "高风险高收益",
		PromptFile:  "aggressive.md",
		RiskParams: RiskConfig{
			MaxRiskPerTrade:     0.50,
			MaxTotalRisk:        0.50,
			MinRiskRewardRatio:  1.8,
			FixedLeverage:       30,
			MaxMarginUsage:      0.95,
			StopLossATRMultiple: 1.5,
		},
	},
	{
		Name:        "conservative",
		Description: "低风险稳定收益",
		PromptFile:  "conservative.md",
		RiskParams: RiskConfig{
			MaxRiskPerTrade:     0.10,
			MaxTotalRisk:        0.30,
			MinRiskRewardRatio:  2.5,
			FixedLeverage:       5,
			MaxMarginUsage:      0.50,
			StopLossATRMultiple: 2.0,
		},
	},
	{
		Name:        "scalping",
		Description: "超短线快进快出",
		PromptFile:  "scalping.md",
		RiskParams: RiskConfig{
			MaxRiskPerTrade:     0.05,
			MaxTotalRisk:        0.20,
			MinRiskRewardRatio:  1.5,
			FixedLeverage:       20,
			MaxMarginUsage:      0.30,
			StopLossATRMultiple: 0.8,
		},
	},
}

// StrategyManager 策略管理器
type StrategyManager struct {
	mu              sync.RWMutex
	strategies      map[string]*Strategy
	activeStrategy  string
	strategiesDir   string
	defaultPrompt   string // 默认 prompt 文件路径
}

// NewStrategyManager 创建策略管理器
func NewStrategyManager(strategiesDir string) *StrategyManager {
	sm := &StrategyManager{
		strategies:    make(map[string]*Strategy),
		strategiesDir: strategiesDir,
		defaultPrompt: "extracted_prompts.md",
	}

	// 初始化默认策略
	for i := range DefaultStrategies {
		s := &DefaultStrategies[i]
		sm.strategies[s.Name] = s
	}

	// 设置默认活跃策略
	sm.activeStrategy = "balanced"

	// 确保策略目录存在
	if err := os.MkdirAll(strategiesDir, 0755); err != nil {
		log.Printf("Warning: Failed to create strategies directory: %v", err)
	}

	return sm
}

// GetStrategy 获取指定策略
func (sm *StrategyManager) GetStrategy(name string) (*Strategy, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	s, ok := sm.strategies[name]
	return s, ok
}

// GetActiveStrategy 获取当前活跃策略
func (sm *StrategyManager) GetActiveStrategy() *Strategy {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if s, ok := sm.strategies[sm.activeStrategy]; ok {
		return s
	}

	// 返回默认策略
	if s, ok := sm.strategies["balanced"]; ok {
		return s
	}

	return nil
}

// SetActiveStrategy 设置活跃策略
func (sm *StrategyManager) SetActiveStrategy(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.strategies[name]; !ok {
		return fmt.Errorf("strategy not found: %s", name)
	}

	sm.activeStrategy = name
	log.Printf("✅ 切换到策略: %s", name)
	return nil
}

// GetActiveStrategyName 获取当前活跃策略名称
func (sm *StrategyManager) GetActiveStrategyName() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.activeStrategy
}

// ListStrategies 列出所有策略
func (sm *StrategyManager) ListStrategies() []Strategy {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]Strategy, 0, len(sm.strategies))
	for _, s := range sm.strategies {
		sCopy := *s
		sCopy.Active = (s.Name == sm.activeStrategy)
		result = append(result, sCopy)
	}
	return result
}

// AddStrategy 添加自定义策略
func (sm *StrategyManager) AddStrategy(s Strategy) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s.Name == "" {
		return fmt.Errorf("strategy name cannot be empty")
	}

	sm.strategies[s.Name] = &s
	log.Printf("✅ 添加策略: %s", s.Name)
	return nil
}

// RemoveStrategy 删除策略
func (sm *StrategyManager) RemoveStrategy(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 不允许删除当前活跃策略
	if name == sm.activeStrategy {
		return fmt.Errorf("cannot remove active strategy")
	}

	// 不允许删除内置策略
	for _, ds := range DefaultStrategies {
		if ds.Name == name {
			return fmt.Errorf("cannot remove built-in strategy: %s", name)
		}
	}

	delete(sm.strategies, name)
	log.Printf("✅ 删除策略: %s", name)
	return nil
}

// GetPromptContent 获取策略的 prompt 内容
func (sm *StrategyManager) GetPromptContent() (string, error) {
	strategy := sm.GetActiveStrategy()
	if strategy == nil || strategy.PromptFile == "" {
		// 使用默认 prompt
		return sm.readPromptFile(sm.defaultPrompt)
	}

	// 尝试读取策略专用 prompt
	promptPath := filepath.Join(sm.strategiesDir, strategy.PromptFile)
	content, err := sm.readPromptFile(promptPath)
	if err != nil {
		log.Printf("⚠️ 无法读取策略 prompt %s: %v，回退到默认", promptPath, err)
		// 回退到默认 prompt
		return sm.readPromptFile(sm.defaultPrompt)
	}
	return content, nil
}

func (sm *StrategyManager) readPromptFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// GetRiskConfig 获取当前策略的风险配置
func (sm *StrategyManager) GetRiskConfig() RiskConfig {
	strategy := sm.GetActiveStrategy()
	if strategy == nil {
		// 返回默认配置
		return RiskConfig{
			MaxRiskPerTrade:     0.50,
			MaxTotalRisk:        0.50,
			MinRiskRewardRatio:  1.8,
			FixedLeverage:       30,
			MaxMarginUsage:      0.95,
			StopLossATRMultiple: 1.5,
		}
	}
	return strategy.RiskParams
}

// GetSymbols 获取当前策略的交易对列表
func (sm *StrategyManager) GetSymbols(defaultSymbols []string) []string {
	strategy := sm.GetActiveStrategy()
	if strategy == nil || len(strategy.Symbols) == 0 {
		return defaultSymbols
	}
	return strategy.Symbols
}

// 全局策略管理器
var globalStrategyManager *StrategyManager

// InitGlobalStrategyManager 初始化全局策略管理器
func InitGlobalStrategyManager(strategiesDir string) {
	globalStrategyManager = NewStrategyManager(strategiesDir)
}

// GetStrategyManager 获取全局策略管理器
func GetStrategyManager() *StrategyManager {
	return globalStrategyManager
}
