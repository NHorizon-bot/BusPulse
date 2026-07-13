# BusPulse Diagnose-Agent 项目接口分层对照（Java 规范映射）

> 按 Java 经典分层规范（Controller / Service / ServiceImpl / Mapper / DTO / VO / Config / Middleware），
> 一一对应到本项目 Go 代码中的实现。

---

## 一、总览对照表

| Java 分层 | Go 对应 | 路径 | 核心职责 |
|-----------|---------|------|---------|
| **Controller（入口）** | `cmd/server/main.go` | `cmd/server/main.go` | 启动入口，组装依赖，注册路由，启停 Server |
| **Controller（路由）** | `internal/handler/` | `internal/handler/diagnose.go` | 接收 HTTP 请求，解析入参，调用 Service，返回响应 |
| | | `internal/handler/health.go` | 健康检查端点 |
| **Request DTO** | 内嵌结构体 | `diagnoseRequest`（handler/diagnose.go:31） | 请求体 JSON 反序列化，与领域对象隔离 |
| **Response VO** | 内嵌结构体 | `diagnoseResponse`（handler/diagnose.go:38） | 响应体 JSON 序列化，含 AuditID/耗时等元数据 |
| **Service Interface** | `handler.Diagnoser` 接口 | `internal/handler/diagnose.go:16` | 诊断服务接口，定义 `Diagnose(ctx, req) → report, err` |
| **ServiceImpl** | `agent.DiagAgent` | `internal/agent/agent.go` | Diagnoser 实现：规则引擎前置 + Eino ReAct 推理 |
| **DTO / Domain** | `internal/domain/` | `internal/domain/diagnosis.go` | 核心领域模型：DiagnosticRequest / DiagnosticReport |
| | | `internal/domain/order.go` | 订单/车辆/算法等工具输出数据模型 |
| | | `internal/domain/errors.go` | 业务语义错误定义 |
| **Mapper（DAO）** | `internal/agent/tools/` | `internal/agent/tools/tools.go` | 工具层，对接外部服务（当前全部 Mock） |
| **Service（业务层）** | `internal/rule/` | `internal/rule/engine.go` | 确定性规则引擎，按优先级短路匹配 |
| | | `internal/rule/rules.go` | R-01~R-10 十条规则实现 |
| | | `internal/rule/types.go` | 规则相关的类型定义 |
| **Config** | `internal/config/` | `internal/config/config.go` | YAML 双层配置加载 + 默认值填充 |
| **Middleware（过滤器）** | `internal/handler/middleware/` | `recover.go` | panic 兜底 → 500 |
| | | `timeout.go` | 请求级别超时控制 → 503 |
| | | `audit.go` | 审计 Header 提取注入 Context |
| **AOP/横切关注点** | `internal/gateway/` | `internal/gateway/sanitizer.go` | 输出脱敏：PII 擦除 + 字节截断 |
| | `internal/audit/` | `internal/audit/logger.go` | 审计日志：全链路记录写入 JSONL |
| **Util / Util** | `pkg/` | `pkg/ctxutil/timeout.go` | Context 超时预算计算 |
| | | `pkg/errutil/sentinel.go` | 错误包装工具 |
| | | `pkg/jsonutil/extract.go` | JSON 防御性解析 |
| **Application.yml** | `configs/config.yaml` | `configs/config.yaml` | 服务配置（端口/超时/模型/阈值） |
| **Application-local.yml** | `configs/config.local.yaml` | `configs/config.local.yaml` | 本地覆盖配置（API Key 等敏感值） |
| **pom.xml / build.gradle** | `go.mod` | `go.mod` | Go 模块定义和依赖管理 |
| **pom.xml / build.gradle** | `Makefile` | `Makefile` | 构建/测试/运行命令编排 |

---

## 二、分层详解

### 1. Controller 层 → `internal/handler/`

**Java 对比**：`@RestController` + `@PostMapping`

```go
// ═══════ 接口定义（相当于 Java 的 Service Interface）═══════
type Diagnoser interface {
    Diagnose(ctx context.Context, req domain.DiagnosticRequest) (*domain.DiagnosticReport, error)
}

// ═══════ Controller 实现 ═══════
type DiagnoseHandler struct {
    diagnoser   Diagnoser         // 依赖接口，非具体实现
    auditLogger *audit.Logger
}

func (h *DiagnoseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. 解析 Request DTO（相当于 @RequestBody）
    var body diagnoseRequest
    json.NewDecoder(r.Body).Decode(&body)

    // 2. 参数校验（相当于 @Valid）
    if body.OrderID == "" { /* 400 */ }

    // 3. 调用 Service（接口注入）
    report, err := h.diagnoser.Diagnose(ctx, domainReq)

    // 4. 组装 Response VO + 审计日志
    json.NewEncoder(w).Encode(diagnoseResponse{AuditID, report, durationMs})
}
```

### 2. Service 层 → `internal/agent/` + `internal/rule/`

**Java 对比**：Service Interface → ServiceImpl

handler 依赖的 `Diagnoser` 接口由 `DiagAgent` 实现：

```go
type DiagAgent struct {
    ruleEngine *rule.Engine    // 前置规则（分流节点）
    reactAgent *react.Agent    // Eino ReAct 推理
}

func (a *DiagAgent) Diagnose(ctx, req) (*DiagnosticReport, error) {
    // 规则引擎前置 → Eino ReAct 推理 → JSON 解析 → 降级兜底
}
```

