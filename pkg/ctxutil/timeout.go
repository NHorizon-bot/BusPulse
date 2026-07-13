// Package ctxutil 提供 context 超时预算计算辅助，与业务无关。
package ctxutil

import (
	"context"
	"time"
)

// RemainingBudget 计算 ctx 的剩余超时预算。
// 若 ctx 无截止时间，返回 defaultBudget。
// 若已超时，返回 0。
func RemainingBudget(ctx context.Context, defaultBudget time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultBudget
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// WithBudget 从 parent 派生子 context，超时时间为 min(remaining, budget)。
// 确保子 context 不会超过父 context 的剩余时间。
// 调用方必须调用返回的 cancel 函数，通常通过 defer cancel() 完成。
func WithBudget(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	remaining := RemainingBudget(parent, budget)
	if remaining <= 0 || remaining < budget {
		// 父 context 剩余时间更短，直接派生（不设新截止时间，由父 context 控制）
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, budget)
}
