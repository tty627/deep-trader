package main

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

// AIMode AI运行模式
type AIMode string

const (
	AIModePrimary AIMode = "primary" // 只用第一个模型
	AIModeVote    AIMode = "vote"    // 多模型投票
	AIModeCompare AIMode = "compare" // 对比模式（不执行）
)

// AIModelConfig AI模型配置
type AIModelConfig struct {
	Name    string  `json:"name"`
	APIKey  string  `json:"api_key"`
	APIURL  string  `json:"api_url"`
	Model   string  `json:"model"`
	Weight  float64 `json:"weight"`  // 投票权重
	Enabled bool    `json:"enabled"`
}

// AIModelsConfig 多模型配置
type AIModelsConfig struct {
	Models []AIModelConfig `json:"ai_models"`
	Mode   AIMode          `json:"ai_mode"`
}

// ModelDecision 单个模型的决策结果
type ModelDecision struct {
	ModelName  string        `json:"model_name"`
	Decision   *FullDecision `json:"decision"`
	Error      error         `json:"error,omitempty"`
	Duration   time.Duration `json:"duration"`
}

// AIManager 多AI模型管理器
type AIManager struct {
	mu      sync.RWMutex
	brains  map[string]*AIBrain
	configs map[string]AIModelConfig
	mode    AIMode
}

// NewAIManager 创建AI管理器
func NewAIManager(config AIModelsConfig, proxyURL string) *AIManager {
	am := &AIManager{
		brains:  make(map[string]*AIBrain),
		configs: make(map[string]AIModelConfig),
		mode:    config.Mode,
	}

	if am.mode == "" {
		am.mode = AIModePrimary
	}

	for _, mc := range config.Models {
		if !mc.Enabled {
			continue
		}
		am.configs[mc.Name] = mc
		am.brains[mc.Name] = NewAIBrain(mc.APIKey, mc.APIURL, mc.Model, proxyURL)
		log.Printf("✅ 加载AI模型: %s (%s)", mc.Name, mc.Model)
	}

	return am
}

// GetMode 获取当前运行模式
func (am *AIManager) GetMode() AIMode {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.mode
}

// SetMode 设置运行模式
func (am *AIManager) SetMode(mode AIMode) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.mode = mode
	log.Printf("✅ AI模式切换为: %s", mode)
}

// GetDecision 根据模式获取决策
func (am *AIManager) GetDecision(ctx *Context) (*FullDecision, []ModelDecision, error) {
	am.mu.RLock()
	mode := am.mode
	am.mu.RUnlock()

	switch mode {
	case AIModeVote:
		return am.getVoteDecision(ctx)
	case AIModeCompare:
		return am.getCompareDecision(ctx)
	default:
		return am.getPrimaryDecision(ctx)
	}
}

// getPrimaryDecision 使用主模型获取决策
func (am *AIManager) getPrimaryDecision(ctx *Context) (*FullDecision, []ModelDecision, error) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	// 找到第一个启用的模型
	var primaryBrain *AIBrain
	var primaryName string

	for name, brain := range am.brains {
		primaryBrain = brain
		primaryName = name
		break
	}

	if primaryBrain == nil {
		return nil, nil, fmt.Errorf("no AI model available")
	}

	start := time.Now()
	decision, err := primaryBrain.GetDecision(ctx)
	duration := time.Since(start)

	modelDecisions := []ModelDecision{
		{
			ModelName: primaryName,
			Decision:  decision,
			Error:     err,
			Duration:  duration,
		},
	}

	return decision, modelDecisions, err
}

// getVoteDecision 多模型投票决策
func (am *AIManager) getVoteDecision(ctx *Context) (*FullDecision, []ModelDecision, error) {
	am.mu.RLock()
	brains := make(map[string]*AIBrain)
	configs := make(map[string]AIModelConfig)
	for k, v := range am.brains {
		brains[k] = v
		configs[k] = am.configs[k]
	}
	am.mu.RUnlock()

	if len(brains) == 0 {
		return nil, nil, fmt.Errorf("no AI model available")
	}

	// 并行调用所有模型
	var wg sync.WaitGroup
	resultCh := make(chan ModelDecision, len(brains))

	for name, brain := range brains {
		wg.Add(1)
		go func(n string, b *AIBrain) {
			defer wg.Done()
			start := time.Now()
			decision, err := b.GetDecision(ctx)
			resultCh <- ModelDecision{
				ModelName: n,
				Decision:  decision,
				Error:     err,
				Duration:  time.Since(start),
			}
		}(name, brain)
	}

	wg.Wait()
	close(resultCh)

	// 收集结果
	var modelDecisions []ModelDecision
	var validDecisions []*FullDecision
	var weights []float64

	for result := range resultCh {
		modelDecisions = append(modelDecisions, result)
		if result.Error == nil && result.Decision != nil {
			validDecisions = append(validDecisions, result.Decision)
			weights = append(weights, configs[result.ModelName].Weight)
		}
	}

	if len(validDecisions) == 0 {
		return nil, modelDecisions, fmt.Errorf("all AI models failed")
	}

	// 投票合并决策
	finalDecision := am.mergeDecisions(validDecisions, weights)

	return finalDecision, modelDecisions, nil
}

