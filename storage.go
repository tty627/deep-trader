package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Storage JSON文件存储层
type Storage struct {
	basePath string
	mu       sync.RWMutex
	data     *StorageData
	nextID   int64
}

// StorageData 存储的所有数据
type StorageData struct {
	EquitySnapshots []EquitySnapshot   `json:"equity_snapshots"`
	TradeRecords    []TradeRecord      `json:"trade_records"`
	AIDecisions     []AIDecisionRecord `json:"ai_decisions"`
	ConfigSnapshots []ConfigSnapshot   `json:"config_snapshots"`
}

// ConfigSnapshot 配置快照
type ConfigSnapshot struct {
	ID         int64     `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	ConfigJSON string    `json:"config_json"`
	Reason     string    `json:"reason"`
}

// EquitySnapshot 净值快照
type EquitySnapshot struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Equity    float64   `json:"equity"`
	PnL       float64   `json:"pnl"`
	PnLPct    float64   `json:"pnl_pct"`
}

// AIDecisionRecord AI决策记录
type AIDecisionRecord struct {
	ID            int64     `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	CoTTrace      string    `json:"cot_trace"`
	DecisionsJSON string    `json:"decisions_json"`
	SystemPrompt  string    `json:"system_prompt"`
	UserPrompt    string    `json:"user_prompt"`
}

// NewStorage 创建存储实例
func NewStorage(dbPath string) (*Storage, error) {
	if dbPath == "" {
		dbPath = "data/storage.json"
	}

	// 确保目录存在
	dir := filepath.Dir(dbPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}

	s := &Storage{
		basePath: dbPath,
		data:     &StorageData{},
		nextID:   1,
	}

	// 尝试加载已有数据
	if err := s.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("load existing data: %w", err)
		}
		// 文件不存在，使用空数据
		log.Printf("📁 创建新的存储文件: %s", dbPath)
	} else {
		log.Printf("✅ 已加载存储数据: %s", dbPath)
	}

	// 计算下一个ID
	s.calculateNextID()

	return s, nil
}

// calculateNextID 计算下一个可用ID
func (s *Storage) calculateNextID() {
	maxID := int64(0)
	for _, snap := range s.data.EquitySnapshots {
		if snap.ID > maxID {
			maxID = snap.ID
		}
	}
	for _, rec := range s.data.AIDecisions {
		if rec.ID > maxID {
			maxID = rec.ID
		}
	}
	for _, cfg := range s.data.ConfigSnapshots {
		if cfg.ID > maxID {
			maxID = cfg.ID
		}
	}
	s.nextID = maxID + 1
}

// getNextID 获取并递增ID
func (s *Storage) getNextID() int64 {
	id := s.nextID
	s.nextID++
	return id
}

// load 从文件加载数据
func (s *Storage) load() error {
	data, err := os.ReadFile(s.basePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, s.data)
}

// save 保存数据到文件
func (s *Storage) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}

	// 写入临时文件，然后重命名（原子操作）
	tmpPath := s.basePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.basePath)
}

// Close 关闭存储（保存数据）
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save()
}

// ===== 净值快照操作 =====

// SaveEquitySnapshot 保存净值快照
func (s *Storage) SaveEquitySnapshot(equity, pnl, pnlPct float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := EquitySnapshot{
		ID:        s.getNextID(),
		Timestamp: time.Now(),
		Equity:    equity,
		PnL:       pnl,
		PnLPct:    pnlPct,
	}

	s.data.EquitySnapshots = append(s.data.EquitySnapshots, snap)
	return s.save()
}

// GetEquityHistory 获取净值历史
func (s *Storage) GetEquityHistory(limit int) ([]EquitySnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 1000
	}

	// 复制并排序
	snapshots := make([]EquitySnapshot, len(s.data.EquitySnapshots))
	copy(snapshots, s.data.EquitySnapshots)

	// 按时间排序（从早到晚）
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.Before(snapshots[j].Timestamp)
	})

	// 限制数量（返回最新的）
	if len(snapshots) > limit {
		snapshots = snapshots[len(snapshots)-limit:]
	}

	return snapshots, nil
}

// GetEquityHistoryByTimeRange 按时间范围获取净值历史
func (s *Storage) GetEquityHistoryByTimeRange(start, end time.Time) ([]EquitySnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []EquitySnapshot
	for _, snap := range s.data.EquitySnapshots {
		if (snap.Timestamp.Equal(start) || snap.Timestamp.After(start)) &&
			(snap.Timestamp.Equal(end) || snap.Timestamp.Before(end)) {
			result = append(result, snap)
		}
	}

	// 按时间排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})

	return result, nil
}

// ===== 交易记录操作 =====

// SaveTradeRecord 保存交易记录
func (s *Storage) SaveTradeRecord(record TradeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data.TradeRecords = append(s.data.TradeRecords, record)
	return s.save()
}

// GetTradeRecords 获取交易记录（分页）
func (s *Storage) GetTradeRecords(limit, offset int) ([]TradeRecord, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	total := len(s.data.TradeRecords)

	// 复制并按时间倒序
	records := make([]TradeRecord, total)
	copy(records, s.data.TradeRecords)

	// 倒序（最新的在前）
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	// 应用分页
	if offset >= len(records) {
		return []TradeRecord{}, total, nil
	}
	records = records[offset:]
	if len(records) > limit {
		records = records[:limit]
	}

	return records, total, nil
}

