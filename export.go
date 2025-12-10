package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ExportFormat 导出格式
type ExportFormat string

const (
	ExportCSV  ExportFormat = "csv"
	ExportJSON ExportFormat = "json"
)

// ExportOptions 导出选项
type ExportOptions struct {
	Format    ExportFormat `json:"format"`
	StartTime *time.Time   `json:"start_time,omitempty"`
	EndTime   *time.Time   `json:"end_time,omitempty"`
	OutputDir string       `json:"output_dir"`
}

// Exporter 数据导出器
type Exporter struct {
	storage *Storage
}

// NewExporter 创建导出器
func NewExporter(storage *Storage) *Exporter {
	return &Exporter{storage: storage}
}

// ExportEquityHistory 导出净值曲线
func (e *Exporter) ExportEquityHistory(opts ExportOptions) (string, error) {
	var snapshots []EquitySnapshot
	var err error

	if opts.StartTime != nil && opts.EndTime != nil {
		snapshots, err = e.storage.GetEquityHistoryByTimeRange(*opts.StartTime, *opts.EndTime)
	} else {
		snapshots, err = e.storage.GetEquityHistory(0) // 获取全部
	}

	if err != nil {
		return "", fmt.Errorf("get equity history: %w", err)
	}

	if len(snapshots) == 0 {
		return "", fmt.Errorf("no equity data to export")
	}

	filename := e.generateFilename("equity", opts.Format)
	filepath := filepath.Join(opts.OutputDir, filename)

	switch opts.Format {
	case ExportCSV:
		err = e.exportEquityToCSV(snapshots, filepath)
	case ExportJSON:
		err = e.exportToJSON(snapshots, filepath)
	default:
		err = fmt.Errorf("unsupported format: %s", opts.Format)
	}

	if err != nil {
		return "", err
	}

	return filepath, nil
}

// ExportTradeRecords 导出交易记录
func (e *Exporter) ExportTradeRecords(opts ExportOptions) (string, error) {
	records, _, err := e.storage.GetTradeRecords(0, 0) // 获取全部
	if err != nil {
		return "", fmt.Errorf("get trade records: %w", err)
	}

	if len(records) == 0 {
		return "", fmt.Errorf("no trade records to export")
	}

	// 时间范围筛选
	if opts.StartTime != nil || opts.EndTime != nil {
		filtered := make([]TradeRecord, 0)
		for _, r := range records {
			t, err := time.Parse("15:04:05", r.Time)
			if err != nil {
				filtered = append(filtered, r) // 无法解析时间的保留
				continue
			}

			if opts.StartTime != nil && t.Before(*opts.StartTime) {
				continue
			}
			if opts.EndTime != nil && t.After(*opts.EndTime) {
				continue
			}
			filtered = append(filtered, r)
		}
		records = filtered
	}

	filename := e.generateFilename("trades", opts.Format)
	filepath := filepath.Join(opts.OutputDir, filename)

	switch opts.Format {
	case ExportCSV:
		err = e.exportTradesToCSV(records, filepath)
	case ExportJSON:
		err = e.exportToJSON(records, filepath)
	default:
		err = fmt.Errorf("unsupported format: %s", opts.Format)
	}

	if err != nil {
		return "", err
	}

	return filepath, nil
}

// ExportAIDecisions 导出AI决策记录
func (e *Exporter) ExportAIDecisions(opts ExportOptions, limit int) (string, error) {
	if limit <= 0 {
		limit = 1000
	}

	decisions, err := e.storage.GetAIDecisions(limit)
	if err != nil {
		return "", fmt.Errorf("get AI decisions: %w", err)
	}

	if len(decisions) == 0 {
		return "", fmt.Errorf("no AI decisions to export")
	}

	filename := e.generateFilename("decisions", opts.Format)
	filepath := filepath.Join(opts.OutputDir, filename)

	switch opts.Format {
	case ExportCSV:
		err = e.exportDecisionsToCSV(decisions, filepath)
	case ExportJSON:
		err = e.exportToJSON(decisions, filepath)
	default:
		err = fmt.Errorf("unsupported format: %s", opts.Format)
	}

	if err != nil {
		return "", err
	}

	return filepath, nil
}

