// Package rule 实现确定性规则引擎（R-01~R-10），覆盖 60% 的高频客诉场景。
// 规则命中时零 Token 成本直接输出诊断结论，是整个 Agent 的第一道分流节点。
package rule

import "github.com/buspulse/diagnose-agent/internal/domain"

// RuleID 规则唯一标识
type RuleID string

const (
	R01GeoFenceOut          RuleID = "R-01"
	R02VirtualStationDisabled RuleID = "R-02"
	R03CapacityFull         RuleID = "R-03"
	R04HeadingMismatch      RuleID = "R-04"
	R05EtaProtection        RuleID = "R-05"
	R06GPSDeviation         RuleID = "R-06"
	R07TrafficCongestion    RuleID = "R-07"
	R08DriverAppOffline     RuleID = "R-08"
	R09MarketingMismatch    RuleID = "R-09"
	R10ConnectionPoolExhausted RuleID = "R-10"
)

// RuleResult 规则命中后的确定性输出
type RuleResult struct {
	RuleID      RuleID                   `json:"rule_id"`
	Category    domain.RootCauseCategory `json:"category"`
	Analysis    string                   `json:"analysis"`    // 技术根因一句话
	Script      string                   `json:"script"`      // 客服话术
	IssueLevel  domain.IssueLevel        `json:"issue_level"`
	Actions     []domain.RecommendedAction `json:"actions,omitempty"`
}

// MatchInput 规则引擎所需的全量输入快照
// 由工具扇出层填充，各字段可为 nil（规则内部做 nil 检查）
type MatchInput struct {
	Order   *domain.OrderContext
	GPS     *domain.VehicleGPS
	Algo    *domain.AlgoSnapshot
	Trace   *domain.TraceLog
	Fence   *domain.GeoFence
	Station *domain.StationFlow
	Eta     *domain.EtaSnapshot
}

// Thresholds 规则判定阈值，从配置层注入，避免硬编码
type Thresholds struct {
	GPSDeviationM        float64 // R-06：偏离距离阈值（米）
	GPSDeviationS        int     // R-06：持续偏离时间阈值（秒）
	EtaProtectionMin     float64 // R-05：在车乘客 ETA 保护阈值（分钟）
	HeadingConflictDeg   float64 // R-04：方向冲突角度阈值（度）
	CongestionIndex      float64 // R-07：深红拥堵指数阈值
	AntiCheatCancelCount int     // R-02（反作弊）：24h 取消次数阈值
}

// DefaultThresholds 与 config.yaml 对应的默认值
var DefaultThresholds = Thresholds{
	GPSDeviationM:      500,
	GPSDeviationS:      180,
	EtaProtectionMin:   15,
	HeadingConflictDeg: 90,
	CongestionIndex:    3.5,
	AntiCheatCancelCount: 3,
}
