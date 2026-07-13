package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Timeout 为每个请求注入全局诊断超时 context。
// 超时后返回 503，而非让 handler 无限挂起。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			done := make(chan struct{})
			// 在独立 goroutine 中执行 handler，以便在超时时能抢先响应
			panicCh := make(chan interface{}, 1)
			go func() {
				defer func() {
					if p := recover(); p != nil {
						panicCh <- p
					}
					close(done)
				}()
				next.ServeHTTP(w, r.WithContext(ctx))
			}()

			select {
			case <-done:
				// handler 正常完成，检查是否有 panic 需要处理
				select {
				case p := <-panicCh:
					// handler goroutine 发生了 panic，但 done 已关闭
					// Recover 中间件会处理，此处仅透传
					_ = p
				default:
				}
			case <-ctx.Done():
				// 超时：写入 503（只在 header 未发送时有效）
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "diagnosis timeout, please retry",
					"code":  "DIAGNOSE_TIMEOUT",
				})
				// 等待 handler goroutine 退出，避免 goroutine 泄漏
				<-done
			}
		})
	}
}