// GetTradeRecordsBySymbol 按币种获取交易记录
func (s *Storage) GetTradeRecordsBySymbol(symbol string, limit int) ([]TradeRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	var result []TradeRecord
	for _, r := range s.data.TradeRecords {
		if r.Symbol == symbol {
			result = append(result, r)
		}
	}

	// 倒序
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	if len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// GetTradeStats 获取交易统计
func (s *Storage) GetTradeStats() (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]interface{})

	totalTrades := len(s.data.TradeRecords)
	stats["total_trades"] = totalTrades

	winTrades := 0
	loseTrades := 0
	totalPnL := 0.0
	maxWin := 0.0
	maxLoss := 0.0

	for _, r := range s.data.TradeRecords {
		totalPnL += r.PnL
		if r.PnL > 0 {
			winTrades++
			if r.PnL > maxWin {
				maxWin = r.PnL
			}
		} else if r.PnL < 0 {
			loseTrades++
			if r.PnL < maxLoss {
				maxLoss = r.PnL
			}
		}
	}

	stats["win_trades"] = winTrades
	stats["lose_trades"] = loseTrades
	stats["total_pnl"] = totalPnL
	stats["max_win"] = maxWin
	stats["max_loss"] = maxLoss

	if totalTrades > 0 {
		stats["win_rate"] = float64(winTrades) / float64(totalTrades) * 100
		stats["avg_pnl"] = totalPnL / float64(totalTrades)
	} else {
		stats["win_rate"] = 0.0
		stats["avg_pnl"] = 0.0
	}

	return stats, nil
}

// ===== AI决策记录操作 =====

// SaveAIDecision 保存AI决策记录
func (s *Storage) SaveAIDecision(decision *FullDecision) error {
	if decision == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	decisionsJSON, _ := json.Marshal(decision.Decisions)

	record := AIDecisionRecord{
		ID:            s.getNextID(),
		Timestamp:     decision.Timestamp,
		CoTTrace:      decision.CoTTrace,
		DecisionsJSON: string(decisionsJSON),
		SystemPrompt:  decision.SystemPrompt,
		UserPrompt:    decision.UserPrompt,
	}

	s.data.AIDecisions = append(s.data.AIDecisions, record)
	return s.save()
}

// GetAIDecisions 获取AI决策历史
func (s *Storage) GetAIDecisions(limit int) ([]AIDecisionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}

	// 复制并倒序
	records := make([]AIDecisionRecord, len(s.data.AIDecisions))
	copy(records, s.data.AIDecisions)

	// 按时间倒序
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})

	if len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}

// ===== 配置快照操作 =====

// SaveConfigSnapshot 保存配置快照
func (s *Storage) SaveConfigSnapshot(config interface{}, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}

	snapshot := ConfigSnapshot{
		ID:         s.getNextID(),
		Timestamp:  time.Now(),
		ConfigJSON: string(configJSON),
		Reason:     reason,
	}

	s.data.ConfigSnapshots = append(s.data.ConfigSnapshots, snapshot)
	return s.save()
}

// ===== 数据清理 =====

// CleanOldData 清理旧数据
func (s *Storage) CleanOldData(retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 90 // 默认保留90天
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	// 清理净值快照
	var newSnapshots []EquitySnapshot
	dailySnapshots := make(map[string]EquitySnapshot)
	for _, snap := range s.data.EquitySnapshots {
		if snap.Timestamp.After(cutoff) {
			newSnapshots = append(newSnapshots, snap)
		} else {
			// 保留每天第一条
			day := snap.Timestamp.Format("2006-01-02")
			if _, exists := dailySnapshots[day]; !exists {
				dailySnapshots[day] = snap
			}
		}
	}
	for _, snap := range dailySnapshots {
		newSnapshots = append(newSnapshots, snap)
	}
	s.data.EquitySnapshots = newSnapshots

	// 清理AI决策记录
	var newDecisions []AIDecisionRecord
	for _, dec := range s.data.AIDecisions {
		if dec.Timestamp.After(cutoff) {
			newDecisions = append(newDecisions, dec)
		}
	}
	s.data.AIDecisions = newDecisions

	log.Printf("✅ 已清理 %d 天前的旧数据", retentionDays)
	return s.save()
}

// GetAllTradeRecords 获取所有交易记录（用于导出）
func (s *Storage) GetAllTradeRecords() []TradeRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]TradeRecord, len(s.data.TradeRecords))
	copy(records, s.data.TradeRecords)
	return records
}

// GetAllEquitySnapshots 获取所有净值快照（用于导出）
func (s *Storage) GetAllEquitySnapshots() []EquitySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]EquitySnapshot, len(s.data.EquitySnapshots))
	copy(snapshots, s.data.EquitySnapshots)
	return snapshots
}

// 全局存储实例
var globalStorage *Storage

// InitGlobalStorage 初始化全局存储
func InitGlobalStorage(dbPath string) error {
	var err error
	globalStorage, err = NewStorage(dbPath)
	return err
}

// GetStorage 获取全局存储实例
func GetStorage() *Storage {
	return globalStorage
}
