package main

import (
	"sync"
	"time"
)

// HistoryTracker 历史数据追踪器 (用于计算 OI 变动等)
type HistoryTracker struct {
	mu        sync.RWMutex
	OISnapshots map[string][]OISnapshot // Symbol -> 时间序列
}

type OISnapshot struct {
	Timestamp time.Time
	Value     float64
}

// TradeHistoryManager 管理历史交易记录
type TradeHistoryManager struct {
	mu      sync.RWMutex
	history []TradeRecord
}

// NewTradeHistoryManager 创建新的历史管理器
func NewTradeHistoryManager() *TradeHistoryManager {
	return &TradeHistoryManager{
		history: make([]TradeRecord, 0),
	}
}

// AddRecord 添加一条交易记录
func (m *TradeHistoryManager) AddRecord(record TradeRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 插入到开头，保持最新的在最前
	m.history = append([]TradeRecord{record}, m.history...)
	
	// 限制只保留最近 100 条，避免内存无限膨胀
	if len(m.history) > 100 {
		m.history = m.history[:100]
	}
}

// GetHistory 获取当前历史记录的副本
func (m *TradeHistoryManager) GetHistory() []TradeRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	// Return a copy
	result := make([]TradeRecord, len(m.history))
	copy(result, m.history)
	return result
}

var tracker = &HistoryTracker{
	OISnapshots: make(map[string][]OISnapshot),
}

// RecordOI 记录一次 OI
func (h *HistoryTracker) RecordOI(symbol string, value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	h.OISnapshots[symbol] = append(h.OISnapshots[symbol], OISnapshot{
		Timestamp: now,
		Value:     value,
	})

	// 清理超过 5 小时的数据，防止内存泄漏
	cutoff := now.Add(-5 * time.Hour)
	filtered := h.OISnapshots[symbol][:0]
	for _, s := range h.OISnapshots[symbol] {
		if s.Timestamp.After(cutoff) {
			filtered = append(filtered, s)
		}
	}
	h.OISnapshots[symbol] = filtered
}

// GetOIChange 获取 OI 变动百分比
func (h *HistoryTracker) GetOIChange(symbol string, duration time.Duration) float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	snaps, ok := h.OISnapshots[symbol]
	if !ok || len(snaps) < 2 {
		return 0.0
	}

	current := snaps[len(snaps)-1]
	targetTime := current.Timestamp.Add(-duration)

	// 寻找最接近 targetTime 的快照
	var bestMatch *OISnapshot
	minDiff := time.Hour * 100 // 初始大值

	for i := len(snaps) - 2; i >= 0; i-- {
		s := &snaps[i]
		diff := s.Timestamp.Sub(targetTime)
		if diff < 0 { diff = -diff }

		if diff < minDiff {
			minDiff = diff
			bestMatch = s
		} else {
			// 因为是倒序，一旦 diff 变大说明已经远离了
			break 
		}
	}

	if bestMatch != nil && bestMatch.Value > 0 {
		return (current.Value - bestMatch.Value) / bestMatch.Value * 100
	}

	return 0.0
}
