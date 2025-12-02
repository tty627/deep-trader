package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// WebServer 简单的 Web 监控服务
type WebServer struct {
	mu               sync.RWMutex
	latestContext    *Context
	latestDecision   *FullDecision
	history          []*FullDecision
	marketData       map[string]*MarketData
	loopIntervalSecs int // 当前 AI 循环周期（秒）
}

// NewWebServer 创建新的 Web 服务
func NewWebServer(defaultInterval int) *WebServer {
	if defaultInterval <= 0 {
		defaultInterval = 150
	}
	return &WebServer{
		marketData:       make(map[string]*MarketData),
		history:          make([]*FullDecision, 0),
		loopIntervalSecs: defaultInterval,
	}
}

// SetLoopIntervalSeconds 设置循环周期（秒）
func (s *WebServer) SetLoopIntervalSeconds(sec int) {
	if sec <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loopIntervalSecs = sec
}

// GetLoopIntervalSeconds 获取当前循环周期（秒）
func (s *WebServer) GetLoopIntervalSeconds() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loopIntervalSecs
}

// UpdateState 更新状态
func (s *WebServer) UpdateState(ctx *Context, decision *FullDecision, md map[string]*MarketData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestContext = ctx
	s.latestDecision = decision
	
	if decision != nil {
		// 避免同一个 FullDecision 被重复加入历史（例如一个周期内多次 UpdateState 调用）
		if len(s.history) == 0 || decision != s.history[len(s.history)-1] {
			s.history = append(s.history, decision)
		}
	}

	// Deep copy market data
	s.marketData = make(map[string]*MarketData)
	for k, v := range md {
		s.marketData[k] = v
	}
}

// Start 启动 HTTP 服务
func (s *WebServer) Start(port int) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/index.html")
	})

	http.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		defer s.mu.RUnlock()

		resp := map[string]interface{}{
			"context":              s.latestContext,
			"decision":             s.latestDecision,
			"market_data":          s.marketData,
			"loop_interval_seconds": s.loopIntervalSecs,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// 获取 / 设置循环周期
	http.HandleFunc("/api/loop_interval", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.mu.RLock()
			defer s.mu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"loop_interval_seconds": s.loopIntervalSecs,
			})
		case http.MethodPost:
			var req struct {
				LoopIntervalSeconds int `json:"loop_interval_seconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
				return
			}
			if req.LoopIntervalSeconds < 30 || req.LoopIntervalSeconds > 900 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "loop_interval_seconds must be between 30 and 900"})
				return
			}

			s.SetLoopIntervalSeconds(req.LoopIntervalSeconds)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":               "ok",
				"loop_interval_seconds": req.LoopIntervalSeconds,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	http.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		defer s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.history)
	})

	// 一键平仓 API：POST /api/close_all
	http.HandleFunc("/api/close_all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		cfg, err := LoadConfig()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "load config failed"})
			return
		}
		if cfg.BinanceAPIKey == "" || cfg.BinanceSecretKey == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "only available in real trading mode (Binance API key/secret required)"})
			return
		}

		ex := NewBinanceExchange(cfg.BinanceAPIKey, cfg.BinanceSecretKey, cfg.BinanceProxyURL)

		// 查询当前实盘持仓并全部平掉
		positions := ex.GetPositions()
		if len(positions) == 0 {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok",
				"closed": 0,
			})
			return
		}

		closed := 0
		var errs []string
		for _, p := range positions {
			var action string
			if p.Side == "long" {
				action = "close_long"
			} else if p.Side == "short" {
				action = "close_short"
			} else {
				continue
			}

			if err := ex.ExecuteDecision(Decision{Symbol: p.Symbol, Action: action}); err != nil {
				log.Printf("CloseAll error %s %s: %v", p.Symbol, action, err)
				errs = append(errs, fmt.Sprintf("%s: %v", p.Symbol, err))
			} else {
				closed++
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"status": "ok",
			"closed": closed,
		}
		if len(errs) > 0 {
			resp["errors"] = errs
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// 手动调整杠杆 API：POST /api/set_leverage {symbol, leverage}
	http.HandleFunc("/api/set_leverage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Symbol   string `json:"symbol"`
			Leverage int    `json:"leverage"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
			return
		}
		if req.Symbol == "" || req.Leverage <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "symbol and positive leverage required"})
			return
		}

		// 重新加载配置并调用 Binance 实盘
		cfg, err := LoadConfig()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "load config failed"})
			return
		}
		if cfg.BinanceAPIKey == "" || cfg.BinanceSecretKey == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "binance api key/secret not configured"})
			return
		}

		ex := NewBinanceExchange(cfg.BinanceAPIKey, cfg.BinanceSecretKey, cfg.BinanceProxyURL)
		if err := ex.SetLeverage(req.Symbol, req.Leverage); err != nil {
			log.Printf("SetLeverage API error: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("🌍 Web Dashboard running at http://localhost%s", addr)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("Web Server Error: %v", err)
		}
	}()
}
