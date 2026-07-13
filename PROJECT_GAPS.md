# BusPulse Diagnose-Agent 项目尚未配置/完成事项

> 生成时间：2026-07-13
> 基于完整源码扫描

---

## 一、🧱 未实现的模块（空目录）

| 目录 | 状态 | 说明 |
|------|------|------|
| `api/schema/` | 🟢 空目录 | 无 OpenAPI / Protobuf 定义，目前 JSON 结构仅在代码中手写 |
| `internal/retriever/` | 🟢 空目录 | 无向量检索实现。`prompt.go` 注释提到"P2 阶段接入向量库后改为动态检索 Top-K 条注入"，尚未开始 |

---

## 二、🔧 代码级 TODO（9 个 P1 待办）

### 2.1 工具层全部为 Mock（8 个 P1 TODO）

`internal/agent/tools/tools.go` 中**全部 8 个工具均为 Mock 实现**，均标注 `TODO(P1)`：

| 工具函数 | Mock 内容 | 需对接的真实服务 |
|---------|----------|----------------|
| `GetOrderContext` | 固定返回 mock `OrderContext` | 订单服务 RPC |
| `GetVehicleGPS` | 固定返回 mock `VehicleGPS` | GPS 服务 RPC |
| `GetAlgoSnapshot` | 固定返回 mock `AlgoSnapshot` | 算法服务 RPC |
| `GetTraceLog` | 固定返回空 `ErrorSpans` | Jaeger / SkyWalking |
| `GetGeoFence` | 固定返回 `UserInsideFence=true` | 围栏服务/多边形碰撞检测 |
| `GetStationFlow` | 固定返回 `StationActive=1` | 站点服务 |
| `GetMapRoute` | 固定返回 `congestion_index=1.8` | 高德/百度地图 API |
| `GetEtaSnapshot` | 固定返回 `NearbyVehicles=4` | ETA 服务 |

### 2.2 规则引擎中 1 个空实现

| 位置 | 内容 |
|------|------|
| `internal/rule/rules.go:210` R-09 | `checkR09MarketingMismatch` 始终返回 `(nil, false)`，标注 `TODO(P1): 接入 GetMarketingRule 工具后实现` |

---

## 三、📦 依赖管理未完善

| 问题 | 说明 |
|------|------|
| `go.mod` 缺少 Eino 依赖 | `go.mod` 中仅有 `gopkg.in/yaml.v3`，但代码中大量引用了 `github.com/cloudwego/eino/*` 和 `github.com/cloudwego/eino-ext/*`。执行过 `go mod tidy` 后可能已补全，但需确认版本兼容性 |
| Eino 版本未锁定 | 尚无明确锁定版本号，生产构建需要固定版本 |

---

## 四、🔐 配置与安全

| 问题 | 说明 |
|------|------|
| `configs/config.local.yaml` 中 API Key 为空 | LLM API Key 需要开发者手动配置（已 .gitignore，安全） |
| 无配置校验 | 启动时无配置字段合法性校验（如 API Key 在非 Mock 模式下的必填检查） |
| 无敏感配置加密方案 | API Key 等敏感信息直接明文写入 local YAML，无加密或 Secret Manager 方案 |

---

## 五、🧪 测试覆盖不足

| 文件/模块 | 状态 | 说明 |
|----------|------|------|
| `internal/agent/` | ❌ 无测试 | `agent.go` 的核心 `Diagnose` 方法、`parseReport`、`fallbackReport` 均无单元测试 |
| `internal/domain/` | ❌ 无测试 | 领域模型虽简单，但枚举常量和结构体无序列化测试 |
| `internal/rule/` | ❌ 无测试 | `engine.go` 的 `Match` 短路逻辑、`rules.go` 10 条规则均无单元测试 |
| `internal/handler/` | ❌ 无测试 | `diagnose.go` 的 `ServeHTTP` 无 handler 层测试 |
| `internal/handler/middleware/` | ❌ 无测试 | `recover.go`、`timeout.go`、`audit.go` 均无中间件测试 |
| `internal/config/` | ❌ 无测试 | 配置加载、默认值填充无测试 |
| `internal/audit/` | ❌ 无测试 | 审计日志写入无测试 |
| `pkg/ctxutil/` | ❌ 无测试 | `timeout.go` 无单元测试 |
| `pkg/errutil/` | ❌ 无测试 | 工具函数无测试 |
| **有测试的** | ✅ **2 个** | `internal/gateway/sanitizer_test.go`、`pkg/jsonutil/extract_test.go` |

---

## 六、🏗️ 基础设施缺失

| 项目 | 状态 | 说明 |
|------|------|------|
| `Dockerfile` | ❌ 不存在 | 无法容器化构建 |
| `docker-compose.yml` | ❌ 不存在 | 无本地开发编排（依赖服务：MySQL/Redis/Jaeger 等） |
| `Makefile` | ✅ 已有 | 有基本的 `run/build/test/lint/tidy/clean` 命令 |
| `.github/workflows/` | ❌ 不存在 | 无 CI/CD 流水线 |
| `helm/` 或 `k8s/` 部署 | ❌ 不存在 | 无 K8s 部署配置 |
| 健康检查 | ✅ 已有 | `GET /health` 存活探针已实现 |
| 优雅退出 | ✅ 已有 | 信号监听 + graceful shutdown |

---

## 七、📄 API 文档缺失

| 问题 | 说明 |
|------|------|
| 无 OpenAPI/Swagger 定义 | `api/schema/` 目录为空，接口仅通过代码注释描述 |
| 无接口文档生成 | 无法自动生成 API 文档 |

---

## 八、🔄 已知架构待优化

| 事项 | 说明 |
|------|------|
| system prompt 硬编码 | `agent/prompt.go` 中约 80 行完整 prompt 硬编码在代码中，应转为 `prompt.yaml` 或 `prompt.md` 文件加载 |
| 状态机文件已删除 | 项目初期存在 `internal/agent/statemachine.go` 和 `statemachine_test.go`，当前版本已不复存在（`domain/errors.go` 中仍有 `ErrStateMachinePanic` 错误常量） |
| Mock 延迟模拟 | `tools.go` 中所有工具使用 `time.Sleep` 模拟延迟，生产环境需移除 |
| 审计日志仅文件模式 | `AuditConfig.Backend` 支持 `"file"` 和 `"mysql"`，但 MySQL 后端未实现 |
| 无 Graceful Degradation 测试 | 降级逻辑（`fallbackReport`）路径未被测试覆盖 |
| ReAct 循环超时传递 | 工具层未使用 `ctxutil.WithBudget` 做逐工具超时递降 |

---

## 九、✅ 已经配置好的部分（仅供参考）

- ✅ HTTP Server 完整实现（中间件链：Recover → Timeout → AuditContext → Handler）
- ✅ Eino ReAct Agent 完整链路（规则前置 → LLM 推理 → JSON 解析 → 降级兜底）
- ✅ 脱敏网关（PII 清洗 + 字节截断）
- ✅ 确定性规则引擎（R-01~R-10）
- ✅ 审计日志（JSONL 文件写入）
- ✅ YAML 配置加载（base + local 双层覆盖）
- ✅ Go 工具链（Makefile、go.mod）
- ✅ 配置默认值填充（`config.go` applyDefaults）
- ✅ `.gitignore`（已忽略 bin/、logs/、local 配置、.env）
