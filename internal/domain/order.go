package domain

// OrderStatus 订单状态枚举
type OrderStatus string

const (
	OrderStatusCalling    OrderStatus = "CALLING"    // 呼叫中
	OrderStatusDispatched OrderStatus = "DISPATCHED" // 已派单
	OrderStatusOnRoute    OrderStatus = "ON_ROUTE"   // 履约中（司机接驾/乘客在车）
	OrderStatusCompleted  OrderStatus = "COMPLETED"  // 已完成
	OrderStatusCancelled  OrderStatus = "CANCELLED"  // 已取消
)

// OrderContext 订单基础上下文（GetOrderContext 工具输出）
type OrderContext struct {
	OrderID     string      `json:"order_id"`
	UserID      string      `json:"user_id"`    // 已脱敏（SHA256 混淆）
	DriverID    string      `json:"driver_id"`
	VehicleID   string      `json:"vehicle_id"`
	LineID      string      `json:"line_id"`
	Status      OrderStatus `json:"status"`
	CityID      string      `json:"city_id"`
	// 上下车站点 ID
	PickupStationID  string `json:"pickup_station_id"`
	DropoffStationID string `json:"dropoff_station_id"`
	// 时间戳（Unix 秒）
	CreateTime     int64 `json:"create_time"`
	DispatchTime   int64 `json:"dispatch_time,omitempty"`
	CompleteTime   int64 `json:"complete_time,omitempty"`
	CancelTime     int64 `json:"cancel_time,omitempty"`
	// 取消次数（24h 内，反作弊规则 R-02 使用）
	CancelCount24H int `json:"cancel_count_24h"`
}

// GeoPoint 已网格化模糊处理的地理坐标（精度 ~100m）
type GeoPoint struct {
	GridLng float64 `json:"grid_lng"` // 精确到小数点后 3 位
	GridLat float64 `json:"grid_lat"`
}

// VehicleGPS 车辆 GPS 轨迹快照（GetVehicleGPS 工具输出）
type VehicleGPS struct {
	VehicleID       string   `json:"vehicle_id"`
	CurrentPosition GeoPoint `json:"current_position"`
	// 最近 3 分钟轨迹（网格化，最多 10 个点，防止撑爆 LLM 上下文）
	RecentTrack        []GeoPoint `json:"recent_track,omitempty"`
	DeviationMeters    float64    `json:"deviation_meters"`    // 偏离推荐路线距离（米）
	DeviationDurationS int        `json:"deviation_duration_s"` // 持续偏离时间（秒）
	// 距当前派单站点的距离（米），司机到站流转规则 R-03 使用
	DistanceToStationM float64 `json:"distance_to_station_m"`
}

// FilterReason 算法过滤原因
type FilterReason string

const (
	FilterHeadingMismatch   FilterReason = "HEADING_MISMATCH"    // 方向冲突（R-04）
	FilterEtaExceeded       FilterReason = "ETA_EXCEEDED"        // ETA 超保护阈值（R-05）
	FilterCapacityFull      FilterReason = "CAPACITY_FULL"       // 容量满载（R-03）
	FilterDelayTimeExceeded FilterReason = "DELAY_TIME_EXCEEDED" // 时延超限
	FilterDriverOffline     FilterReason = "DRIVER_OFFLINE"      // 司机离线
)

// AlgoSnapshot 算法策略快照（GetAlgoSnapshot 工具输出）
// 仅保留含 FILTER/TIMEOUT/MISMATCH/LIMIT_EXCEEDED 的关键行（≤1KB）
type AlgoSnapshot struct {
	OrderID string `json:"order_id"`
	// 过滤矩阵：key=车辆ID，value=被过滤原因列表
	FilterMatrix map[string][]FilterReason `json:"filter_matrix,omitempty"`
	// 最终派单车辆（若有）
	AssignedVehicleID string `json:"assigned_vehicle_id,omitempty"`
	// 方向夹角（度），>90° 触发 R-04
	HeadingAngleDeg float64 `json:"heading_angle_deg,omitempty"`
	// 预估 ETA 延误（分钟），>15 触发 R-05
	EtaDeltaMin float64 `json:"eta_delta_min,omitempty"`
	// 在车乘客数
	PassengersOnBoard int `json:"passengers_on_board,omitempty"`
}

// TraceSpan 链路追踪单个 Span 摘要
type TraceSpan struct {
	SpanID      string `json:"span_id"`
	ServiceName string `json:"service_name"`
	OperationName string `json:"operation_name"`
	DurationMs  int64  `json:"duration_ms"`
	StatusCode  int    `json:"status_code"` // HTTP 状态码或 gRPC code
	ErrorMsg    string `json:"error_msg,omitempty"`
}

// TraceLog 全链路 Trace 摘要（GetTraceLog 工具输出，仅保留异常 Span）
type TraceLog struct {
	TraceID     string      `json:"trace_id"`
	TotalSpans  int         `json:"total_spans"`
	ErrorSpans  []TraceSpan `json:"error_spans"` // 仅错误/超时 Span，数量≤10
	RootSpanMs  int64       `json:"root_span_ms"` // 根 Span 总耗时
}

// GeoFence 地理围栏状态（GetGeoFence 工具输出）
type GeoFence struct {
	FenceID     string `json:"fence_id"`
	CityID      string `json:"city_id"`
	// 用户请求坐标是否在围栏内
	UserInsideFence bool `json:"user_inside_fence"`
	// 当前是否在运营时段内
	WithinOperationHours bool   `json:"within_operation_hours"`
	OperationHoursDesc   string `json:"operation_hours_desc,omitempty"` // 例："07:00-21:00"
}

// StationStatus 站点状态
type StationStatus int

const (
	StationActive   StationStatus = 1
	StationInactive StationStatus = 0 // 施工关闭/禁停，触发 R-02
)

// StationFlow 虚拟站点状态（GetStationFlow 工具输出）
type StationFlow struct {
	StationID   string        `json:"station_id"`
	Status      StationStatus `json:"status"`
	// 距用户最近 500m 内的活跃站点数（R-02 使用）
	NearbyActiveCount int    `json:"nearby_active_count"`
	InactiveReason    string `json:"inactive_reason,omitempty"` // 关闭原因
}

// EtaSnapshot ETA 快照（GetEtaSnapshot 工具输出）
type EtaSnapshot struct {
	OrderID       string  `json:"order_id"`
	PromisedEtaS  int     `json:"promised_eta_s"`  // 承诺到达时间（秒）
	CurrentEtaS   int     `json:"current_eta_s"`   // 当前预估到达时间（秒）
	DeltaS        int     `json:"delta_s"`         // 延误秒数（正数=延误）
	NearbyVehicles int    `json:"nearby_vehicles"` // 附近可用车辆数（R-09 使用）
}
