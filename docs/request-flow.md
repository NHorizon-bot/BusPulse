# BusPulse 诊断请求全链路详解

> 本文档基于 `cmd/server/main.go` 入口，逐层追踪一次 `POST /api/v1/diagnose` 请求的完整生命周期。

---

## 目录

1. [请求链路全景图](#1-请求链路全景图)
2. [第 1 层：HTTP 中间件链](#2-第-1-层http-中间件链)
3. [第 2 层：Handler 层 — 请求处理与审计](#3-第-2-层handler-层--请求处理与审计)
4. [第 3 层：DiagAgent — 诊断执行入口](#4-第-3-层diagagent--诊断执行入口)
5. [第 4 层：规则引擎前置（零 Token 成本）](#5-第-4-层规则引擎前置零-token-成本)
6. [第 5 层：Eino ReAct 推理循环](#6-第-5-层eino-react-推理循环)
7. [第 6 层：工具层（只读 RPC）](#7-第-6-层工具层只读-rpc)
8. [第 7 层：脱敏与摘要网关](#8-第-7-层脱敏与摘要网关)
9. [第 8 层：输出解析与降级兜底](#9-第-8-层输出解析与降级兜底)
10. [第 9 层：审计日志落盘](#10-第-9-层审计日志落盘)
11. [请求响应全流程时序图](#11-请求响应全流程时序图)
12. [关键配置项速查](#12-关键配置项速查)

---

## 1. 请求链路全景图

```
客户端（curl/工单系统/Link机器人）
    │
    │ POST /api/v1/diagnose  {"order_id":"test_001","city_id":"bj",...}
    ▼
┌─────────────────────────────────────────────────────────────────────┐
│  第 1 层：HTTP 中间件链（由外到内）                                  │
│    ┌──────────────────────────────────────────────────────────────┐ │
│    │ ① Recover（panic 兜底 → 500）                               │ │
│    │ ② Timeout（30s 全局超时 → 503）                              │ │
│    │ ③ AuditContext（提取 X-Operator-Id / X-Trigger-Source 到 ctx) │ │
│    └──────────────────────────────────────────────────────────────┘ │
│                                    │                                │
│                                    ▼                                │
│  ┌─ 第 2 层：DiagnoseHandler ─────────────────────────────────────┐ │
│  │  ① JSON 反序列化 & 参数校验                                     │ │
│  │  ② 调用 DiagAgent.Diagnose(ctx, DiagnosticRequest)              │ │
│  │  ③ 返回后写审计日志（goroutine 异步）                            │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                    │                                │
│                                    ▼                                │
│  ┌─ 第 3~6 层：DiagAgent（推理与控制层）───────────────────────────┐ │
│  │  ① 规则引擎前置（R-01~R-10，零 Token 成本）                     │ │
│  │     ┌─ 命中 → 输出确定性结论（Confidence=1.0，NeedsReview=false）│ │
│  │     └─ 未命中 → 激活 LLM ReAct 循环                              │ │
│  │  ② Eino react.NewAgent（内置 ReAct）                            │ │
│  │     Think(LLM) → Act(调工具) → Observe(工具结果) → 循环⋯         │ │
│  │     直到 LLM 不再发 tool_call 或达到 MaxIterations=4 熔断        │ │
│  │  ③ 解析 LLM 输出（三层 JSON 防御）→ fallback 兜底                │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                    │                                │
│                                    ▼                                │
│  ┌─ 第 7 层：脱敏与摘要网关 ───────────────────────────────────────┐ │
│  │  工具输出进入 LLM 前：手机号→[PHONE]，身份证→[ID_CARD]，GPS→截断 │ │
│  └────────────────────────────────────────────────────────────────┘ │
│                                    │                                │
│                                    ▼                                │
│  ┌─ 第 8 层：审计日志落盘 ─────────────────────────────────────────┐ │
│  │  audit.jsonl 追加一行 JSON                                      │ │
│  └────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
                HTTP Response 200  {"audit_id":"aud_xxx","report":{...}}
```

---

## 2. 第 1 层：HTTP 中间件链

### 文件：`internal/handler/middleware/recover.go`

**职责**：捕获 handler 中 panic，返回 500 而非进程崩溃。

```go
func Recover(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                // 返回 {"error":"internal server error","code":"PANIC_RECOVERED"}
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

- 最外层保护，任何 panic 不会让 Go 进程退出的最后防线
- 返回 `503 Service Unavailable`（注意：不是 500！代码硬编码了 500 但注释写的是 recover，实际写成 500 了）

### 文件：`internal/handler/middleware/timeout.go`

**职责**：注入全局诊断超时 context，超时后返回 503 并释放资源。

```go
func Timeout(d time.Duration) func(http.Handler) http.Handler {
    // 创建 WithTimeout context → 独立 goroutine 执行 handler
    // select 监听 ctx.Done()：
    //   超时 → 写 503 "DIAGNOSE_TIMEOUT"
    //          → 等待 handler goroutine 退出（防泄漏）
}
```

关键细节：
- 超时值来自 `config.yaml` → `server.diagnose_timeout_ms`，当前 **30000ms**
- 超时后 handler goroutine 不会立即中断——**它仍在后台运行直到自己完成或 ctx.Done()**，但客户端已经收到了 503
- 超时后 ReAct 循环中 DeepSeek API 调用会因为 `ctx.DeadlineExceeded` 而失败，`agent.Diagnose()` 返回降级报告给 handler，但这时 response header 已经被中间件写了，所以降级报告**写入响应但不被客户端收到**（看之前的实际输出：同时收到 503 和降级 JSON，说明存在 race condition）

### 文件：`internal/handler/middleware/audit.go`

**职责**：从 HTTP Header 提取审计元数据注入 context。

| Header | Context Key | 用途 |
|--------|------------|------|
| `X-Operator-Id` | `operator_id` | 触发人标识（客服工号/飞书账号） |
| `X-Trigger-Source` | `trigger_source` | 触发来源（WIDGET/FEISHU_CARD/ALERT/API） |

---

## 3. 第 2 层：Handler 层 — 请求处理与审计

### 文件：`internal/handler/diagnose.go`

`DiagnoseHandler` 实现 `ServeHTTP`，是整个请求的核心编排入口。

#### 3.1 请求参数

```json
{
  "order_id":            "test_001",   // 必需，订单号
  "city_id":             "bj",         // 可选，城市ID
  "trace_id":            "trc_abc123", // 可选，全链路 TraceID
  "free_text_context":   "用户投诉..." // 可选，自然语言描述
}
```

#### 3.2 执行步骤

```
Step 1: 校验 Method → 仅接受 POST，否则返回 405
Step 2: JSON 反序列化 → 失败返回 400 "INVALID_JSON"
Step 3: 校验 order_id 非空 → 为空返回 400 "MISSING_ORDER_ID"
Step 4: 从 context 提取 operator_id / trigger_source（来自中间件）
Step 5: 组装 domain.DiagnosticRequest
Step 6: 调用 diagnoser.Diagnose(ctx, req) → 返回 DiagnosticReport
Step 7: 计算耗时 elapsed = time.Since(start).Milliseconds()
Step 8: 兜底：report == nil 时构建 UNKNOWN 降级报告
Step 9: 异步写审计日志（goroutine，不阻塞响应）
Step 10: 返回 200 {"audit_id","report","duration_ms"}
```

关键设计：
- **降级兜底**：DiagAgent.Diagnose 保证永远返回非 nil report（内部有 fallback），handler 层还有第二层兜底
- **异步审计**：`go func() { auditLogger.Write(...) }()` — 审计日志写失败不影响响应速度
- **context 传递**：中间件注入的 operator_id / trigger_source 通过 `r.Context()` 传递到 handler

---

## 4. 第 3 层：DiagAgent — 诊断执行入口

### 文件：`internal/agent/agent.go`

`DiagAgent` 是整个推理控制层的核心容器：

```go
type DiagAgent struct {
    ruleEngine *rule.Engine   // 规则引擎（前置分流）
    reactAgent *react.Agent   // Eino ReAct 循环（LLM 驱动）
}
```

#### Agent 启动时初始化链路

`cmd/server/main.go` 中 `NewDiagAgent()` 的依赖注入顺序：

```
main()
    │
    ├─ config.Load("configs/config.yaml", "configs/config.local.yaml")
    │     → 加载基础配置 + 本地覆盖配置（api_key 等在 local 中）
    │     → applyDefaults 填零值
    │
    ├─ audit.NewFileLogger(cfg.Audit.FilePath)
    │     → 创建 JSONL 追加写入器
    │
    ├─ gateway.New(cfg.Sanitizer.MaxPayloadBytes)
    │     → 脱敏网关（手机号/身份证/GPS → 替换 + 截断）
    │
    ├─ rule.NewEngine(ruleThresholds)
    │     → 规则引擎（注入阈值）
    │
    ├─ agent.NewDiagAgent(ctx, cfg, ruleEngine, san)
    │     │
    │     ├─ buildChatModel(ctx, &cfg.LLM)
    │     │     → DeepSeek: einoDeepSeek.NewChatModel(APIKey, Model, BaseURL)
    │     │        Qwen:    einoOpenAI.NewChatModel(APIKey, Model, BaseURL)
    │     │
    │     ├─ tools.All(san)
    │     │     → 注册 8 个只读工具（InferTool 自动推断 JSON Schema）
    │     │
    │     ├─ react.NewAgent(ctx, &react.AgentConfig{
    │     │     ToolCallingModel: chatModel,
    │     │     ToolsConfig:    8个BaseTool,
    │     │     MessageModifier: SystemPrompt（SOP字典冷启动注入）,
    │     │     MaxStep:        maxIterations*2 + 2,
    │     │ })
    │     │
    │     └─ return &DiagAgent{ruleEngine, reactAgent}
    │
    ├─ handler.NewDiagnoseHandler(diagAgent, auditLogger)
    └─ http.ListenAndServe(":8080", mux)
```

#### MaxStep 计算

```go
maxStep := cfg.LLM.MaxIterations*2 + 2  // 默认 MaxIterations=4 → maxStep=10
// 每轮 ReAct 含 ChatModel 1步 + ToolsNode 1步 = 2步
// + 初始步骤 + 最终输出步骤 = 2
// 所以 4轮 = 4*2+2 = 10步
```

---

## 5. 第 4 层：规则引擎前置（零 Token 成本）

### 文件：`internal/rule/engine.go`、`internal/rule/rules.go`、`internal/rule/types.go`

> **注意**：当前代码中，`DiagAgent.Diagnose()` **直接调用了 `reactAgent.Generate()`**，未执行规则引擎前置分流。`ruleEngine` 虽然已注入但**未实际使用**。以下为架构设计与实际代码的对照。

#### 架构设计（Smart_Diagnose_Agent_Research_Report.md）

```
故障输入
    │
    ▼
┌───────────────────────────────┐
│  规则引擎诊断（前置）            │
│  R-01~R-10 确定性秒杀           │
│  零 Token 成本、零幻觉           │
└───────────┬───────────────────┘
            │
   ┌────────┴────────────┐
   ▼                     ▼
[规则已覆盖]          [规则无法解释]
    │                     │
    ▼                     ▼
直接输出结论         激活 LLM ReAct
```

#### 规则清单（R-01 ~ R-10）

| ID | 场景 | 判定特征 | 级别 | 结论 |
|----|------|---------|------|------|
| R-01 | GPS 超区下单 | `Fence.UserInsideFence = false` 或非运营时段 | P3 | GEO_FENCE_OUT |
| R-02 | 虚拟站点失效 | `Station.Status = 0` 且 500m 内无活跃站点 | P2 | VIRTUAL_STATION_DISABLED |
| R-03 | 班次满载 | `FilterMatrix` 含 `CAPACITY_FULL` | P3 | CAPACITY_FULL |
| R-04 | 方向冲突 | `HeadingAngleDeg > 90°` | P3 | ALGO_HEADING_MISMATCH |
| R-05 | ETA 保护 | `FilterMatrix` 含 `ETA_EXCEEDED` 且 `EtaDeltaMin > 15` | P3 | ETA_PROTECTION |
| R-06 | GPS 偏离 | `DeviationMeters > 500` 且 `DeviationDurationS > 180` | P2 | DRIVER_GPS_DEVIATION |
| R-07 | 客观拥堵 | Trace ErrorSpan 含 `CONGESTION_INDEX` + `DEEP_RED` | P3 | TRAFFIC_CONGESTION |
| R-08 | App 切后台 | `DistanceToStationM > 100` 且订单已派单 | P2 | DRIVER_GPS_DEVIATION |
| R-09 | 营销不适用 | TODO（占位，永远返回 false） | — | — |
| R-10 | 连接池耗尽 | ErrorSpan 含 5xx + `connection pool`/`redis`/`timeout` | P1 | TECH_RPC_ERROR |

#### 规则执行顺序

```
R-10(技术) → R-01(准入) → R-02(准入) → R-03(派单) → R-04(派单)
→ R-05(派单) → R-06(履约) → R-07(履约) → R-08(场站) → R-09(费用)
```

引擎短路——首个命中即停止。

规则命中时输出的 `RuleResult` 被转换为 `DiagnosticReport`：
- `Confidence = 1.0`（确定性结论）
- `NeedsReview = false`
- 零 Token 成本（不调用 LLM）

---

## 6. 第 5 层：Eino ReAct 推理循环

### 文件：`internal/agent/agent.go` + `internal/agent/prompt.go`

当规则引擎未命中时（或当前代码直接走这条路），激活 LLM 驱动的 ReAct 循环。

#### System Prompt（冷启动注入）

文件 `internal/agent/prompt.go` 中，`BuildSystemPrompt()` 返回一个固定 System Prompt，包含：

1. **核心约束**：只做诊断不做修复、工具优先、严格 JSON 输出、大白话原则
2. **输出 JSON Schema**：定义 `issue_level` / `root_cause_category` / `root_cause_analysis` / `customer_service_script` / `recommended_actions`
3. **优先级定义**：P1(技术故障) / P2(策略拦截) / P3(运营配置)
4. **诊断工具使用指引**：按客诉类型推荐优先工具

| 客诉类型 | 优先调用工具 |
|---------|------------|
| 金刚位无入口/无法发单 | GetGeoFence → GetStationFlow |
| 有车分不上/无车可派 | GetOrderContext → GetAlgoSnapshot |
| 技术报错 500/504 | GetOrderContext → GetTraceLog |
| 接驾迟到/绕路 | GetVehicleGPS → GetMapRoute → GetEtaSnapshot |
| 费用异常/退款 | GetOrderContext |
| 司机到站无法流转 | GetVehicleGPS |

5. **SOP 排障字典（9 大场景）**：R-01 到 R-10 的触发条件、话术、推荐动作

#### User Message 构建

```go
func buildUserMessage(req domain.DiagnosticRequest) *schema.Message {
    content := fmt.Sprintf("请诊断以下公交订单问题：\n订单号：%s\n城市：%s", req.OrderID, req.CityID)
    if req.FreeTextContext != "" {
        content += fmt.Sprintf("\n客服描述：%s", req.FreeTextContext)
    }
    if req.TraceID != "" {
        content += fmt.Sprintf("\nTraceID：%s（如有技术报错请调用 GetTraceLog）", req.TraceID)
    }
    return schema.UserMessage(content)
}
```

#### ReAct 循环执行过程

Eino 的 `react.NewAgent` 封装了标准 ReAct 循环：

```
┌─── [1] LLM 思考（Think）
│    输入：System Prompt + User Message + 历史消息
│    输出：tool_calls 或 最终回答
│
├─── [2] 执行工具（Act）
│    输入：tool_calls → 路由到对应的 BaseTool
│    过程：多个 tool_call 可在一轮中并发执行
│    输出：工具返回结果（已脱敏）
│
├─── [3] 观察结果（Observe）
│    工具结果包装为 Tool Role Message 返回给 LLM
│
└─── 回到 [1] 直到 LLM 不再产生 tool_call 或达到 MaxStep
```

**循环熔断**：
- `MaxIterations=4` → `MaxStep=10`
- 超过步数后强制退出，走 fallback 降级
- 全局诊断超时 30s（HTTP middleware Timeout）

**多工具并发**：LLM 可以在同一轮发起多个 `tool_call`，Eino 框架会并发执行这些工具，结果一起打包返回（不消耗额外 LLM 轮次）。

#### 模型包装日志（新增）

`loggingChatModel` 包装了底层 ChatModel，将每轮 ReAct 循环打印到控制台：

```
══════════ ReAct 第 1 轮 ── LLM 收到以下消息 ══════════
👤 [User] 请诊断以下公交订单问题：订单号：test_001 城市：bj
🧠 [LLM→工具] GetOrderContext({"order_id":"test_001"})
🧠 [LLM→工具] GetAlgoSnapshot({"order_id":"test_001"})
────────────────── 等待工具返回结果后进入下一轮 ──────────────────

══════════ ReAct 第 2 轮 ── LLM 收到以下消息 ══════════
📦 [ToolResult-GetOrderContext] {"order_id":"test_001","status":"DISPATCHED",...}
📦 [ToolResult-GetAlgoSnapshot] {"filter_matrix":{...},"heading_angle_deg":105,...}
✅ [LLM 最终结论] {"issue_level":"P3","root_cause_category":"ALGO_HEADING_MISMATCH"...}
```

---

## 7. 第 6 层：工具层（只读 RPC）

### 文件：`internal/agent/tools/tools.go`

8 个工具，全部注册为 `einoTool.InvokableTool`（通过 `utils.InferTool` 自动推断 JSON Schema）。

| 工具名称 | 输入 | 输出结构 | 模拟延迟 | 对接状态 |
|---------|------|---------|---------|---------|
| `GetOrderContext` | order_id, city_id | `OrderContext`（订单状态/司机/车辆/站点） | 80ms | Mock（TODO: 订单服务） |
| `GetVehicleGPS` | vehicle_id | `VehicleGPS`（坐标/轨迹/偏离度） | 120ms | Mock（TODO: GPS 服务） |
| `GetAlgoSnapshot` | order_id | `AlgoSnapshot`（过滤矩阵/方向角/ETA） | 200ms | Mock（TODO: 算法服务） |
| `GetTraceLog` | trace_id | `TraceLog`（异常 Span 摘要） | 150ms | Mock（TODO: Jaeger） |
| `GetGeoFence` | city_id, lng, lat | `GeoFence`（围栏/运营时段） | 60ms | Mock（TODO: 围栏服务） |
| `GetStationFlow` | station_id | `StationFlow`（站点状态/活跃数） | 60ms | Mock（TODO: 站点服务） |
| `GetMapRoute` | 起终点坐标 | 距离/耗时/拥堵指数/限行 | 180ms | Mock（TODO: 地图 API） |
| `GetEtaSnapshot` | order_id | `EtaSnapshot`（承诺ETA/当前ETA/延误） | 100ms | Mock（TODO: ETA 服务） |

#### 工具实现模式

每个工具统一遵循以下模式：

```go
func buildGetXxx(san *gateway.Sanitizer) (einoTool.InvokableTool, error) {
    return utils.InferTool(
        "GetXxx",                       // 工具名称（LLM 通过此名调用）
        "工具描述（给 LLM 看的）",       // 触发 LLM 决策的语义信息
        func(ctx context.Context, in *Input) (string, error) {
            simulateLatency(ctx, 100*time.Millisecond)  // 模拟网络延迟
            mock := domain.Xxx{...}                     // Mock 数据
            return sanitizeAndMarshal(san, "GetXxx", mock) // 脱敏 + 序列化
        },
    )
}
```

#### 工具描述设计要点

每个工具的 `description` 包含了**触发场景**和**联动规则**信息，帮助 LLM 判断何时调用：

```go
// GetAlgoSnapshot 的例子：
"查询算法过滤矩阵快照...这是诊断'有车分不上'类客诉的核心工具"

// GetVehicleGPS 的例子：
"查询车辆实时GPS轨迹快照...用于判断：司机GPS偏离(R-06)、App切后台(R-08)"
```

#### simulateLatency 函数

```go
func simulateLatency(ctx context.Context, d time.Duration) {
    select {
    case <-ctx.Done():      // 优先响应取消
    case <-time.After(d):   // 正常延迟
    }
}
```

生产替换为真实 RPC 后此函数删除。

---

## 8. 第 7 层：脱敏与摘要网关

### 文件：`internal/gateway/sanitizer.go`

工具返回结果在**进入 LLM 上下文之前**，必须经过脱敏与截断处理。

#### 脱敏规则

| 敏感类型 | 正则匹配 | 替换为 |
|---------|---------|--------|
| 手机号 | `1[3-9]\d{9}`（11 位数字） | `[PHONE]` |
| 身份证 | `\d{17}[\dXx]`（18 位） | `[ID_CARD]` |
| 精确 GPS | `-?\d{1,3}\.\d{6,}`（小数点后 6 位+） | `[GEO_GRID]` |

#### 截断策略

```go
func truncate(data []byte, maxBytes int) []byte {
    // 超过 maxPayloadBytes（默认 1024）时截断
    // 末尾追加 "...(truncated)" 标记
}
```

默认 `maxPayloadBytes = 1024`，超大 JSON 被截断为 ~1KB 特征摘要。截断在 PII 擦除之后做，防止敏感信息正好落在截断边界处逃逸。

#### Sanitize 调用链路

```
工具 mock 函数 → json.Marshal(v) → san.Sanitize(toolName, raw, 1024)
    → erasePII(raw)          ← 手机号/身份证/GPS 替换
    → truncate(cleaned, 1024) ← 截断至 ~1KB
    → string(cleaned)          ← 返回给 LLM
```

---

## 9. 第 8 层：输出解析与降级兜底

### 文件：`internal/agent/agent.go`

LLM 的原始输出经过三层解析防御链：

```go
func parseReport(raw string) (*domain.DiagnosticReport, error) {
    // 第 1 层：剥离 Markdown code block ```json ... ```
    // 第 2 层：提取第一个平衡的 { } JSON 对象（处理截断 + 前后多余文字）
    // 第 3 层：Unmarshal + 字段校验
    //   - root_cause_analysis 非空校验
    //   - issue_level 枚举归一化（非法值→P2）
    //   - root_cause_category 兜底（空→UNKNOWN）
}
```

#### 三层 JSON 解析防御（pkg/jsonutil/extract.go）

```go
func ExtractJSON(raw string) ([]byte, error) {
    // Layer 1: 剥离 markdown code block
    //   ```json\n{...}\n``` → {...}
    // Layer 2: 提取第一个完整 JSON 对象
    //   深度遍历大括号平衡，处理截断（如 { ... 后半部分丢失）
    //   也处理输出前后有多余自然语言的情况
    // 若仍失败，返回描述性 error
}
```

#### 降级兜底（fallbackReport）

当 LLM 失败或 JSON 解析失败时，不走重试，直接输出降级报告：

```go
func fallbackReport(req, reason) *DiagnosticReport {
    return &DiagnosticReport{
        IssueLevel:        P2,
        RootCauseCategory: UNKNOWN,
        RootCauseAnalysis: "自动诊断失败：" + reason,
        CustomerServiceScript: "非常抱歉...将由专员跟进处理",
        RecommendedActions: [{MANUAL_REVIEW, "转人工复核"}],
        Confidence:        0.0,
        NeedsReview:       true,
        ReviewReason:      reason,
    }
}
```

触发降级的场景：
1. `reactAgent.Generate()` 返回 error（LLM API 超时/鉴权/网络错误）
2. `parseReport()` 解析失败（JSON 格式问题/字段缺失）
3. 报告为 nil（handler 层第二层兜底）

---

## 10. 第 9 层：审计日志落盘

### 文件：`internal/audit/logger.go`

每笔诊断结束后（无论成功/失败），以 goroutine 异步写入 `logs/audit.jsonl`。

#### 审计记录结构

```json
{
  "audit_id": "aud_1783932540_test_001",
  "operator_id": "xiaoming_v",
  "trigger_source": "WIDGET",
  "order_id": "test_001",
  "timestamp": 1783932540,
  "agent_tools_invoked": [],
  "diag_duration_ms": 10938,
  "llm_raw_digest": "",
  "final_diagnostic_report": { "...": "..." },
  "human_action": "PENDING",
  "human_correction_note": "",
  "error": ""
}
```

| 字段 | 来源 | 说明 |
|------|------|------|
| `audit_id` | `aud_<Unix秒>_<orderID前8位>` | 唯一标识，可回溯 |
| `operator_id` | HTTP Header `X-Operator-Id` | 触发人 |
| `trigger_source` | HTTP Header `X-Trigger-Source` | WIDGET/FEISHU_CARD/ALERT/API |
| `diag_duration_ms` | handler 层 `time.Since(start)` | 全链路耗时 |
| `final_diagnostic_report` | 降级后的 report | 无论成功失败都有 |
| `human_action` | 初始值 PENDING | 人工复核后改为 CONFIRMED/REJECTED |
| `error` | 仅在失败时填充 | LLM 报错/解析错误 |

#### 审计 ID 生成

```go
func NewAuditID(orderID string) string {
    return fmt.Sprintf("aud_%d_%.8s", time.Now().Unix(), orderID)
}
// 示例：aud_1783932540_test_001
```

#### 写模式

- 文件格式：**JSONL**（每行一个 JSON 对象，方便 `grep`/`jq` 分析）
- 写入方式：`*json.Encoder.Encode()` 追加写入
- 线程安全：`sync.Mutex` 保护
- 异步写入：handler 中 `go func() { auditLogger.Write(...) }()` 不阻塞响应

---

## 11. 请求响应全流程时序图

```
Client                    HTTP Middleware              Handler              DiagAgent              LLM/DeepSeek            Tools
  │                            │                         │                     │                       │                    │
  │  POST /api/v1/diagnose     │                         │                     │                       │                    │
  │ ──────────────────────────►│                         │                     │                       │                    │
  │                            │                         │                     │                       │                    │
  │  ═══ Recover(panic→500) ═══►                         │                     │                       │                    │
  │  ═══ Timeout(30s→503) ══════►                        │                     │                       │                    │
  │  ═══ AuditContext(Header→ctx) ═══►                  │                     │                       │                    │
  │                            │                         │                     │                       │                    │
  │                            │  ServeHTTP(w, r)        │                     │                       │                    │
  │                            │ ──────────────────────►│                     │                       │                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │  Diagnose(ctx, req) │                       │                    │
  │                            │                         │ ──────────────────►│                       │                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │                     │  (规则引擎未命中后)     │                    │
  │                            │                         │                     │  Generate(ctx, msgs)  │                    │
  │                            │                         │                     │ ─────────────────────►│                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │                     │  ═══ ReAct 第 1 轮 ════►                    │
  │                            │                         │                     │  tool_call [tools...]  │                    │
  │                            │                         │                     │ ◄─────────────────────│                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │                     │  并发调用 2~8 个工具   │                    │
  │                            │                         │                     │ ───────────────────────────────────────────►│
  │                            │                         │                     │  (脱敏后)工具返回结果  │                    │
  │                            │                         │                     │ ◄────────────────────────────────────────────│
  │                            │                         │                     │                       │                    │
  │                            │                         │                     │  ═══ ReAct 第 2 轮 ════►                    │
  │                            │                         │                     │  (观察结果→更多工具)   │                    │
  │                            │                         │                     │  ◄─────────────────────│                    │
  │                            │                         │                     │  ⋯                     │                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │                     │  ═══ 最终轮 ═══════════►                    │
  │                            │                         │                     │  (无 tool_call, 输出)  │                    │
  │                            │                         │                     │ ◄─────────────────────│                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │                     │  parseReport(JSON)     │                    │
  │                            │                         │                     │  (三层防御链)           │                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │  ◄──────────────────│                       │                    │
  │                            │                         │  (report, err)      │                       │                    │
  │                            │                         │                     │                       │                    │
  │                            │                         │  Audit(异步 goroutine)                       │                    │
  │                            │                         │  auditLogger.Write() ───► logs/audit.jsonl    │                    │
  │                            │                         │                     │                       │                    │
  │  HTTP 200 {"audit_id",...} ◄─────────────────────────│                     │                       │                    │
  │ ◄──────────────────────────│                         │                     │                       │                    │
```

---

## 12. 关键配置项速查

| 配置路径 | 默认值 | 说明 |
|---------|--------|------|
| `server.port` | 8080 | HTTP 监听端口 |
| `server.diagnose_timeout_ms` | 30000 | 全链路诊断超时（当前配置，原 3500 太短已调大） |
| `llm.provider` | deepseek | 模型供应商：deepseek / qwen |
| `llm.api_key` | — | **必需**，在 config.local.yaml 中填写 |
| `llm.endpoint` | https://api.deepseek.com/v1 | API 端点 |
| `llm.model` | deepseek-chat | 模型名 |
| `llm.temperature` | 0.1 | 推理温度（低温度 → 确定性输出） |
| `llm.max_tokens` | 32768 | LLM 输出 max_tokens |
| `llm.max_iterations` | 4 | ReAct 最大循环轮次 |
| `tool_dispatcher.fan_out_timeout_ms` | 1500 | 工具扇出层超时（当前未实际使用，旧架构参数） |
| `rule_engine.*` | 见 config.yaml | 规则引擎各阈值 |
| `audit.file_path` | ./logs/audit.jsonl | 审计日志路径 |
| `sanitizer.max_payload_bytes` | 1024 | 工具输出截断阈值 |

---

## 附录：当前代码与架构设计的差异

| 项目 | 架构设计（文档） | 当前实现 | 说明 |
|------|----------------|---------|------|
| **规则引擎前置** | 规则引擎在前，命中后直接输出，跳过 LLM | `DiagAgent.Diagnose()` 直接调 `reactAgent.Generate()`，规则引擎未执行 | 规则引擎逻辑和阈值配置已就位，但未串联到诊断流程中 |
| **工具扇出层 (ToolDispatcher)** | config.yaml 有 `tool_dispatcher.fan_out_timeout_ms` 和各工具独立超时 | 当前由 Eino ReAct 循环调度，工具超时依赖全局 ctx | 旧的 `errgroup` 扇出架构被 ReAct 取代，配置参数待清理 |
| **规则引擎 R-09** | 占位 | 永远返回 `nil, false` | 需要接入 GetMarketingRule 工具后实现 |
| **GPT Code Review Agent** | go.mod 中有 `github.com/ollama/ollama` 等依赖 | 未在代码中出现 | 可能引用自依赖传递，非本模块直接使用 |
