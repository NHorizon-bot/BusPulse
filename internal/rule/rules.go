package rule

import (
	"strings"

	"github.com/buspulse/diagnose-agent/internal/domain"
)

// rule 是单条规则的函数签名：输入快照 + 阈值 → (命中结论, 是否命中)
type rule func(in MatchInput, th Thresholds) (*RuleResult, bool)

// allRules 按优先级排列，引擎短路——首个命中即停止。
// 顺序依据：技术报错 > 策略拦截 > 运营配置问题
var allRules = []rule{
	checkR10ConnectionPool,  // 技术：连接池耗尽（批量进线必杀）
	checkR01GeoFenceOut,     // 准入：GPS 超区
	checkR02VirtualStation,  // 准入：虚拟站点失效
	checkR03CapacityFull,    // 派单：班次满载
	checkR04HeadingMismatch, // 派单：方向冲突
	checkR05EtaProtection,   // 派单：在车乘客 ETA 保护
	checkR06GPSDeviation,    // 履约：司机 GPS 偏离
	checkR07Congestion,      // 履约：客观拥堵
	checkR08DriverAppOffline, // 场站：司机 App 切后台
	checkR09MarketingMismatch, // 费用：营销规则不匹配
}

// ── R-01 GPS 超区下单 ──────────────────────────────────────────────────────
func checkR01GeoFenceOut(in MatchInput, _ Thresholds) (*RuleResult, bool) {
	if in.Fence == nil {
		return nil, false
	}
	if in.Fence.UserInsideFence {
		return nil, false
	}
	return &RuleResult{
		RuleID:     R01GeoFenceOut,
		Category:   domain.CatGeoFenceOut,
		IssueLevel: domain.IssueLevelP3,
		Analysis:   "用户当前坐标不在服务围栏内，或非运营时段（" + in.Fence.OperationHoursDesc + "）",
		Script:     "非常抱歉，网约公交目前仅在限定区域和时段（" + in.Fence.OperationHoursDesc + "）内提供服务，您当前的位置或时间暂时超出了服务范围。",
		Actions: []domain.RecommendedAction{
			{ActionType: domain.ActionNotifyOps, ActionName: "通知运营查看服务区设置"},
		},
	}, true
}

// ── R-02 虚拟站点失效 ─────────────────────────────────────────────────────
func checkR02VirtualStation(in MatchInput, _ Thresholds) (*RuleResult, bool) {
	if in.Station == nil {
		return nil, false
	}
	if in.Station.Status == domain.StationActive && in.Station.NearbyActiveCount > 0 {
		return nil, false
	}
	reason := in.Station.InactiveReason
	if reason == "" {
		reason = "施工关闭或禁止大巴停靠"
	}
	return &RuleResult{
		RuleID:     R02VirtualStationDisabled,
		Category:   domain.CatVirtualStationDisabled,
		IssueLevel: domain.IssueLevelP2,
		Analysis:   "上车/下车站点当前不可用（" + reason + "），500m 内无活跃站点",
		Script:     "非常抱歉，您选择的站点因" + reason + "暂时无法使用，附近暂无可替换站点，建议稍后重试或选择其他出行方式。",
		Actions: []domain.RecommendedAction{
			{ActionType: domain.ActionNotifyOps, ActionName: "通知运营核查站点状态"},
		},
	}, true
}

// ── R-03 班次满载 ─────────────────────────────────────────────────────────
func checkR03CapacityFull(in MatchInput, _ Thresholds) (*RuleResult, bool) {
	if in.Algo == nil {
		return nil, false
	}
	for _, filters := range in.Algo.FilterMatrix {
		for _, f := range filters {
			if f == domain.FilterCapacityFull {
				return &RuleResult{
					RuleID:     R03CapacityFull,
					Category:   domain.CatCapacityFull,
					IssueLevel: domain.IssueLevelP3,
					Analysis:   "该班次/线路预约座位已满（LockedSeats ≥ TotalSeats），算法无可用容量",
					Script:     "非常抱歉，您预约的班次座位已全部被预定，建议您尝试预约其他时段的班次。",
				}, true
			}
		}
	}
	return nil, false
}

// ── R-04 方向冲突 ─────────────────────────────────────────────────────────
func checkR04HeadingMismatch(in MatchInput, th Thresholds) (*RuleResult, bool) {
	if in.Algo == nil {
		return nil, false
	}
	hasMismatch := false
	for _, filters := range in.Algo.FilterMatrix {
		for _, f := range filters {
			if f == domain.FilterHeadingMismatch {
				hasMismatch = true
				break
			}
		}
	}
	if !hasMismatch && in.Algo.HeadingAngleDeg <= th.HeadingConflictDeg {
		return nil, false
	}
	return &RuleResult{
		RuleID:     R04HeadingMismatch,
		Category:   domain.CatAlgoHeadingMismatch,
		IssueLevel: domain.IssueLevelP3,
		Analysis:   "附近车辆行驶方向与用户目的地夹角超过阈值，掉头成本高于直行，算法判定方向冲突",
		Script:     "网约公交是全程动态匹配的。距您最近的车辆行驶方向与您的目的地相反，系统已为您匹配综合预计到达时间最短的方案，请耐心等候。",
	}, true
}