// ExportFullReport 导出完整报告（包含所有数据）
func (e *Exporter) ExportFullReport(opts ExportOptions) (string, error) {
	report := make(map[string]interface{})

	// 净值历史
	if snapshots, err := e.storage.GetEquityHistory(1000); err == nil {
		report["equity_history"] = snapshots
	}

	// 交易记录
	if records, _, err := e.storage.GetTradeRecords(500, 0); err == nil {
		report["trade_records"] = records
	}

	// 交易统计
	if stats, err := e.storage.GetTradeStats(); err == nil {
		report["trade_stats"] = stats
	}

	// AI决策
	if decisions, err := e.storage.GetAIDecisions(100); err == nil {
		report["ai_decisions"] = decisions
	}

	report["generated_at"] = time.Now()

	filename := e.generateFilename("full_report", ExportJSON)
	filepath := filepath.Join(opts.OutputDir, filename)

	if err := e.exportToJSON(report, filepath); err != nil {
		return "", err
	}

	return filepath, nil
}

// exportEquityToCSV 导出净值到CSV
func (e *Exporter) exportEquityToCSV(snapshots []EquitySnapshot, filepath string) error {
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	// 写入 BOM 以支持 Excel 正确识别 UTF-8
	file.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// 写入表头
	headers := []string{"timestamp", "equity", "pnl", "pnl_pct"}
	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// 写入数据
	for _, s := range snapshots {
		row := []string{
			s.Timestamp.Format(time.RFC3339),
			strconv.FormatFloat(s.Equity, 'f', 2, 64),
			strconv.FormatFloat(s.PnL, 'f', 2, 64),
			strconv.FormatFloat(s.PnLPct, 'f', 4, 64),
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}

	return nil
}

// exportTradesToCSV 导出交易记录到CSV
func (e *Exporter) exportTradesToCSV(records []TradeRecord, filepath string) error {
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	file.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(file)
	defer writer.Flush()

	headers := []string{"time", "symbol", "side", "action", "entry_price", "exit_price", "quantity", "pnl", "pnl_pct", "reason"}
	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	for _, r := range records {
		row := []string{
			r.Time,
			r.Symbol,
			r.Side,
			r.Action,
			strconv.FormatFloat(r.EntryPrice, 'f', 8, 64),
			strconv.FormatFloat(r.ExitPrice, 'f', 8, 64),
			strconv.FormatFloat(r.Quantity, 'f', 8, 64),
			strconv.FormatFloat(r.PnL, 'f', 2, 64),
			strconv.FormatFloat(r.PnLPct, 'f', 2, 64),
			r.Reason,
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}

	return nil
}

// exportDecisionsToCSV 导出AI决策到CSV
func (e *Exporter) exportDecisionsToCSV(decisions []AIDecisionRecord, filepath string) error {
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	file.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(file)
	defer writer.Flush()

	headers := []string{"id", "timestamp", "cot_trace", "decisions_json"}
	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	for _, d := range decisions {
		row := []string{
			strconv.FormatInt(d.ID, 10),
			d.Timestamp.Format(time.RFC3339),
			d.CoTTrace,
			d.DecisionsJSON,
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}

	return nil
}

// exportToJSON 导出到JSON
func (e *Exporter) exportToJSON(data interface{}, filepath string) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	if err := os.WriteFile(filepath, jsonData, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}

// generateFilename 生成文件名
func (e *Exporter) generateFilename(prefix string, format ExportFormat) string {
	timestamp := time.Now().Format("20060102_150405")
	ext := "json"
	if format == ExportCSV {
		ext = "csv"
	}
	return fmt.Sprintf("%s_%s.%s", prefix, timestamp, ext)
}

// ===== 便捷导出函数 =====

// ExportEquityToCSV 快捷导出净值到CSV
func ExportEquityToCSV(outputDir string) (string, error) {
	storage := GetStorage()
	if storage == nil {
		return "", fmt.Errorf("storage not initialized")
	}

	if outputDir == "" {
		outputDir = "exports"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	exporter := NewExporter(storage)
	return exporter.ExportEquityHistory(ExportOptions{
		Format:    ExportCSV,
		OutputDir: outputDir,
	})
}

// ExportTradesToCSV 快捷导出交易到CSV
func ExportTradesToCSV(outputDir string) (string, error) {
	storage := GetStorage()
	if storage == nil {
		return "", fmt.Errorf("storage not initialized")
	}

	if outputDir == "" {
		outputDir = "exports"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	exporter := NewExporter(storage)
	return exporter.ExportTradeRecords(ExportOptions{
		Format:    ExportCSV,
		OutputDir: outputDir,
	})
}

// ExportFullReportJSON 快捷导出完整报告
func ExportFullReportJSON(outputDir string) (string, error) {
	storage := GetStorage()
	if storage == nil {
		return "", fmt.Errorf("storage not initialized")
	}

	if outputDir == "" {
		outputDir = "exports"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	exporter := NewExporter(storage)
	return exporter.ExportFullReport(ExportOptions{
		Format:    ExportJSON,
		OutputDir: outputDir,
	})
}
