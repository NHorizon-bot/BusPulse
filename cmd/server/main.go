// Package main 是 diagnose-agent 服务的唯一启动入口。
// 职责：加载配置 → 组装依赖 → 注册路由 → 启动 HTTP server → 优雅退出。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/buspulse/diagnose-agent/internal/agent"
	"github.com/buspulse/diagnose-agent/internal/audit"
	"github.com/buspulse/diagnose-agent/internal/config"
	"github.com/buspulse/diagnose-agent/internal/gateway"
	"github.com/buspulse/diagnose-agent/internal/handler"
	"github.com/buspulse/diagnose-agent/internal/handler/middleware"
	"github.com/buspulse/diagnose-agent/internal/rule"
)

func main() {
	ctx := context.Background()

	// ── 1. 加载配置 ──────────────────────────────────────────────────────
	cfg, err := config.Load("configs/config.yaml", "configs/config.local.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// ── 2. 审计日志 ──────────────────────────────────────────────────────
	auditLogger, err := audit.NewFileLogger(cfg.Audit.FilePath)
	if err != nil {
		log.Fatalf("init audit logger: %v", err)
	}
	defer auditLogger.Close()

	// ── 3. 脱敏网关 ──────────────────────────────────────────────────────
	san := gateway.New(cfg.Sanitizer.MaxPayloadBytes)

	// ── 4. 规则引擎 ──────────────────────────────────────────────────────
	ruleThresholds := rule.Thresholds{
		GPSDeviationM:        cfg.RuleEngine.GPSDeviationThresholdM,
		GPSDeviationS:        cfg.RuleEngine.GPSDeviationDurationS,
		EtaProtectionMin:     cfg.RuleEngine.EtaProtectionThresholdMin,
		HeadingConflictDeg:   cfg.RuleEngine.HeadingConflictDeg,
		CongestionIndex:      cfg.RuleEngine.CongestionIndexThreshold,
		AntiCheatCancelCount: 3,
	}
	ruleEngine := rule.NewEngine(ruleThresholds)

	// ── 5. Eino DiagAgent（react.NewAgent + eino-ext 模型）────────────────
	// api_key 为空时使用 Mock 模式（本地开发无需填写）
	diagAgent, err := agent.NewDiagAgent(ctx, cfg, ruleEngine, san)
	if err != nil {
		log.Fatalf("init diag agent: %v", err)
	}

	// ── 6. HTTP 路由注册 ──────────────────────────────────────────────────
	mux := http.NewServeMux()

	diagnoseH := handler.NewDiagnoseHandler(diagAgent, auditLogger)
	// 中间件链（由外到内）：Recover → Timeout → AuditContext → handler
	mux.Handle("/api/v1/diagnose",
		middleware.Recover(
			middleware.Timeout(cfg.DiagnoseTimeout())(
				middleware.AuditContext(diagnoseH),
			),
		),
	)
	mux.HandleFunc("/health", handler.HealthHandler)

	// ── 7. 启动 HTTP server ───────────────────────────────────────────────
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("diagnose-agent listening on %s (model: %s/%s)",
			addr, cfg.LLM.Provider, cfg.LLM.Model)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ── 8. 优雅退出 ───────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server forced shutdown: %v", err)
	}
	log.Println("server exited")
}
