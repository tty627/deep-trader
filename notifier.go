package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// NotifyEvent 通知事件类型
type NotifyEvent string

const (
	EventOpenPosition   NotifyEvent = "open_position"   // 开仓
	EventClosePosition  NotifyEvent = "close_position"  // 平仓
	EventStopLoss       NotifyEvent = "stop_loss"       // 止损触发
	EventTakeProfit     NotifyEvent = "take_profit"     // 止盈触发
	EventRiskRejected   NotifyEvent = "risk_rejected"   // 风控拒绝
	EventError          NotifyEvent = "error"           // 异常错误
	EventSystemStart    NotifyEvent = "system_start"    // 系统启动
	EventSystemStop     NotifyEvent = "system_stop"     // 系统停止
	EventHighDrawdown   NotifyEvent = "high_drawdown"   // 高回撤警告
)

// NotifyMessage 通知消息
type NotifyMessage struct {
	Event     NotifyEvent `json:"event"`
	Title     string      `json:"title"`
	Content   string      `json:"content"`
	Symbol    string      `json:"symbol,omitempty"`
	PnL       float64     `json:"pnl,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// Notifier 通知接口
type Notifier interface {
	Send(msg NotifyMessage) error
	Name() string
	Enabled() bool
}

// ===== Telegram 通知 =====

// TelegramConfig Telegram配置
type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// TelegramNotifier Telegram通知器
type TelegramNotifier struct {
	config TelegramConfig
	client *http.Client
}

func NewTelegramNotifier(config TelegramConfig) *TelegramNotifier {
	return &TelegramNotifier{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *TelegramNotifier) Name() string {
	return "Telegram"
}

func (t *TelegramNotifier) Enabled() bool {
	return t.config.Enabled && t.config.BotToken != "" && t.config.ChatID != ""
}

func (t *TelegramNotifier) Send(msg NotifyMessage) error {
	if !t.Enabled() {
		return nil
	}

	text := t.formatMessage(msg)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.config.BotToken)

	payload := map[string]interface{}{
		"chat_id":    t.config.ChatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	body, _ := json.Marshal(payload)
	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram API error: %s", string(respBody))
	}

	return nil
}

func (t *TelegramNotifier) formatMessage(msg NotifyMessage) string {
	emoji := t.getEmoji(msg.Event)
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("%s <b>%s</b>\n", emoji, msg.Title))
	sb.WriteString(fmt.Sprintf("━━━━━━━━━━━━━━━━\n"))

	if msg.Symbol != "" {
		sb.WriteString(fmt.Sprintf("📊 Symbol: <code>%s</code>\n", msg.Symbol))
	}

	sb.WriteString(msg.Content)

	if msg.PnL != 0 {
		pnlEmoji := "🟢"
		if msg.PnL < 0 {
			pnlEmoji = "🔴"
		}
		sb.WriteString(fmt.Sprintf("\n%s PnL: <code>%+.2f USDT</code>", pnlEmoji, msg.PnL))
	}

	sb.WriteString(fmt.Sprintf("\n\n🕐 %s", msg.Timestamp.Format("2006-01-02 15:04:05")))

	return sb.String()
}

func (t *TelegramNotifier) getEmoji(event NotifyEvent) string {
	switch event {
	case EventOpenPosition:
		return "📈"
	case EventClosePosition:
		return "📉"
	case EventStopLoss:
		return "🛑"
	case EventTakeProfit:
		return "🎯"
	case EventRiskRejected:
		return "⚠️"
	case EventError:
		return "❌"
	case EventSystemStart:
		return "🚀"
	case EventSystemStop:
		return "🔴"
	case EventHighDrawdown:
		return "📉"
	default:
		return "📢"
	}
}

// ===== Discord 通知 =====

// DiscordConfig Discord配置
type DiscordConfig struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
}

// DiscordNotifier Discord通知器
type DiscordNotifier struct {
	config DiscordConfig
	client *http.Client
}

func NewDiscordNotifier(config DiscordConfig) *DiscordNotifier {
	return &DiscordNotifier{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (d *DiscordNotifier) Name() string {
	return "Discord"
}

func (d *DiscordNotifier) Enabled() bool {
	return d.config.Enabled && d.config.WebhookURL != ""
}

func (d *DiscordNotifier) Send(msg NotifyMessage) error {
	if !d.Enabled() {
		return nil
	}

	color := d.getColor(msg.Event)

	embed := map[string]interface{}{
		"title":       msg.Title,
		"description": msg.Content,
		"color":       color,
		"timestamp":   msg.Timestamp.Format(time.RFC3339),
		"footer": map[string]string{
			"text": "Deep Trader",
		},
	}

	if msg.Symbol != "" {
		embed["fields"] = []map[string]interface{}{
			{"name": "Symbol", "value": msg.Symbol, "inline": true},
		}
		if msg.PnL != 0 {
			embed["fields"] = append(embed["fields"].([]map[string]interface{}),
				map[string]interface{}{"name": "PnL", "value": fmt.Sprintf("%+.2f USDT", msg.PnL), "inline": true},
			)
		}
	}

	payload := map[string]interface{}{
		"embeds": []interface{}{embed},
	}

	body, _ := json.Marshal(payload)
	resp, err := d.client.Post(d.config.WebhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("discord send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord API error: %s", string(respBody))
	}

	return nil
}

func (d *DiscordNotifier) getColor(event NotifyEvent) int {
	switch event {
	case EventOpenPosition, EventTakeProfit, EventSystemStart:
		return 0x00FF00 // 绿色
	case EventClosePosition:
		return 0x0099FF // 蓝色
	case EventStopLoss, EventHighDrawdown:
		return 0xFF9900 // 橙色
	case EventRiskRejected, EventError, EventSystemStop:
		return 0xFF0000 // 红色
	default:
		return 0x808080 // 灰色
	}
}

// ===== Email 通知 =====

// EmailConfig Email配置
type EmailConfig struct {
	Enabled  bool   `json:"enabled"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"` // 逗号分隔多个收件人
}

