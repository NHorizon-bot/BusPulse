// Package middleware 提供 HTTP 中间件。
package middleware

import (
	"encoding/json"
	"net/http"
)

// Recover 捕获 handler 中的 panic，返回 500 而非让进程崩溃。
// 状态机顶层已有 recover，此中间件作为 HTTP 层最外层兜底。
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "internal server error",
					"code":  "PANIC_RECOVERED",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
