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
	mu             sync.RWMutex
	latestContext  *Context
	latestDecision *FullDecision
	history        []*FullDecision
	marketData     map[string]*MarketData
}

// NewWebServer 创建新的 Web 服务
func NewWebServer() *WebServer {
	return &WebServer{
		marketData: make(map[string]*MarketData),
		history:    make([]*FullDecision, 0),
	}
}

// UpdateState 更新状态
func (s *WebServer) UpdateState(ctx *Context, decision *FullDecision, md map[string]*MarketData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestContext = ctx
	s.latestDecision = decision
	
	if decision != nil {
		s.history = append(s.history, decision)
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
			"context":     s.latestContext,
			"decision":    s.latestDecision,
			"market_data": s.marketData,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	http.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		defer s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.history)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("🌍 Web Dashboard running at http://localhost%s", addr)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("Web Server Error: %v", err)
		}
	}()
}