规则引擎是独立的 Service 层组件：

```go
type Engine struct { thresholds Thresholds }

func (e *Engine) Match(ctx, in) (*RuleResult, bool) {
    for _, r := range allRules {
        if result, hit := r(in, e.thresholds); hit { return result, true }
    }
    return nil, false
}
```

### 3. Mapper/DAO 层 → `internal/agent/tools/`

相当于 Java 中 Mapper 或 FeignClient——对接外部数据源。

每个工具通过 `utils.InferTool` 自动推断 JSON Schema 注册给 LLM，当前全部 Mock：

```go
buildGetOrderContext → TODO: 替换为真实订单服务 RPC
buildGetVehicleGPS  → TODO: 替换为 GPS 服务 RPC
buildGetAlgoSnapshot → TODO: 替换为算法服务 RPC
// ... 共 8 个工具
```

### 4. Domain 层 → `internal/domain/`

相当于 Java 的 DTO，且不依赖任何 internal 子包（纯净模型层）：

| Java 概念 | Go 实现 |
|-----------|---------|
| `DiagnosticRequest` | 入参 DTO（相当于 `@RequestBody` 的 Java Bean） |
| `DiagnosticReport` | 出参 VO + 核心领域对象 |
| `OrderContext` / `VehicleGPS` / `AlgoSnapshot` | 外部数据源的输出模型 |
| `IssueLevel` / `RootCauseCategory` / `ActionType` | 枚举常量（等价 Java `enum`） |
| `ErrOrderNotFound` 等 | 业务异常定义（等价 Java `extends RuntimeException`） |

### 5. Config 层 → `internal/config/` + `configs/`

**Java 对比**：`@ConfigurationProperties` + `application.yml`

```go
type Config struct {
    Server         ServerConfig         `yaml:"server"`
    ToolDispatcher ToolDispatcherConfig `yaml:"tool_dispatcher"`
    LLM            LLMConfig            `yaml:"llm"`
    RuleEngine     RuleEngineConfig     `yaml:"rule_engine"`
    Audit          AuditConfig          `yaml:"audit"`
    Sanitizer      SanitizerConfig      `yaml:"sanitizer"`
}
```

双层加载：`config.yaml`（公共）+ `config.local.yaml`（本地覆盖），等价 Java 的 `application.yml` + `application-local.yml`。

---

## 三、请求调用链路（时序）

```text
┌─ HTTP 请求 ─────────────────────────────────────────────────────┐
│ POST /api/v1/diagnose {"order_id":"ord_001"}                    │
└─────────────────────────────────────────────────────────────────┘
                              │
                     ┌────────▼────────┐
                     │   Middleware     │  ← Filter 链
                     │  Recover        │     panic 捕获
                     │  Timeout        │     超时控制
                     │  AuditContext   │     Header 提取
                     └────────┬────────┘
                              │
                     ┌────────▼────────┐
                     │ DiagnoseHandler │  ← Controller
                     │ (ServeHTTP)     │     解析JSON→校验→调用Service
                     └────────┬────────┘
                              │
              ┌───────────────▼────────────────┐
              │     DiagAgent.Diagnose()        │  ← ServiceImpl
              │  ┌──────────────────────────┐   │
              │  │  规则引擎 Match()         │   │  ← 前置规则（零Token）
              │  │  R-01~R-10 短路命中→返回  │   │
              │  └────────────┬─────────────┘   │
              │               │ 未命中           │
              │  ┌────────────▼─────────────┐   │
              │  │  Eino ReAct 循环          │   │  ← LLM 推理
              │  │  1. LLM Think            │   │
              │  │  2. Call Tool → 脱敏网关   │   │  ← Mapper + AOP
              │  │  3. Observe → 循环/终止    │   │
              │  │  4. 输出 JSON             │   │
              │  └──────────────────────────┘   │
              │  → JSON 解析 → 降级兜底          │
              └───────────────┬────────────────┘
                              │
                     ┌────────▼────────┐
                     │  Audit Logger    │  ← AOP 横切
                     │  异步写入 JSONL  │
                     └────────┬────────┘
                              │
                    ┌─────────▼──────────┐
                    │   JSON Response    │
                    │  { report, audit } │
                    └────────────────────┘
```

---

## 四、Java → Go 关键差异对照

| Java | Go | 本项目体现 |
|------|----|-----------|
| 类继承/实现 | 接口隐式实现 | `DiagAgent` 实现 `Diagnoser` 无需 `implements` |
| `@Autowired` | 显式构造注入 | `main.go` 中 `NewXxx` 手动串联依赖 |
| `try/catch` | `if err != nil` | 所有错误都显式处理 |
| `@RequestMapping` | `http.ServeMux` | `mux.Handle("/api/v1/diagnose", handler)` |
| `@Valid/@NotBlank` | 手写参数校验 | `if body.OrderID == "" { /* 400 */ }` |
| `ThreadLocal` | `context.Context` | `middleware.AuditContext` 把 Header 注入 ctx |
| `@ControllerAdvice` | 中间件链 | `Recover → Timeout → AuditContext → Handler` |
| `@Aspect` | 显式调用 | `Sanitizer` 在工具返回值送入 LLM 前调用 |
| 构造函数注入 | 依赖在 `main` 统一组装 | `NewDiagAgent(cfg, ruleEngine, san)` |