// EmailNotifier Email通知器
type EmailNotifier struct {
	config EmailConfig
}

func NewEmailNotifier(config EmailConfig) *EmailNotifier {
	return &EmailNotifier{config: config}
}

func (e *EmailNotifier) Name() string {
	return "Email"
}

func (e *EmailNotifier) Enabled() bool {
	return e.config.Enabled && e.config.SMTPHost != "" && e.config.From != "" && e.config.To != ""
}

func (e *EmailNotifier) Send(msg NotifyMessage) error {
	if !e.Enabled() {
		return nil
	}

	subject := fmt.Sprintf("[Deep Trader] %s", msg.Title)
	body := e.formatBody(msg)

	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		e.config.From, e.config.To, subject, body)

	addr := fmt.Sprintf("%s:%d", e.config.SMTPHost, e.config.SMTPPort)

	var auth smtp.Auth
	if e.config.Username != "" && e.config.Password != "" {
		auth = smtp.PlainAuth("", e.config.Username, e.config.Password, e.config.SMTPHost)
	}

	recipients := strings.Split(e.config.To, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	return smtp.SendMail(addr, auth, e.config.From, recipients, []byte(message))
}

func (e *EmailNotifier) formatBody(msg NotifyMessage) string {
	var sb strings.Builder

	sb.WriteString("<html><body style='font-family: Arial, sans-serif;'>")
	sb.WriteString(fmt.Sprintf("<h2>%s</h2>", msg.Title))

	if msg.Symbol != "" {
		sb.WriteString(fmt.Sprintf("<p><strong>Symbol:</strong> %s</p>", msg.Symbol))
	}

	sb.WriteString(fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(msg.Content, "\n", "<br>")))

	if msg.PnL != 0 {
		color := "green"
		if msg.PnL < 0 {
			color = "red"
		}
		sb.WriteString(fmt.Sprintf("<p><strong>PnL:</strong> <span style='color:%s'>%+.2f USDT</span></p>", color, msg.PnL))
	}

	sb.WriteString(fmt.Sprintf("<p style='color: #888; font-size: 12px;'>Time: %s</p>", msg.Timestamp.Format("2006-01-02 15:04:05")))
	sb.WriteString("</body></html>")

	return sb.String()
}

// ===== 通知管理器 =====

// NotificationConfig 通知配置
type NotificationConfig struct {
	Telegram TelegramConfig `json:"telegram"`
	Discord  DiscordConfig  `json:"discord"`
	Email    EmailConfig    `json:"email"`
}

// NotifyManager 通知管理器
type NotifyManager struct {
	notifiers []Notifier
	mu        sync.RWMutex
	queue     chan NotifyMessage
	quit      chan struct{}
}

// NewNotifyManager 创建通知管理器
func NewNotifyManager(config NotificationConfig) *NotifyManager {
	nm := &NotifyManager{
		notifiers: make([]Notifier, 0),
		queue:     make(chan NotifyMessage, 100),
		quit:      make(chan struct{}),
	}

	// 添加通知器
	if config.Telegram.Enabled {
		nm.notifiers = append(nm.notifiers, NewTelegramNotifier(config.Telegram))
	}
	if config.Discord.Enabled {
		nm.notifiers = append(nm.notifiers, NewDiscordNotifier(config.Discord))
	}
	if config.Email.Enabled {
		nm.notifiers = append(nm.notifiers, NewEmailNotifier(config.Email))
	}

	// 启动异步发送协程
	go nm.worker()

	return nm
}

