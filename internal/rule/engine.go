package rule

import (
	"context"

	"github.com/buspulse/diagnose-agent/internal/domain"
)

// Engine 规则引擎，无状态，线程安全，可复用同一实例。
type Engine struct {
	thresholds Thresholds
}

// NewEngine 创建规则引擎实例，注入阈值配置。
func NewEngine(th Thresholds) *Engine {
	return &Engine{thresholds: th}
}

// Match 依次执行所有规则，短路返回第一个命中的结论。
// 若无规则命中，返回 (nil, false)，由调用方激活 LLM 推理路径。
// ctx 用于未来扩展（如规则本身需要异步查询），当前同步执行。
func (e *Engine) Match(_ context.Context, in MatchInput) (*RuleResult, bool) {
	for _, r := range allRules {
		if result, hit := r(in, e.thresholds); hit {
			return result, true
		}
	}
	return nil, false
}

// ToReport 将规则结论转换为标准 DiagnosticReport，由接入层统一渲染。
func ToReport(result *RuleResult, req domain.DiagnosticRequest) *domain.DiagnosticReport {
	return &domain.DiagnosticReport{
		IssueLevel:            result.IssueLevel,
		RootCauseCategory:     result.Category,
		RootCauseAnalysis:     result.Analysis,
		CustomerServiceScript: result.Script,
		AgentExecutionPath:    []string{string(result.RuleID)},
		RecommendedActions:    result.Actions,
		Confidence:            1.0, // 规则命中为确定性结论，置信度 100%
		NeedsReview:           false,
	}
}
