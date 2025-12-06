package main

import (
	"sync"
)

// LeverageManager 管理全局杠杆配置，支持并发读写
type LeverageManager struct {
	mu            sync.RWMutex
	DefaultBTCETH int
	DefaultAlt    int
	Specific      map[string]int
}

// NewLeverageManager 创建新的杠杆管理器
func NewLeverageManager(defaultBTCETH, defaultAlt int) *LeverageManager {
	return &LeverageManager{
		DefaultBTCETH: defaultBTCETH,
		DefaultAlt:    defaultAlt,
		Specific:      make(map[string]int),
	}
}

// Get 获取指定币种的最大杠杆
func (l *LeverageManager) Get(symbol string) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	
	if val, ok := l.Specific[symbol]; ok {
		return val
	}
	
	if symbol == "BTCUSDT" || symbol == "ETHUSDT" {
		return l.DefaultBTCETH
	}
	return l.DefaultAlt
}

// Set 设置指定币种的杠杆
func (l *LeverageManager) Set(symbol string, val int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Specific[symbol] = val
}

// GetAllSpecific 获取所有特殊配置（用于上下文传递）
func (l *LeverageManager) GetAllSpecific() map[string]int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	
	copy := make(map[string]int)
	for k, v := range l.Specific {
		copy[k] = v
	}
	return copy
}