// ── R-05 在车乘客 ETA 保护 ────────────────────────────────────────────────
func checkR05EtaProtection(in MatchInput, th Thresholds) (*RuleResult, bool) {
	if in.Algo == nil {
		return nil, false
	}
	hasTrigger := false
	for _, filters := range in.Algo.FilterMatrix {
		for _, f := range filters {
			if f == domain.FilterEtaExceeded {
				hasTrigger = true
				break
			}
		}
	}
	if !hasTrigger && in.Algo.EtaDeltaMin <= th.EtaProtectionMin {
		return nil, false
	}
	return &RuleResult{
		RuleID:     R05EtaProtection,
		Category:   domain.CatEtaProtection,
		IssueLevel: domain.IssueLevelP3,
		Analysis:   "接入该订单将导致在车乘客 ETA 延误超过保护阈值，算法优先保障在途乘客体验",
		Script:     "系统在动态调度时会优先保障已在车乘客的准时到达。您的订单将在下一个合适的时机优先安排，感谢您的理解。",
	}, true
}

// ── R-06 司机 GPS 偏离 ────────────────────────────────────────────────────
func checkR06GPSDeviation(in MatchInput, th Thresholds) (*RuleResult, bool) {
	if in.GPS == nil {
		return nil, false
	}
	if in.GPS.DeviationMeters <= th.GPSDeviationM || in.GPS.DeviationDurationS <= th.GPSDeviationS {
		return nil, false
	}
	return &RuleResult{
		RuleID:     R06GPSDeviation,
		Category:   domain.CatDriverGPSDeviation,
		IssueLevel: domain.IssueLevelP2,
		Analysis:   "司机 GPS 偏离推荐路线超过 500m 且持续超过 3 分钟，判定为主观走错路",
		Script:     "非常抱歉给您带来不便，司机行驶路线出现偏差，我们已提醒司机尽快调整路线，预计稍有延误，感谢您的耐心等待。",
		Actions: []domain.RecommendedAction{
			{ActionType: domain.ActionNotifyOps, ActionName: "通知运营联系司机"},
		},
	}, true
}

// ── R-07 客观拥堵 ─────────────────────────────────────────────────────────
// TraceLog 中通过 ErrorMsg 携带路况信息（当前 Mock 实现，生产时对接路况 API）
func checkR07Congestion(in MatchInput, th Thresholds) (*RuleResult, bool) {
	if in.Trace == nil {
		return nil, false
	}
	for _, span := range in.Trace.ErrorSpans {
		if strings.Contains(span.ErrorMsg, "CONGESTION_INDEX") &&
			strings.Contains(span.ErrorMsg, "DEEP_RED") {
			return &RuleResult{
				RuleID:     R07TrafficCongestion,
				Category:   domain.CatTrafficCongestion,
				IssueLevel: domain.IssueLevelP3,
				Analysis:   "当前路段拥堵指数达到深红级别，实时路况导致行程延误",
				Script:     "由于当前路段遭遇严重拥堵，车辆正在实时绕行最优路线，感谢您的耐心，我们正在尽力缩短行程时间。",
			}, true
		}
	}
	return nil, false
}

// ── R-08 司机 App 切后台 ──────────────────────────────────────────────────
func checkR08DriverAppOffline(in MatchInput, _ Thresholds) (*RuleResult, bool) {
	if in.Order == nil || in.GPS == nil {
		return nil, false
	}
	// 判定：司机 GPS 显示在场站内，但距站点距离异常大（> 100m 表示 App 断连漂移）
	// 生产态：结合虚拟队列状态判断，此处为 MVP 简化版
	if in.GPS.DistanceToStationM > 100 && in.Order.Status == domain.OrderStatusDispatched {
		return &RuleResult{
			RuleID:     R08DriverAppOffline,
			Category:   domain.CatDriverGPSDeviation,
			IssueLevel: domain.IssueLevelP2,
			Analysis:   "司机 GPS 到站距离超过 100m，疑似 App 切后台导致 GPS 定位漂移或断连",
			Script:     "司机正在前往接驾，由于网络原因实时位置稍有延迟，请您在站点等候，司机到达后会主动联系您。",
			Actions: []domain.RecommendedAction{
				{ActionType: domain.ActionNotifyOps, ActionName: "通知运营提醒司机保持 App 前台"},
			},
		}, true
	}
	return nil, false
}

// ── R-09 营销工具不适用 ───────────────────────────────────────────────────
// 生产态需要接入营销规则服务，此处为 MVP 占位实现
func checkR09MarketingMismatch(_ MatchInput, _ Thresholds) (*RuleResult, bool) {
	// TODO(P1): 接入 GetMarketingRule 工具后实现
	return nil, false
}

// ── R-10 下游连接池耗尽（技术链路 504）────────────────────────────────────
func checkR10ConnectionPool(in MatchInput, _ Thresholds) (*RuleResult, bool) {
	if in.Trace == nil {
		return nil, false
	}
	for _, span := range in.Trace.ErrorSpans {
		if span.StatusCode == 504 || span.StatusCode == 500 {
			if strings.Contains(span.ErrorMsg, "connection pool") ||
				strings.Contains(span.ErrorMsg, "redis") ||
				strings.Contains(span.ErrorMsg, "timeout") {
				return &RuleResult{
					RuleID:     R10ConnectionPoolExhausted,
					Category:   domain.CatTechRPCError,
					IssueLevel: domain.IssueLevelP1,
					Analysis:   "Trace 下钻定位：" + span.ServiceName + " 服务连接池耗尽或超时（" + span.ErrorMsg + "）",
					Script:     "系统当前出现短暂的技术波动，我们的技术团队已在处理，请稍后重试，给您带来的不便深表歉意。",
					Actions: []domain.RecommendedAction{
						{
							ActionType:    domain.ActionTriggerJira,
							ActionName:    "自动创建 P1 Jira",
							ActionPayload: `{"jira_owner":"@infra","trace_id":"` + in.Trace.TraceID + `"}`,
						},
					},
				}, true
			}
		}
	}
	return nil, false
}
