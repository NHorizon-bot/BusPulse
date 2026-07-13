package middleware

import (
	"context"
	"net/http"
)

type contextKey string

const (
	// CtxKeyOperatorID 请求触发人（从 Header X-Operator-Id 读取）
	CtxKeyOperatorID contextKey = "operator_id"
	// CtxKeyTriggerSource 触发来源（从 Header X-Trigger-Source 读取）
	CtxKeyTriggerSource contextKey = "trigger_source"
)

// AuditContext 从 HTTP Header 提取审计元数据注入 context，
// 供 handler 层写审计日志时使用。
func AuditContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if v := r.Header.Get("X-Operator-Id"); v != "" {
			ctx = context.WithValue(ctx, CtxKeyOperatorID, v)
		}
		if v := r.Header.Get("X-Trigger-Source"); v != "" {
			ctx = context.WithValue(ctx, CtxKeyTriggerSource, v)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OperatorIDFromCtx 从 context 安全取出 operator_id，不存在返回空串。
func OperatorIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(CtxKeyOperatorID).(string)
	return v
}

// TriggerSourceFromCtx 从 context 安全取出 trigger_source，不存在返回空串。
func TriggerSourceFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(CtxKeyTriggerSource).(string)
	return v
}
