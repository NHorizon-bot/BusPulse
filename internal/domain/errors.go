package domain

import "errors"

// 业务语义哨兵错误，用于在层间传递语义，由 pkg/errutil 提供包装工具。

var (
	// ErrCriticalDataMissing CRITICAL 级工具全部失败，无法开始推理
	ErrCriticalDataMissing = errors.New("critical tool data missing")

	// ErrLLMTimeout LLM 推理超时（超过独立计时器阈值）
	ErrLLMTimeout = errors.New("llm inference timeout")

	// ErrParseRetryExceeded JSON 解析格式重试次数（1次）耗尽
	ErrParseRetryExceeded = errors.New("llm response parse retry exceeded")

	// ErrMaxIterationsExceeded ReAct 推理轮次（4轮）耗尽未收敛，强制输出
	ErrMaxIterationsExceeded = errors.New("react max iterations exceeded")

	// ErrOrderNotFound 订单在工具层查询不到
	ErrOrderNotFound = errors.New("order not found")

	// ErrStateMachinePanic 状态机内部 panic 被 recover 捕获
	ErrStateMachinePanic = errors.New("state machine recovered from panic")
)
