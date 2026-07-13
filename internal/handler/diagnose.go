package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/buspulse/diagnose-agent/internal/audit"
	"github.com/buspulse/diagnose-agent/internal/domain"
	"github.com/buspulse/diagnose-agent/internal/handler/middleware"
)

// Diagnoser 诊断执行接口，由 agent.DiagAgent 实现。
// Handler 只依赖此接口，不直接引用 agent 包（避免循环依赖）。
type Diagnoser interface {
	Diagnose(ctx context.Context, req domain.DiagnosticRequest) (*domain.DiagnosticReport, error)
}

// DiagnoseHandler 处理 POST /api/v1/diagnose 请求。
type DiagnoseHandler struct {
	diagnoser   Diagnoser
	auditLogger *audit.Logger
}

// NewDiagnoseHandler 创建 DiagnoseHandler。
func NewDiagnoseHandler(diagnoser Diagnoser, auditLogger *audit.Logger) *DiagnoseHandler {
	return &DiagnoseHandler{diagnoser: diagnoser, auditLogger: auditLogger}
}

type diagnoseRequest struct {
	OrderID         string `json:"order_id"`
	CityID          string `json:"city_id,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	FreeTextContext string `json:"free_text_context,omitempty"`
}

type diagnoseResponse struct {
	AuditID    string                   `json:"audit_id"`
	Report     *domain.DiagnosticReport `json:"report"`
	DurationMs int64                    `json:"duration_ms"`
}

func (h *DiagnoseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "only POST is accepted")
		return
	}

	var body diagnoseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must be valid JSON")
		return
	}
	if body.OrderID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ORDER_ID", "order_id is required")
		return
	}

	ctx := r.Context()
	operatorID := middleware.OperatorIDFromCtx(ctx)
	triggerSource := middleware.TriggerSourceFromCtx(ctx)
	if triggerSource == "" {
		triggerSource = string(domain.SourceAPI)
	}

	domainReq := domain.DiagnosticRequest{
		OrderID:         body.OrderID,
		CityID:          body.CityID,
		TraceID:         body.TraceID,
		OperatorID:      operatorID,
		Source:          domain.DiagnosticSource(triggerSource),
		FreeTextContext: body.FreeTextContext,
	}

	report, diagErr := h.diagnoser.Diagnose(ctx, domainReq)
	elapsed := time.Since(start).Milliseconds()

	// DiagAgent 保证永远返回非 nil report（降级兜底）
	if report == nil {
		report = &domain.DiagnosticReport{
			IssueLevel:            domain.IssueLevelP2,
			RootCauseCategory:     domain.CatUnknown,
			RootCauseAnalysis:     "系统内部错误，请人工介入",
			CustomerServiceScript: "非常抱歉，系统出现异常，我们将尽快跟进处理。",
			NeedsReview:           true,
		}
	}

	auditID := audit.NewAuditID(body.OrderID)
	errStr := ""
	if diagErr != nil {
		errStr = diagErr.Error()
	}
	go func() {
		_ = h.auditLogger.Write(audit.Record{
			AuditID:        auditID,
			OperatorID:     operatorID,
			TriggerSource:  triggerSource,
			OrderID:        body.OrderID,
			TimestampUnix:  time.Now().Unix(),
			DiagDurationMs: elapsed,
			FinalReport:    report,
			HumanAction:    audit.HumanActionPending,
			Err:            errStr,
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(diagnoseResponse{
		AuditID:    auditID,
		Report:     report,
		DurationMs: elapsed,
	})
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}
