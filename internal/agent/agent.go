// Package agent 实现诊断 Agent 的推理控制层。
// 核心架构：
//   - 规则引擎前置：R-01~R-10 确定性秒杀（零 Token 成本）
//   - Eino react.NewAgent：ReAct 循环，LLM 自主决定调用哪些工具
//   - 模型：eino-ext 官方 DeepSeek / Qwen（OpenAI 兼容接口）
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	einoDeepSeek "github.com/cloudwego/eino-ext/components/model/deepseek"
	einoOpenAI "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/buspulse/diagnose-agent/internal/agent/tools"
	"github.com/buspulse/diagnose-agent/internal/config"
	"github.com/buspulse/diagnose-agent/internal/domain"
	"github.com/buspulse/diagnose-agent/internal/gateway"
	"github.com/buspulse/diagnose-agent/internal/rule"
	"github.com/buspulse/diagnose-agent/pkg/jsonutil"
)

// loggingChatModel 包装模型，将每轮 ReAct 调用打印到控制台
type loggingChatModel struct {
	inner model.ToolCallingChatModel
	step  int
}

func (l *loggingChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	l.step++
	log.Printf("══════════ ReAct 第 %d 轮 ── LLM 收到以下消息 ══════════", l.step)

	for _, msg := range in {
		switch {
		case msg.Role == schema.User:
			log.Printf("👤 [User] %.200s", msg.Content)
		case len(msg.ToolCalls) > 0:
			for _, tc := range msg.ToolCalls {
				log.Printf("🔧 [ToolCall] %s(%s)", tc.Function.Name, truncateStr(tc.Function.Arguments, 200))
			}
		case msg.Role == schema.Tool:
			log.Printf("📦 [ToolResult-%s] %.150s", msg.Name, msg.Content)
		case msg.Content != "":
			log.Printf("💬 [Assistant] %.200s", msg.Content)
		default:
			log.Printf("📝 [%s] (empty or system msg)", msg.Role)
		}
	}

	out, err := l.inner.Generate(ctx, in, opts...)
	if err != nil {
		log.Printf("❌ [LLM Error] %v", err)
		return out, err
	}

	if len(out.ToolCalls) > 0 {
		for _, tc := range out.ToolCalls {
			log.Printf("🧠 [LLM→工具] %s(%s)", tc.Function.Name, truncateStr(tc.Function.Arguments, 200))
		}
		log.Printf("────────────────── 等待工具返回结果后进入下一轮 ──────────────────")
	} else {
		log.Printf("✅ [LLM 最终结论] %.400s", out.Content)
	}

	return out, nil
}

func (l *loggingChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return l.inner.Stream(ctx, in, opts...)
}

func (l *loggingChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	wrappedInner, err := l.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &loggingChatModel{inner: wrappedInner, step: l.step}, nil
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ModelProvider 模型供应商选择
type ModelProvider string

const (
	ProviderDeepSeek ModelProvider = "deepseek"
	ProviderQwen     ModelProvider = "qwen" // 通过 OpenAI 兼容接口
)

// DiagAgent 诊断 Agent，封装规则引擎 + Eino ReAct 推理。
type DiagAgent struct {
	ruleEngine *rule.Engine
	reactAgent *react.Agent
}

// NewDiagAgent 组装完整诊断 Agent。
// 依赖注入：配置 → 模型 → 工具（InferTool） → ReAct Agent → 规则引擎。
func NewDiagAgent(
	ctx context.Context,
	cfg *config.Config,
	ruleEngine *rule.Engine,
	san *gateway.Sanitizer,
) (*DiagAgent, error) {

	// ── 1. 初始化 LLM 模型 ────────────────────────────────────────────────
	rawModel, err := buildChatModel(ctx, &cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("build chat model: %w", err)
	}
	// 包装日志层，打印 ReAct 每轮循环
	chatModel := &loggingChatModel{inner: rawModel}
	log.Printf("🤖 ReAct 日志已启用 — 每轮 LLM 调用将打印到控制台")

	// ── 2. 注册 Eino 工具（utils.InferTool 自动推断 JSON Schema）──────────
	// LLM 在 ReAct 循环中自主决定调用哪些工具、调用几次
	// 而非旧版的固定顺序全量扇出
	einoTools, err := tools.All(san)
	if err != nil {
		return nil, fmt.Errorf("build eino tools: %w", err)
	}

	// 提取 InvokableTool（所有工具均实现此接口）
	invokableTools := make([]einoTool.InvokableTool, 0, len(einoTools))
	for _, t := range einoTools {
		if it, ok := t.(einoTool.InvokableTool); ok {
			invokableTools = append(invokableTools, it)
		}
	}

	// ── 3. 创建 Eino react.NewAgent（内置 ReAct 循环）────────────────────
	// MaxStep：react 内部每轮含 ChatModel + ToolsNode 共 2 步
	// MaxIterations=4 → MaxStep=4*2+2=10（含初始和最终输出步骤）
	maxStep := cfg.LLM.MaxIterations*2 + 2
	if maxStep <= 4 {
		maxStep = 10
	}

	reactAgent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			// BaseTool 是 InvokableTool 的父接口，直接传即可
			Tools: func() []einoTool.BaseTool {
				base := make([]einoTool.BaseTool, len(invokableTools))
				for i, t := range invokableTools {
					base[i] = t
				}
				return base
			}(),
		},
		// MessageModifier：每轮 LLM 调用前注入 System Prompt（SOP + 格式约束）
		MessageModifier: react.NewPersonaModifier(BuildSystemPrompt()),
		MaxStep:         maxStep,
	})
	if err != nil {
		return nil, fmt.Errorf("create react agent: %w", err)
	}

	return &DiagAgent{
		ruleEngine: ruleEngine,
		reactAgent: reactAgent,
	}, nil
}

