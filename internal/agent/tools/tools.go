// Package tools 将公交业务只读工具注册为 Eino InvokableTool，
// 供 react.NewAgent 的 ToolsConfig 使用。
// LLM 在 ReAct 循环中自主决定调用哪些工具、调用几次，
// 而不是固定顺序全量扇出。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/buspulse/diagnose-agent/internal/domain"
	"github.com/buspulse/diagnose-agent/internal/gateway"
)

// All 返回所有已注册的 Eino 工具列表，注入 react.AgentConfig.ToolsConfig。
// sanitizer 用于在工具返回结果进入 LLM 上下文前脱敏压缩。
func All(san *gateway.Sanitizer) ([]einoTool.BaseTool, error) {
	builders := []func(*gateway.Sanitizer) (einoTool.InvokableTool, error){
		buildGetOrderContext,
		buildGetVehicleGPS,
		buildGetAlgoSnapshot,
		buildGetTraceLog,
		buildGetGeoFence,
		buildGetStationFlow,
		buildGetMapRoute,
		buildGetEtaSnapshot,
	}
	result := make([]einoTool.BaseTool, 0, len(builders))
	for _, b := range builders {
		t, err := b(san)
		if err != nil {
			return nil, fmt.Errorf("build tool: %w", err)
		}
		result = append(result, t)
	}
	return result, nil
}

// sanitizeAndMarshal 脱敏压缩后序列化为 JSON 字符串返回给 LLM。
// 工具函数统一通过此辅助函数处理输出，不在各工具内部重复脱敏逻辑。
func sanitizeAndMarshal(san *gateway.Sanitizer, toolName string, v interface{}) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal tool result: %w", err)
	}
	cleaned := san.Sanitize(toolName, raw, 1024)
	return string(cleaned), nil
}

// ── GetOrderContext ────────────────────────────────────────────────────────

type orderContextInput struct {
	OrderID string `json:"order_id" jsonschema_description:"需要查询状态的订单ID"`
	CityID  string `json:"city_id,omitempty" jsonschema_description:"城市ID，可选"`
}

func buildGetOrderContext(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetOrderContext",
		"查询订单基础上下文：订单状态（呼叫中/已派单/履约中/已完成/已取消）、"+
			"乘客信息（已脱敏）、司机/车辆ID、上下车站点、创建时间、24h取消次数（反作弊规则使用）",
		func(ctx context.Context, in *orderContextInput) (string, error) {
			// TODO(P1): 替换为真实订单服务 RPC
			// resp, err := orderClient.GetOrder(ctx, &pb.GetOrderReq{OrderId: in.OrderID})
			simulateLatency(ctx, 80*time.Millisecond)
			mock := domain.OrderContext{
				OrderID:          in.OrderID,
				UserID:           "usr_sha256_mock",
				DriverID:         "drv_001",
				VehicleID:        "veh_001",
				LineID:           "line_008",
				Status:           domain.OrderStatusDispatched,
				CityID:           in.CityID,
				PickupStationID:  "sta_101",
				DropoffStationID: "sta_205",
				CreateTime:       1783584000,
				DispatchTime:     1783584060,
				CancelCount24H:   0,
			}
			return sanitizeAndMarshal(san, "GetOrderContext", mock)
		},
	)
}

// ── GetVehicleGPS ──────────────────────────────────────────────────────────

type vehicleGPSInput struct {
	VehicleID string `json:"vehicle_id" jsonschema_description:"需要查询轨迹的车辆ID"`
}

func buildGetVehicleGPS(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetVehicleGPS",
		"查询车辆实时GPS轨迹快照（坐标已网格化脱敏）：当前位置、近3分钟轨迹、"+
			"偏离推荐路线距离（米）、持续偏离时间（秒）、距当前站点距离（米）。"+
			"用于判断：司机GPS偏离(R-06)、App切后台(R-08)",
		func(ctx context.Context, in *vehicleGPSInput) (string, error) {
			// TODO(P1): 替换为 GPS 服务 RPC，精确坐标在 sanitizer 中网格化
			simulateLatency(ctx, 120*time.Millisecond)
			mock := domain.VehicleGPS{
				VehicleID:          in.VehicleID,
				CurrentPosition:    domain.GeoPoint{GridLng: 116.407, GridLat: 39.904},
				RecentTrack:        []domain.GeoPoint{{GridLng: 116.405, GridLat: 39.902}},
				DeviationMeters:    120,
				DeviationDurationS: 45,
				DistanceToStationM: 85,
			}
			return sanitizeAndMarshal(san, "GetVehicleGPS", mock)
		},
	)
}

// ── GetAlgoSnapshot ────────────────────────────────────────────────────────

type algoSnapshotInput struct {
	OrderID string `json:"order_id" jsonschema_description:"需要查询算法快照的订单ID"`
}

func buildGetAlgoSnapshot(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetAlgoSnapshot",
		"查询算法过滤矩阵快照：周边车辆被过滤的原因列表（HEADING_MISMATCH方向冲突/ETA_EXCEEDED延误超限/CAPACITY_FULL满载等）、"+
			"方向夹角（度）、ETA延误（分钟）、在车乘客数。"+
			"这是诊断'有车分不上'类客诉的核心工具",
		func(ctx context.Context, in *algoSnapshotInput) (string, error) {
			// TODO(P1): 替换为算法服务 RPC
			simulateLatency(ctx, 200*time.Millisecond)
			mock := domain.AlgoSnapshot{
				OrderID: in.OrderID,
				FilterMatrix: map[string][]domain.FilterReason{
					"veh_002": {domain.FilterHeadingMismatch},
					"veh_003": {domain.FilterHeadingMismatch},
				},
				AssignedVehicleID: "veh_001",
				HeadingAngleDeg:   105,
				EtaDeltaMin:       8,
				PassengersOnBoard: 3,
			}
			return sanitizeAndMarshal(san, "GetAlgoSnapshot", mock)
		},
	)
}

