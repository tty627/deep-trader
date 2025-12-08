package main

import (
	"encoding/json"
	"log"
	"os"
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
	mu           sync.RWMutex
	history      []TradeRecord
	filePath     string // 持久化文件路径
	maxInMemory  int    // 内存中保留的最大数量
	maxInFile    int    // 文件中保留的最大数量
}

// NewTradeHistoryManager 创建新的历史管理器
func NewTradeHistoryManager() *TradeHistoryManager {
	m := &TradeHistoryManager{
		history:     make([]TradeRecord, 0),
		filePath:    "trade_history.json",
		maxInMemory: 100,
		maxInFile:   500, // 文件中保留最近500条
	}
	// 启动时从文件加载历史记录
	m.loadFromFile()
	return m
}

// AddRecord 添加一条交易记录（带简单去重）
func (m *TradeHistoryManager) AddRecord(record TradeRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 简单去重：如果已有完全相同的记录，则不再重复插入
	for _, r := range m.history {
		if r.Time == record.Time && r.Symbol == record.Symbol && r.Side == record.Side &&
			r.Action == record.Action && r.EntryPrice == record.EntryPrice && r.ExitPrice == record.ExitPrice &&
			r.Quantity == record.Quantity && r.PnL == record.PnL {
			return
		}
	}

	// 插入到开头，保持最新的在最前
	m.history = append([]TradeRecord{record}, m.history...)
	
	// 限制只保留最近 maxInMemory 条，避免内存无限膨胀
	if len(m.history) > m.maxInMemory {
		m.history = m.history[:m.maxInMemory]
	}
	
	// 异步保存到文件
	go m.saveToFile()
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

// loadFromFile 从文件加载历史记录
func (m *TradeHistoryManager) loadFromFile() {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("⚠️ 加载历史记录失败: %v", err)
		}
		return
	}
	
	var records []TradeRecord
	if err := json.Unmarshal(data, &records); err != nil {
		log.Printf("⚠️ 解析历史记录失败: %v", err)
		return
	}
	
	// 只加载最近 maxInMemory 条到内存
	if len(records) > m.maxInMemory {
		m.history = records[:m.maxInMemory]
	} else {
		m.history = records
	}
	log.Printf("✅ 已加载 %d 条历史交易记录", len(m.history))
}

// saveToFile 保存历史记录到文件
func (m *TradeHistoryManager) saveToFile() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	// 限制文件中的记录数量
	toSave := m.history
	if len(toSave) > m.maxInFile {
		toSave = toSave[:m.maxInFile]
	}
	
	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		log.Printf("⚠️ 序列化历史记录失败: %v", err)
		return
	}
	
	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		log.Printf("⚠️ 保存历史记录失败: %v", err)
	}
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