// Diagnose 执行完整诊断流程，返回结构化报告。
// Eino ReAct 推理 → JSON 解析 → 降级兜底。
func (a *DiagAgent) Diagnose(
	ctx context.Context,
	req domain.DiagnosticRequest,
) (*domain.DiagnosticReport, error) {

	// ── Eino ReAct 推理 ───────────────────────────────────────────────────
	// react.Agent.Generate：
	//   内部自动循环：Think(LLM) → Act(调工具) → Observe(工具结果) → …
	//   直到 LLM 不再产生 tool_call 或达到 MaxStep 为止
	userMsg := buildUserMessage(req)
	output, err := a.reactAgent.Generate(ctx, []*schema.Message{userMsg})
	if err != nil {
		return fallbackReport(req, fmt.Sprintf("ReAct 推理失败：%v", err)), nil
	}

	// ── 解析 LLM 最终输出（schema.Message.Content 是 string）────────────
	report, parseErr := parseReport(output.Content)
	if parseErr != nil {
		return fallbackReport(req, fmt.Sprintf("输出解析失败：%v，原文：%.200s", parseErr, output.Content)), nil
	}

	return report, nil
}

// ── 模型构建 ───────────────────────────────────────────────────────────────

// buildChatModel 根据配置中的 Provider 选择模型实现。
// DeepSeek 和 Qwen 都实现 model.ToolCallingChatModel，可直接传入 react.AgentConfig。
func buildChatModel(ctx context.Context, cfg *config.LLMConfig) (model.ToolCallingChatModel, error) {
	switch ModelProvider(cfg.Provider) {
	case ProviderDeepSeek, "": // 默认 DeepSeek
		return buildDeepSeek(ctx, cfg)
	case ProviderQwen:
		return buildQwen(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown model provider: %q (supported: deepseek, qwen)", cfg.Provider)
	}
}

func buildDeepSeek(ctx context.Context, cfg *config.LLMConfig) (model.ToolCallingChatModel, error) {
	dsCfg := &einoDeepSeek.ChatModelConfig{
		APIKey: cfg.APIKey,
		Model:  cfg.Model, // "deepseek-chat" | "deepseek-reasoner"(R1)
	}
	if cfg.Endpoint != "" {
		dsCfg.BaseURL = cfg.Endpoint
	}
	if cfg.MaxTokens > 0 {
		dsCfg.MaxTokens = cfg.MaxTokens
	}
	return einoDeepSeek.NewChatModel(ctx, dsCfg)
}

func buildQwen(ctx context.Context, cfg *config.LLMConfig) (model.ToolCallingChatModel, error) {
	// Qwen 通过 DashScope OpenAI 兼容接口接入
	// BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"
	qwenCfg := &einoOpenAI.ChatModelConfig{
		APIKey:  cfg.APIKey,
		Model:   cfg.Model, // "qwen-plus" | "qwen-max" 等
		BaseURL: cfg.Endpoint,
	}
	return einoOpenAI.NewChatModel(ctx, qwenCfg)
}

// ── 消息与报告 ─────────────────────────────────────────────────────────────

func buildUserMessage(req domain.DiagnosticRequest) *schema.Message {
	content := fmt.Sprintf("请诊断以下公交订单问题：\n订单号：%s\n城市：%s",
		req.OrderID, req.CityID)
	if req.FreeTextContext != "" {
		content += fmt.Sprintf("\n客服描述：%s", req.FreeTextContext)
	}
	if req.TraceID != "" {
		content += fmt.Sprintf("\nTraceID：%s（如有技术报错请调用 GetTraceLog）", req.TraceID)
	}
	return schema.UserMessage(content)
}

// parseReport 三层 JSON 解析防御链（复用 pkg/jsonutil）
func parseReport(raw string) (*domain.DiagnosticReport, error) {
	jsonBytes, err := jsonutil.ExtractJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("extract json: %w", err)
	}
	var report domain.DiagnosticReport
	if err := json.Unmarshal(jsonBytes, &report); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if report.RootCauseAnalysis == "" {
		return nil, fmt.Errorf("missing root_cause_analysis")
	}
	// 枚举归一化
	switch report.IssueLevel {
	case domain.IssueLevelP1, domain.IssueLevelP2, domain.IssueLevelP3:
	default:
		report.IssueLevel = domain.IssueLevelP2
	}
	if report.RootCauseCategory == "" {
		report.RootCauseCategory = domain.CatUnknown
	}
	return &report, nil
}

func fallbackReport(_ domain.DiagnosticRequest, reason string) *domain.DiagnosticReport {
	return &domain.DiagnosticReport{
		IssueLevel:            domain.IssueLevelP2,
		RootCauseCategory:     domain.CatUnknown,
		RootCauseAnalysis:     "自动诊断失败：" + reason,
		CustomerServiceScript: "非常抱歉给您带来不便，我们已记录您的问题，将由专员跟进处理。",
		RecommendedActions: []domain.RecommendedAction{
			{ActionType: domain.ActionManualReview, ActionName: "转人工复核"},
		},
		Confidence:   0.0,
		NeedsReview:  true,
		ReviewReason: reason,
	}
}