func (nm *NotifyManager) worker() {
	for {
		select {
		case msg := <-nm.queue:
			nm.sendToAll(msg)
		case <-nm.quit:
			return
		}
	}
}

func (nm *NotifyManager) sendToAll(msg NotifyMessage) {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	for _, n := range nm.notifiers {
		if n.Enabled() {
			if err := n.Send(msg); err != nil {
				log.Printf("⚠️ [Notify] %s 发送失败: %v", n.Name(), err)
			}
		}
	}
}

// Send 异步发送通知
func (nm *NotifyManager) Send(msg NotifyMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	select {
	case nm.queue <- msg:
	default:
		log.Printf("⚠️ [Notify] 队列已满，丢弃消息: %s", msg.Title)
	}
}

// SendSync 同步发送通知
func (nm *NotifyManager) SendSync(msg NotifyMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	nm.sendToAll(msg)
}

// Close 关闭通知管理器
func (nm *NotifyManager) Close() {
	close(nm.quit)
}

// HasEnabled 检查是否有启用的通知器
func (nm *NotifyManager) HasEnabled() bool {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	for _, n := range nm.notifiers {
		if n.Enabled() {
			return true
		}
	}
	return false
}

// ===== 便捷通知方法 =====

// NotifyOpenPosition 通知开仓
func (nm *NotifyManager) NotifyOpenPosition(symbol, side string, size, entryPrice float64) {
	nm.Send(NotifyMessage{
		Event:   EventOpenPosition,
		Title:   fmt.Sprintf("Open %s %s", strings.ToUpper(side), symbol),
		Symbol:  symbol,
		Content: fmt.Sprintf("Side: %s\nSize: $%.2f\nEntry: %.4f", side, size, entryPrice),
	})
}

// NotifyClosePosition 通知平仓
func (nm *NotifyManager) NotifyClosePosition(symbol, side string, pnl, pnlPct float64) {
	nm.Send(NotifyMessage{
		Event:   EventClosePosition,
		Title:   fmt.Sprintf("Close %s %s", strings.ToUpper(side), symbol),
		Symbol:  symbol,
		PnL:     pnl,
		Content: fmt.Sprintf("PnL: %+.2f USDT (%.2f%%)", pnl, pnlPct),
	})
}

// NotifyStopLoss 通知止损
func (nm *NotifyManager) NotifyStopLoss(symbol string, pnl float64) {
	nm.Send(NotifyMessage{
		Event:   EventStopLoss,
		Title:   fmt.Sprintf("Stop Loss Triggered: %s", symbol),
		Symbol:  symbol,
		PnL:     pnl,
		Content: fmt.Sprintf("Position closed at stop loss.\nLoss: %.2f USDT", pnl),
	})
}

// NotifyRiskRejected 通知风控拒绝
func (nm *NotifyManager) NotifyRiskRejected(symbol, reason string) {
	nm.Send(NotifyMessage{
		Event:   EventRiskRejected,
		Title:   "Risk Control Rejected",
		Symbol:  symbol,
		Content: fmt.Sprintf("Reason: %s", reason),
	})
}

// NotifyError 通知错误
func (nm *NotifyManager) NotifyError(err error) {
	nm.Send(NotifyMessage{
		Event:   EventError,
		Title:   "System Error",
		Content: err.Error(),
	})
}

// NotifySystemStart 通知系统启动
func (nm *NotifyManager) NotifySystemStart(equity float64) {
	nm.Send(NotifyMessage{
		Event:   EventSystemStart,
		Title:   "Deep Trader Started",
		Content: fmt.Sprintf("Initial Equity: $%.2f\nSystem is now running.", equity),
	})
}

// NotifyHighDrawdown 通知高回撤
func (nm *NotifyManager) NotifyHighDrawdown(drawdownPct, equity float64) {
	nm.Send(NotifyMessage{
		Event:   EventHighDrawdown,
		Title:   "High Drawdown Warning",
		Content: fmt.Sprintf("Current Drawdown: %.2f%%\nEquity: $%.2f", drawdownPct*100, equity),
	})
}

// 全局通知管理器
var globalNotifier *NotifyManager

// InitGlobalNotifier 初始化全局通知管理器
func InitGlobalNotifier(config NotificationConfig) {
	globalNotifier = NewNotifyManager(config)
}

// GetNotifier 获取全局通知管理器
func GetNotifier() *NotifyManager {
	return globalNotifier
}
