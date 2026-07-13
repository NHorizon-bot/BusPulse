// Package domain 定义跨层共享的核心领域模型与数据契约。
// 本包不依赖任何其他 internal 子包，是整个项目的依赖基底。
package domain

// IssueLevel 诊断优先级（P1 最高）
type IssueLevel string

const (
	IssueLevelP1 IssueLevel = "P1" // 严重：技术链路故障、批量进线
	IssueLevelP2 IssueLevel = "P2" // 中等：策略拦截、算法配置问题
	IssueLevelP3 IssueLevel = "P3" // 低危：运营区域/时段限制
)

// RootCauseCategory 根因大类枚举，前端直接渲染 icon & 颜色
type RootCauseCategory string

const (
	CatGeoFenceOut          RootCauseCategory = "GEO_FENCE_OUT"          // 超区下单
	CatVirtualStationDisabled RootCauseCategory = "VIRTUAL_STATION_DISABLED" // 虚拟站点失效
	CatAlgoEtaTimeout       RootCauseCategory = "ALGO_ETA_TIMEOUT"       // 算法 ETA 超阈值
	CatAlgoHeadingMismatch  RootCauseCategory = "ALGO_HEADING_MISMATCH"  // 方向冲突
	CatCapacityFull         RootCauseCategory = "CAPACITY_FULL"          // 班次满载
	CatDriverReject         RootCauseCategory = "DRIVER_REJECT"          // 司机拒单
	CatTechRPCError         RootCauseCategory = "TECH_RPC_ERROR"         // 技术链路报错
	CatTechTraceTimeout     RootCauseCategory = "TECH_TRACE_TIMEOUT"     // 下游服务超时
	CatAntiCheatBlock       RootCauseCategory = "ANTI_CHEAT_BLOCK"       // 反作弊拦截
	CatDriverGPSDeviation   RootCauseCategory = "DRIVER_GPS_DEVIATION"   // 司机 GPS 偏离
	CatTrafficCongestion    RootCauseCategory = "TRAFFIC_CONGESTION"     // 客观拥堵
	CatEtaProtection        RootCauseCategory = "ETA_PROTECTION"         // 在车乘客 ETA 保护
	CatUnknown              RootCauseCategory = "UNKNOWN"                // 未能定性，转人工
)

// ActionType 推荐动作类型
type ActionType string

const (
	ActionAutoRefund    ActionType = "AUTO_REFUND"     // 自动退款
	ActionTriggerJira   ActionType = "TRIGGER_JIRA"    // 创建 Jira 工单
	ActionConfigRollback ActionType = "CONFIG_ROLLBACK" // 回滚配置
	ActionNotifyOps     ActionType = "NOTIFY_OPS"      // 通知运营
	ActionManualReview  ActionType = "MANUAL_REVIEW"   // 转人工复核
)

// DiagnosticSource 诊断来源（用于审计与分流统计）
type DiagnosticSource string

const (
	SourceWidget      DiagnosticSource = "WIDGET"       // 工单系统微插件
	SourceFeishuCard  DiagnosticSource = "FEISHU_CARD"  // Feishu/Link 机器人卡片
	SourceAlert       DiagnosticSource = "ALERT"        // 稳定性告警自动触发
	SourceAPI         DiagnosticSource = "API"          // 直接 API 调用（调试/集成）
)

// RecommendedAction 推荐动作（前端渲染为可点击按钮）
type RecommendedAction struct {
	ActionType  ActionType `json:"action_type"`
	ActionName  string     `json:"action_name"`            // 中文展示名称
	ActionPayload string   `json:"action_payload,omitempty"` // JSON 字符串，由执行层解析
}

// DiagnosticReport 最终诊断报告，是整个系统的核心输出契约。
// 前端直接解析此 JSON 渲染诊断卡片，不允许出现自然语言废话。
type DiagnosticReport struct {
	IssueLevel            IssueLevel          `json:"issue_level"`
	RootCauseCategory     RootCauseCategory   `json:"root_cause_category"`
	RootCauseAnalysis     string              `json:"root_cause_analysis"`      // 一句话根因，技术语言
	CustomerServiceScript string              `json:"customer_service_script"`  // 客服话术，无技术黑话
	AgentExecutionPath    []string            `json:"agent_execution_path"`     // 实际调用的工具链路
	RecommendedActions    []RecommendedAction `json:"recommended_actions"`

	// 置信度与降级标注
	Confidence   float64 `json:"confidence"`             // 0.0~1.0，规则命中=1.0
	NeedsReview  bool    `json:"needs_review"`           // true 表示建议人工复核
	ReviewReason string  `json:"review_reason,omitempty"` // 触发人工复核的原因
}

// DiagnosticRequest 诊断请求入参
type DiagnosticRequest struct {
	OrderID    string           `json:"order_id"`
	CityID     string           `json:"city_id,omitempty"`
	TraceID    string           `json:"trace_id,omitempty"`
	OperatorID string           `json:"operator_id,omitempty"` // 触发人，用于审计
	Source     DiagnosticSource `json:"source"`
	// 自然语言补充描述（来自客服工单或 @机器人 消息）
	FreeTextContext string `json:"free_text_context,omitempty"`
}