// getCompareDecision 对比模式（不执行，只记录）
func (am *AIManager) getCompareDecision(ctx *Context) (*FullDecision, []ModelDecision, error) {
	// 使用与投票相同的并行调用逻辑
	_, modelDecisions, _ := am.getVoteDecision(ctx)

	// 记录对比结果
	log.Println("📊 [AI Compare] 多模型决策对比:")
	for _, md := range modelDecisions {
		if md.Error != nil {
			log.Printf("  - %s: ERROR - %v", md.ModelName, md.Error)
		} else if md.Decision != nil && len(md.Decision.Decisions) > 0 {
			log.Printf("  - %s: %d decisions (%.2fs)", md.ModelName, len(md.Decision.Decisions), md.Duration.Seconds())
			for _, d := range md.Decision.Decisions {
				log.Printf("      %s %s (size: $%.0f)", d.Symbol, d.Action, d.PositionSizeUSD)
			}
		} else {
			log.Printf("  - %s: Wait/Hold (%.2fs)", md.ModelName, md.Duration.Seconds())
		}
	}

	// 对比模式返回第一个有效决策但标记为不执行
	for _, md := range modelDecisions {
		if md.Error == nil && md.Decision != nil {
			// 将所有决策改为观望
			for i := range md.Decision.Decisions {
				md.Decision.Decisions[i].Action = "wait"
			}
			return md.Decision, modelDecisions, nil
		}
	}

	return nil, modelDecisions, fmt.Errorf("all AI models failed in compare mode")
}

// mergeDecisions 合并多个决策（投票机制）
func (am *AIManager) mergeDecisions(decisions []*FullDecision, weights []float64) *FullDecision {
	if len(decisions) == 0 {
		return nil
	}

	if len(decisions) == 1 {
		return decisions[0]
	}

	// 统计每个 symbol 的投票
	type VoteResult struct {
		Action    string
		Weight    float64
		Decision  Decision
	}

	symbolVotes := make(map[string]map[string]*VoteResult) // symbol -> action -> vote

	for i, fd := range decisions {
		weight := 1.0
		if i < len(weights) && weights[i] > 0 {
			weight = weights[i]
		}

		for _, d := range fd.Decisions {
			if symbolVotes[d.Symbol] == nil {
				symbolVotes[d.Symbol] = make(map[string]*VoteResult)
			}

			if symbolVotes[d.Symbol][d.Action] == nil {
				symbolVotes[d.Symbol][d.Action] = &VoteResult{
					Action:   d.Action,
					Decision: d,
				}
			}
			symbolVotes[d.Symbol][d.Action].Weight += weight
		}
	}

	// 选择每个 symbol 权重最高的 action
	var finalDecisions []Decision

	for symbol, votes := range symbolVotes {
		var best *VoteResult
		for _, v := range votes {
			if best == nil || v.Weight > best.Weight {
				best = v
			}
		}

		if best != nil {
			log.Printf("📊 [Vote] %s: %s (weight: %.2f)", symbol, best.Action, best.Weight)
			finalDecisions = append(finalDecisions, best.Decision)
		}
	}

	// 按 symbol 排序
	sort.Slice(finalDecisions, func(i, j int) bool {
		return finalDecisions[i].Symbol < finalDecisions[j].Symbol
	})

	// 合并思维链
	var cotTrace string
	for i, fd := range decisions {
		if fd.CoTTrace != "" {
			if cotTrace != "" {
				cotTrace += "\n\n---\n\n"
			}
			cotTrace += fmt.Sprintf("[Model %d]\n%s", i+1, fd.CoTTrace)
		}
	}

	return &FullDecision{
		CoTTrace:     cotTrace,
		Decisions:    finalDecisions,
		Timestamp:    time.Now(),
		SystemPrompt: decisions[0].SystemPrompt,
		UserPrompt:   decisions[0].UserPrompt,
	}
}

// ListModels 列出所有模型
func (am *AIManager) ListModels() []AIModelConfig {
	am.mu.RLock()
	defer am.mu.RUnlock()

	result := make([]AIModelConfig, 0, len(am.configs))
	for _, c := range am.configs {
		// 隐藏 API Key
		c.APIKey = "***"
		result = append(result, c)
	}
	return result
}

// AddModel 添加模型
func (am *AIManager) AddModel(config AIModelConfig, proxyURL string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if config.Name == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	am.configs[config.Name] = config
	am.brains[config.Name] = NewAIBrain(config.APIKey, config.APIURL, config.Model, proxyURL)

	log.Printf("✅ 添加AI模型: %s", config.Name)
	return nil
}

// RemoveModel 删除模型
func (am *AIManager) RemoveModel(name string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if _, ok := am.brains[name]; !ok {
		return fmt.Errorf("model not found: %s", name)
	}

	delete(am.brains, name)
	delete(am.configs, name)

	log.Printf("✅ 删除AI模型: %s", name)
	return nil
}

// EnableModel 启用/禁用模型
func (am *AIManager) EnableModel(name string, enabled bool) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	if config, ok := am.configs[name]; ok {
		config.Enabled = enabled
		am.configs[name] = config
		return nil
	}

	return fmt.Errorf("model not found: %s", name)
}

// 全局AI管理器
var globalAIManager *AIManager

// InitGlobalAIManager 初始化全局AI管理器
func InitGlobalAIManager(config AIModelsConfig, proxyURL string) {
	globalAIManager = NewAIManager(config, proxyURL)
}

// GetAIManager 获取全局AI管理器
func GetAIManager() *AIManager {
	return globalAIManager
}