// ── GetTraceLog ────────────────────────────────────────────────────────────

type traceLogInput struct {
	TraceID string `json:"trace_id" jsonschema_description:"全链路TraceID，从订单上下文或请求Header获取"`
}

func buildGetTraceLog(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetTraceLog",
		"查询全链路Trace摘要，仅返回异常Span（错误/超时）：服务名、操作名、耗时、状态码、错误信息。"+
			"用于诊断500/504技术链路故障，判断下游连接池耗尽、Redis超时等(R-10)",
		func(ctx context.Context, in *traceLogInput) (string, error) {
			// TODO(P1): 对接 Jaeger/SkyWalking，按 trace_id 拉取并过滤异常 Span
			simulateLatency(ctx, 150*time.Millisecond)
			mock := domain.TraceLog{
				TraceID:    in.TraceID,
				TotalSpans: 12,
				ErrorSpans: []domain.TraceSpan{},
				RootSpanMs: 320,
			}
			return sanitizeAndMarshal(san, "GetTraceLog", mock)
		},
	)
}

// ── GetGeoFence ────────────────────────────────────────────────────────────

type geoFenceInput struct {
	CityID string  `json:"city_id" jsonschema_description:"城市ID"`
	Lng    float64 `json:"lng" jsonschema_description:"用户请求经度（将被网格化处理）"`
	Lat    float64 `json:"lat" jsonschema_description:"用户请求纬度（将被网格化处理）"`
}

func buildGetGeoFence(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetGeoFence",
		"查询地理围栏状态：用户坐标是否在服务围栏内、当前是否在运营时段、运营时段描述。"+
			"用于诊断'金刚位无入口/无法发单'类客诉(R-01)",
		func(ctx context.Context, in *geoFenceInput) (string, error) {
			// TODO(P1): 对接围栏服务，多边形碰撞检测
			simulateLatency(ctx, 60*time.Millisecond)
			mock := domain.GeoFence{
				FenceID:              "fence_bj_001",
				CityID:               in.CityID,
				UserInsideFence:      true,
				WithinOperationHours: true,
				OperationHoursDesc:   "07:00-21:00",
			}
			return sanitizeAndMarshal(san, "GetGeoFence", mock)
		},
	)
}

// ── GetStationFlow ─────────────────────────────────────────────────────────

type stationFlowInput struct {
	StationID string `json:"station_id" jsonschema_description:"虚拟站点ID"`
}

func buildGetStationFlow(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetStationFlow",
		"查询虚拟站点状态：是否可用（0=关闭/1=正常）、关闭原因、500m内活跃站点数。"+
			"用于诊断虚拟站点失效类客诉(R-02)",
		func(ctx context.Context, in *stationFlowInput) (string, error) {
			// TODO(P1): 对接站点服务
			simulateLatency(ctx, 60*time.Millisecond)
			mock := domain.StationFlow{
				StationID:         in.StationID,
				Status:            domain.StationActive,
				NearbyActiveCount: 3,
			}
			return sanitizeAndMarshal(san, "GetStationFlow", mock)
		},
	)
}

// ── GetMapRoute ────────────────────────────────────────────────────────────

type mapRouteInput struct {
	OriginLng  float64 `json:"origin_lng" jsonschema_description:"起点经度"`
	OriginLat  float64 `json:"origin_lat" jsonschema_description:"起点纬度"`
	DestLng    float64 `json:"dest_lng" jsonschema_description:"终点经度"`
	DestLat    float64 `json:"dest_lat" jsonschema_description:"终点纬度"`
	VehicleType string `json:"vehicle_type,omitempty" jsonschema_description:"车型，如 bus（大巴）"`
}

func buildGetMapRoute(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetMapRoute",
		"查询路线规划与实时路况：拥堵指数（≥3.5为深红严重拥堵）、是否有大车限行路段。"+
			"用于诊断拥堵绕行(R-07)和大车导航限行(R-08)",
		func(ctx context.Context, in *mapRouteInput) (string, error) {
			// TODO(P1): 对接高德/百度地图 API
			simulateLatency(ctx, 180*time.Millisecond)
			result := map[string]interface{}{
				"distance_m":       8500,
				"estimated_time_s": 1200,
				"congestion_index": 1.8,
				"has_restriction":  false,
			}
			return sanitizeAndMarshal(san, "GetMapRoute", result)
		},
	)
}

// ── GetEtaSnapshot ─────────────────────────────────────────────────────────

type etaSnapshotInput struct {
	OrderID string `json:"order_id" jsonschema_description:"需要查询ETA的订单ID"`
}

func buildGetEtaSnapshot(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
	return utils.InferTool(
		"GetEtaSnapshot",
		"查询订单ETA快照：承诺到达时间、当前预估到达时间、延误秒数、附近可用车辆数。"+
			"用于诊断接驾超阈值(R-09)，附近车辆数=0说明时空断层",
		func(ctx context.Context, in *etaSnapshotInput) (string, error) {
			// TODO(P1): 对接 ETA 服务
			simulateLatency(ctx, 100*time.Millisecond)
			mock := domain.EtaSnapshot{
				OrderID:        in.OrderID,
				PromisedEtaS:   600,
				CurrentEtaS:    720,
				DeltaS:         120,
				NearbyVehicles: 4,
			}
			return sanitizeAndMarshal(san, "GetEtaSnapshot", mock)
		},
	)
}

// simulateLatency 模拟网络延迟，正确响应 ctx.Done。
// 生产替换为真实 RPC 后此函数即可删除。
func simulateLatency(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
