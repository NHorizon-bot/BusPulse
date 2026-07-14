# BusPulse 项目升级路径与演进方向

> 本文档梳理 BusPulse 智能诊断 Agent 从 MVP 到生产稳态的完整升级路径，涵盖 RAG 知识库、MCP 协议集成、规则引擎飞轮、评测基线、可观测性等核心方向。

---

## 目录

1. [当前阶段定位（MVP）](#1-当前阶段定位mvp)
2. [升级总览：6 大方向](#2-升级总览6-大方向)
3. [方向一：RAG 知识库（P2 核心）](#3-方向一rag-知识库p2-核心)
4. [方向二：MCP 协议集成（工具层统一标准）](#4-方向二mcp-协议集成工具层统一标准)
5. [方向三：规则引擎飞轮（零 Token 成本持续扩大）](#5-方向三规则引擎飞轮零-token-成本持续扩大)
6. [方向四：评测基线 & 质量门禁（Eval-Driven）](#6-方向四评测基线--质量门禁eval-driven)
7. [方向五：可观测性 & 全链路追踪（P2 生产态）](#7-方向五可观测性--全链路追踪p2-生产态)
8. [方向六：基础设施与架构演进](#8-方向六基础设施与架构演进)
9. [优先级与排期建议](#9-优先级与排期建议)
10. [架构演进全景图](#10-架构演进全景图)

---

## 1. 当前阶段定位（MVP）

### 已完成的功能

| 领域 | 状态 | 文件/位置 |
|------|------|-----------|
| HTTP 服务 + 中间件 | ✅ 完成 | `cmd/server/main.go`, `internal/handler/middleware/` |
| ReAct 推理循环 | ✅ 完成 | `internal/agent/agent.go` → Eino `react.NewAgent` |
| DeepSeek / Qwen 双模型支持 | ✅ 完成 | `internal/agent/agent.go:139-173` |
| 8 个只读诊断工具（Mock） | ✅ 完成 | `internal/agent/tools/tools.go` |
| 脱敏与摘要网关 | ✅ 完成 | `internal/gateway/sanitizer.go` |
| 审计日志（JSONL） | ✅ 完成 | `internal/audit/logger.go` |
| 规则引擎 R-01~R-10 | ✅ 实现（未串联到诊断流程） | `internal/rule/` |
| SOP 冷启动（System Prompt 硬编码） | ✅ 完成 | `internal/agent/prompt.go` |
| 三层 JSON 解析防御链 | ✅ 完成 | `pkg/jsonutil/extract.go` |

### 当前已知差异与待办

| 项目 | 状态 | 说明 |
|------|------|------|
| 规则引擎前置 | ⏳ 已实现但未串联 | `ruleEngine` 已注入但 `Diagnose()` 直接调了 `reactAgent.Generate()` |
| 工具真实 RPC 替换 | 📝 8 个工具全是 Mock | 每个工具都有 `TODO(P1): 替换为真实服务 RPC` |
| RAG 知识库 | ❌ 未接入 | System Prompt 硬编码 9 条 SOP，P2 阶段接入 |
| MCP 协议 | ❌ 未涉及 | 当前工具通过 Eino `utils.InferTool` 注册 |
| 评测基线 | ❌ 未建立 | 文档规划 30~50 条黄金 Case |
| 可观测性 | ⚠️ 仅日志 | 无 OpenTelemetry，无 Eino Callback 注入 |
| Human-in-the-Loop | ❌ 未实现 | 审计日志有 `human_action` 字段但无暂停/恢复机制 |

---

## 2. 升级总览：6 大方向

```
                         BusPulse 升级路径
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
  短期可做（1-2月）     中期建设（2-4月）       长期演进（4月+）
        │                     │                     │
  ① 规则引擎串联入诊断    ③ RAG 向量知识库       ⑤ MCP 协议集成
  ② 工具替换真实 RPC      ④ 评测基线+质量门禁     ⑥ 架构进化
                         ⑦ 可观测性/OTel
```

| 方向 | 阶段 | 目标 | 难度 |
|------|------|------|------|
| **规则引擎串联** | P0→P1 | 规则命中时零 Token 成本，直接输出 | ★☆☆ |
| **工具真实 RPC** | P1 | 8 个工具对接真实后端服务 | ★★☆ |
| **RAG 知识库** | P2 | SOP 从硬编码改为向量库动态检索 | ★★★ |
| **评测基线** | P1→P2 | 30→50→200 条 Case，自动回归 | ★★☆ |
| **MCP 协议集成** | P3 | 工具通过 MCP Server 暴露，可被任意 AI 客户端复用 | ★★★ |
| **可观测性** | P2 | OpenTelemetry 全链路追踪 | ★★☆ |
| **Human-in-the-Loop** | P2 | 人工复核可暂停/恢复诊断 | ★★★ |
| **架构演进** | P2→P3 | 多 Agent 协作、流式输出、模型微调 | ★★★ |

---

## 3. 方向一：RAG 知识库（P2 核心）

### 3.1 为什么要上 RAG

RAG（检索增强生成）的核心价值是让 LLM 参考**历史经验**做判断，而不是每次凭空推理。但它**有明确的知识边界**——不是所有东西都往 RAG 里塞。

### 3.2 三类知识，三种存储方式

```
┌─────────────────────────────────────────────────────────┐
│                  诊断知识体系                              │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ ① 确定性规则  │  │ ② 历史诊断案例│  │ ③ 业务知识   │   │
│  │              │  │              │  │              │   │
│  │ R-01~R-10   │  │ 人工确认的正  │  │ 城市运营时段  │   │
│  │ 固定阈值     │  │ 样本/负样本   │  │ 线路配置信息  │   │
│  │ 算法逻辑     │  │ 相似问题复现  │  │ 场站周边信息  │   │
│  │              │  │ 罕见场景积累  │  │ 特殊活动规则  │   │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘   │
│         │                 │                 │           │
│         ▼                 ▼                 ▼           │
│  规则引擎        RAG 向量库（P2+）      工具 RPC 返回      │
│  (代码逻辑)       (知识检索)           (实时数据查询)       │
│  零 Token 成本    注入 LLM Prompt      never 进 RAG       │
│  100% 确定       辅助推理决策          保证实时性           │
└─────────────────────────────────────────────────────────┘
```

#### ① 确定性规则 → 规则引擎（R-01~R-10）

```go
// internal/rule/rules.go
// 这些**不进 RAG**，它们留在规则引擎代码中
// 命中直接输出结论，零 Token 成本，100% 确定性

func checkR04HeadingMismatch(in MatchInput, th Thresholds) (*RuleResult, bool) {
    // 方向夹角 > 90° → 判定方向冲突
    // 这是硬性规定，调用工具数据即判定，不需 LLM 参与
}
```

| 不适合进 RAG 的原因 |
|---------------------|
| 规则是**确定性逻辑**，不是经验知识，不需要检索参考 |
| 规则命中**零 Token 成本**，进 RAG 反而要走 LLM，增加成本 |
| 规则是**精确匹配**（夹角 > 90° 就是命中），RAG 相似度检索反而引入不确定性 |
| 改阈值只需改配置，改 RAG 要重新 Embedding 所有向量 |

#### ② 历史诊断案例 → RAG 向量库（**这才是 RAG 存的内容**）

当诊断跑了 2~3 个月后，会积累大量人工确认的诊断案例。每条案例就是一个 RAG 文档：

```json
// 存入 RAG 的样本格式
{
  "case_id": "case_001",
  "title": "北京-方向冲突导致有车分不上",
  "context": {
    "city": "bj",
    "order_status": "DISPATCHED",
    "complaint_type": "有车分不上",
    "algo_heading_angle": 105,
    "filter_reasons": ["HEADING_MISMATCH"]
  },
  "diagnosis": {
    "root_cause_category": "ALGO_HEADING_MISMATCH",
    "issue_level": "P3",
    "customer_service_script": "亲，您附近的车辆行驶方向与您不一致，系统已自动选择综合时间最短的方案……",
    "confidence": 1.0,
    "human_confirmed": true       // 人工确认过！
  }
}
```

| 适合进 RAG 的原因 |
|---------------------|
| 案例是**经验性的**——不是逻辑规则，而是"上次类似情况怎么判"的参考 |
| 可以帮助 LLM **更快收敛**：看到相似案例直接参考，少调几轮工具 |
| 可以**自学习**：新类型问题第一次靠 LLM + 人工确认，确认后入库，下次自动命中 |
| 案例会**持续积累**：越跑越多，RAG 的价值随时间增长 |
| 人工**确认/驳回**的反馈直接反哺 -> 正样本增强、负样本修正 Prompt |

新的诊断到来时，RAG 检索出最相似的 3~5 条历史案例，注入 Prompt：

```
检索到相似历史案例（相似度 92%）：
  - 案例 case_045: 北京，方向夹角 102°，HEADING_MISMATCH
  - 诊断结论: ALGO_HEADING_MISMATCH, 人工确认 ✅
  - 客服话术: "系统已为您匹配综合到达时间最短的方案..."
```

#### ③ 业务知识（实时数据） → 工具 RPC 返回

这个城市服务到几点、这个站点当前是否运营、这条线路经过哪些站——这些**不进 RAG**：

| 不适合进 RAG 的原因 |
|---------------------|
| **实时性要求**：站点状态可能随时变化，存 RAG 会读到过期数据 |
| 通过工具 RPC **实时查询**最可靠（当前 `GetGeoFence`、`GetStationFlow` 等工具的设计目标） |
| RAG 适合静态知识，不适合频繁变化的运营数据 |

### 3.3 所以 RAG 到底存什么？

```
RAG 存（✅）：                         RAG 不存（❌）：
  ┌──────────────────┐                ┌──────────────────┐
  │ 历史诊断案例       │                │ R-01~R-10 规则    │
  │ ─ 人工确认的正样本 │                │ 逻辑/阈值          │
  │ ─ 人工驳回的负样本 │                │                   │
  │ ─ 罕见/复杂的场景  │                │ 实时运营数据        │
  │ ─ 不同城市的差异   │                │ 站点状态/围栏       │
  │ ─ 节假日/活动特例  │                │ 当前路况/拥堵       │
  └──────────────────┘                └──────────────────┘
```

### 3.4 当前 MVP 为什么不需要 RAG

技术文档明确写了：

> **冷启动策略**：规则引擎优先上马，立即可覆盖 60% 基础 Case（零 Token 成本、零幻觉）。LLM 仅卡位 40% 跨系统长尾异常。
>
> **冷启动阶段 SOP 量级 < 10 条，直接注入 Agent Instruction（FString 模板）比向量检索更快更准，向量库在 P2 阶段接入即可。**

当前阶段（MVP）：
- 只有 9 条 SOP，全注入也只占 2~3K Token
- **还没有历史诊断案例积累**——RAG 要有东西才能检索
- System Prompt 硬编码比向量检索更快更准

P2 阶段才上 RAG：
- SOP + 历史案例积累到 50+ 条
- 人工确认机制建立，持续产出正样本
- 此时全量注入 Token 成本过高，需要动态检索

### 3.5 升级路径

```
当前（MVP）：System Prompt 硬编码 9 条 SOP
    │
    ▼
第一步（P1）：从代码抽取为外部 YAML/JSON
    │   └── internal/agent/prompt.go → configs/sops.yaml
    │   启动时加载，热更新需重启
    │   本质仍是全量注入，但不依赖代码部署
    │
    ▼
第二步（P2）：规则引擎入诊断流程（这是上 RAG 的前提）
    │   └── 规则命中 → 零 Token 成本输出
    │   └── 规则未命中 → 激活 LLM ReAct
    │   └── 此时 LLM 只处理 40% 长尾，才有 RAG 检索必要
    │
    ▼
第三步（P2）：建立人工确认闭环
    │   └── Audit 记录 → 人工复核 → 正样本/负样本
    │   └── 当积累 50+ 条确认案例后，RAG 有价值
    │
    ▼
第四步（P2）：接入向量库（RAG）
    │   └── 历史案例 → Embedding → 存入 pgvector
    │   └── 诊断时：检索 Top-K 相似案例 → 注入 Prompt
    │   └── 向量库选型：pgvector（优先）/ Milvus / ES
    │
    ▼
第五步（P2→P3）：反馈闭环自动化
    │   └── 人工确认结论 → 正样本自动入库
    │   └── 人工驳回修正 → 负样本 → Prompt 迭代
    │   └── 高频命中场景 → 抽象为新规则 → 反哺规则引擎
```

### 3.6 架构变化

```
当前：
  input → BuildSystemPrompt() → 固定字符串 → ReAct Agent

P2 阶段：
  input(order_id, city_id, free_text)
       │
       ├── 规则引擎（前置）
       │     │
       │     ├── 命中 → 直接输出 (Confidence=1.0, 零 Token)
       │     └── 未命中 → 继续
       │
       ├── RAG Retriever (Eino-ext Retriever)
       │     │  ┌──────────────────────┐
       │     ├──┤ pgvector / Milvus    │  ← 存历史诊断案例
       │     │  └──────────────────────┘
       │     │
       │     ▼
       │  Top-K 相似历史案例（3~5 条）
       │     │
       │     ▼
       │  MessageModifier: System Prompt + 历史案例参考
       │     │
       │     ▼
       └── ReAct Agent
             │
             └── 工具 RPC（实时数据）
```

### 3.7 代码改动参考

```go
// ── 1. RAG 检索接口 ──
type CaseRetriever interface {
    Retrieve(ctx context.Context, query string, topK int) ([]HistoryCase, error)
}

// ── 2. 历史案例结构 ──
type HistoryCase struct {
    CaseID        string                  `json:"case_id"`
    Context       string                  `json:"context"`       // 检索文本
    Diagnosis     domain.DiagnosticReport `json:"diagnosis"`
    HumanVerified bool                    `json:"human_verified"`
}

// ── 3. 构建 Prompt（P2 版本） ──
func BuildSystemPrompt(retriever CaseRetriever, ctx context.Context,
    req domain.DiagnosticRequest) string {
    
    base := baseSystemPrompt  // 核心约束 + JSON Schema（不变）
    
    // 检索历史相似案例
    similarCases, _ := retriever.Retrieve(ctx, 
        fmt.Sprintf("城市:%s 客诉:%s", req.CityID, req.FreeTextContext), 3)
    
    if len(similarCases) > 0 {
        base += "\n## 参考历史案例\n"
        for _, c := range similarCases {
            base += fmt.Sprintf(`
- 案例 %s:
  诊断结论: %s
  话术: %s
  人工确认: %v
`, c.CaseID, c.Diagnosis.RootCauseCategory,
                c.Diagnosis.CustomerServiceScript, c.HumanVerified)
        }
    }
    
    return base
}

// ── 4. 案例入库（人工确认后触发） ──
func (a *DiagAgent) RecordFeedback(auditRecord audit.Record, 
    humanAction audit.HumanAction) error {
    
    if humanAction == audit.HumanActionConfirmed {
        // 正样本 → 存入 RAG 向量库
        case_ := HistoryCase{
            CaseID:        auditRecord.AuditID,
            Context:       buildContextText(auditRecord),
            Diagnosis:     *auditRecord.FinalReport,
            HumanVerified: true,
        }
        return a.caseStore.Insert(ctx, case_)
    }
    // 负样本 → 可用于 Prompt 迭代分析
}
```

### 3.8 关键决策点

| 决策 | 方案 | 说明 |
|------|------|------|
| **向量的内容** | 历史诊断案例（订单上下文 + 根因 + 人工确认标记） | 不是规则，不是实时数据 |
| **Embedding 模型** | BGE-M3 / text-embedding-3-small | 中文场景 BGE-M3 效果好 |
| **向量库选型** | pgvector（优先） | 无需额外运维，直接复用 PostgreSQL |
| **检索时机** | 诊断开始时一次检索 | 预先注入 System Prompt，不在 ReAct 循环中实时检索 |
| **Top-K** | 3~5 条 | 太少遗漏，太多浪费 Token |
| **什么时候上 RAG** | 历史案例积累 50+ 条后 | 在此之前全量注入 System Prompt 更简单高效 |

---

## 4. 方向二：MCP 协议集成（工具层统一标准）

### 4.1 什么是 MCP

MCP（Model Context Protocol）是 Anthropic 提出的开源标准协议，用于 AI 应用程序与外部工具/数据源的标准化通信。可以理解为"AI 应用的 USB-C 接口"—— 一个统一标准连接任意工具和数据源。

### 4.2 为什么要集成 MCP

当前项目的工具注册方式是通过 Eino 的 `utils.InferTool`：

```go
// 当前方式：工具与 Eino 框架强耦合
return utils.InferTool("GetOrderContext", "描述...", func(ctx, in) (string, error) { ... })
```

问题：
- 工具被锁定在 Eino 框架内，无法被其他 AI 客户端复用
- 工具变更需要改代码重新部署
- 无法利用社区已有的 MCP 生态（如数据库查询、文件操作等）

### 4.3 MCP 集成方案

#### 方案 A：工具层封装为 MCP Server（推荐）

```
┌──────────────────────────────────────────┐
│             诊断 Agent                    │
│  ┌──────────────────────────────────────┐│
│  │  Eino ReAct Agent                    ││
│  │  ↓ tool_calls                        ││
│  │  MCP Client 适配层                    ││
│  └────────────┬─────────────────────────┘│
└───────────────┼──────────────────────────┘
                │ MCP 协议（JSON-RPC）
                ▼
┌──────────────────────────────────────────┐
│          MCP Server（诊断工具）            │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ │
│  │ 订单服务  │ │ GPS服务  │ │ 算法服务  │ │
│  │ 工具      │ │ 工具     │ │ 工具     │ │
│  └──────────┘ └──────────┘ └──────────┘ │
└──────────────────────────────────────────┘
```

改造步骤：

1. **用 MCP Go SDK 包装现有工具**：
```go
// 新增：internal/mcp/server.go
func NewMCPServer() *mcp.Server {
    s := mcp.NewServer()
    
    s.AddTool(mcp.Tool{
        Name:        "GetOrderContext",
        Description: "查询订单基础上下文...",
        Parameters:  /* JSON Schema */,
        Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
            var in domain.OrderContextInput
            json.Unmarshal(args, &in)
            // 调用真实 RPC 或 mock
            return result, nil
        },
    })
    // ... 注册其他 7 个工具
    
    return s
}
```

2. **在 Agent 中通过 MCP Client 调用工具**：
```go
// agent.go 中不再直接注册工具，而是连接 MCP Server
mcpClient, _ := mcp.NewClient("http://localhost:9090")
tools, _ := mcpClient.ListTools()

// 将 MCP 工具动态转换为 Eino BaseTool
einoTools := convertMCPToolsToEinoTools(mcpClient, tools)
```

3. **独立部署 MCP Server**（可选）：
```bash
# 工具层可作为独立进程启动
./bin/diagnose-mcp-server --port 9090

# Agent 通过 MCP 协议连接
./bin/diagnose-agent --mcp-endpoint http://localhost:9090
```

#### 方案 B：Agent 本身作为 MCP 工具暴露（推荐）

将整个 `Diagnose` 功能包装为一个 MCP Tool，让其他 AI 应用（如 Claude、LinkBot）能够直接调用：

```go
// cmd/mcp/main.go
func main() {
    s := mcp.NewServer()
    
    s.AddTool(mcp.Tool{
        Name:        "DiagnoseOrder",
        Description: "诊断公交订单问题：输入订单ID，返回结构化诊断报告",
        Parameters:  diagnoseParamSchema,
        Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
            var req domain.DiagnosticRequest
            json.Unmarshal(args, &req)
            report, _ := diagAgent.Diagnose(ctx, req)
            return json.Marshal(report)
        },
    })
    
    log.Fatal(s.Serve(":9090"))
}
```

这样 Claude Desktop、Link 机器人等任意 MCP 客户端都可以直接调用诊断能力。

### 4.4 MCP 生态扩展方向

| 扩展方向 | 说明 | 优先级 |
|---------|------|--------|
| 内置工具 MCP 化 | 8 个诊断工具包装为标准 MCP Server | 中 |
| 诊断 API 暴露为 MCP | 整个 Diagnose 接口变成 MCP Tool | 高 |
| 集成外部 MCP 工具 | 接入社区已有 MCP（数据库查询、Grafana API 等） | 低 |
| MCP Tool 动态发现 | Agent 启动时从 MCP Registry 拉取可用工具列表 | 低 |

---

## 5. 方向三：规则引擎飞轮（零 Token 成本持续扩大）

### 5.1 当前问题

规则引擎 R-01~R-10 已经实现，但**未串联到诊断流程**中：

```go
// 当前 agent.go: Diagnose() 直接调 React，跳过了规则引擎
func (a *DiagAgent) Diagnose(ctx context.Context, req domain.DiagnosticRequest) {
    output, err := a.reactAgent.Generate(ctx, msgs)  // ← 直接走 LLM
    // 规则引擎根本没执行！
}
```

### 5.2 升级步骤

#### 第一步：串联规则引擎前置（1-2 天）

```go
func (a *DiagAgent) Diagnose(ctx context.Context, req domain.DiagnosticRequest) (*domain.DiagnosticReport, error) {
    // ── 规则引擎前置 ──
    ruleInput := buildMatchInput(ctx, req)  // 需要先获取数据
    if result, hit := a.ruleEngine.Match(ctx, ruleInput); hit {
        return rule.ToReport(result, req), nil  // 零 Token 成本！
    }
    
    // ── 未命中 → ReAct 推理 ──
    output, err := a.reactAgent.Generate(ctx, msgs)
    // ...
}
```

**问题**：规则引擎需要 `MatchInput`（包含 `Order`、`GPS`、`Algo` 等真实数据），当前全是 Mock。所以必须先让工具返回真实数据，规则引擎才能生效。

#### 第二步：规则-LLM 正反馈飞轮

```
LLM 诊断案例持续积累
        │
        ▼
人工确认规则命中的高频 Case
        │
        ▼
抽象为确定性规则参数
        │
        ▼
写入规则引擎（新增 R-11, R-12...）
        │
        ▼
Eval 回归验证：准确率不下降
        │
        ▼
规则覆盖范围扩大 → LLM 调用减少 → 成本降低
```

### 5.3 规则引擎扩展方向

| 规则 ID | 当前状态 | 下一步 |
|---------|---------|--------|
| R-01~R-08 | ✅ 已实现 | 接入真实数据后启用 |
| R-09（营销不适用） | ⚠️ 占位返回 false | 对接营销规则服务 |
| R-10（连接池耗尽） | ✅ 已实现 | 依赖 Trace 真实数据 |
| R-11~R-N（新规则） | 📝 待沉淀 | 从 LLM 高频命中场景反哺 |

---

## 6. 方向四：评测基线 & 质量门禁（Eval-Driven）

### 6.1 为什么需要

> 技术文档原话：
> "MVP 开工第一周即建立评测基线，避免'凭感觉判断质量'"

没有评测基线的问题：
- 改了一条 Prompt → 不知道效果变好还是变差
- 新增加一条规则 → 不知道是否影响已有场景的判断
- 换了模型 → 不知道诊断准确率是否下降

### 6.2 升级路径

```
P0（当前）：无评测
    │
    ▼
P1（建议立即开始）：
    │   运营梳理 30~50 条历史典型客诉
    │   每条标注：订单信息 + 正确根因 + 正确话术 + 预期 Level
    │   保存为 jsonl 格式 gold_cases.jsonl
    │
    ▼
P2（短期）：
    │   实现 Eval Runner：
    │   ┌──────────────────────────────────────┐
    │   │ for each case:                       │
    │   │   report := agent.Diagnose(case.req)  │
    │   │   score := compare(report, case.expected)
    │   │   accuracy = sum(score) / len(cases)  │
    │   └──────────────────────────────────────┘
    │   跑一次，看当前准确率
    │
    ▼
P3（持续）：
    │   CI/CD 门禁：每次合并前自动跑 Eval
    │   准确率下降 → 阻塞合并
    │   准确率趋势仪表盘（Grafana）
```

### 6.3 Eval 框架设计

```go
// 新增：internal/eval/runner.go
package eval

type GoldCase struct {
    Request  domain.DiagnosticRequest  `json:"request"`
    Expected domain.DiagnosticReport   `json:"expected"`
}

type EvalResult struct {
    Total     int     `json:"total"`
    Passed    int     `json:"passed"`
    Accuracy  float64 `json:"accuracy"`
    Failures  []CaseFailure `json:"failures,omitempty"`
}

func Run(ctx context.Context, agent Diagnoser, cases []GoldCase) (*EvalResult, error) {
    passed := 0
    for _, c := range cases {
        report, err := agent.Diagnose(ctx, c.Request)
        if err != nil { /* 计入失败 */ }
        if report.RootCauseCategory == c.Expected.RootCauseCategory {
            passed++
        }
    }
    return &EvalResult{
        Total: len(cases),
        Passed: passed,
        Accuracy: float64(passed) / float64(len(cases)),
    }, nil
}
```

### 6.4 评估维度

| 维度 | 权重 | 说明 |
|------|------|------|
| **根因分类准确率** | 70% | RootCauseCategory 是否匹配标注 |
| **Level 准确率** | 15% | IssueLevel 是否匹配（P1/P2/P3） |
| **话术质量** | 10% | CustomerServiceScript 不含技术黑话 |
| **Confidence 合理性** | 5% | 规则命中应为 1.0，LLM 推理一般 0.6~0.9 |

---

## 7. 方向五：可观测性 & 全链路追踪（P2 生产态）

### 7.1 当前状态

- ✅ 审计日志落盘（`logs/audit.jsonl`）—— 记录了最终结论
- ❌ 无 Eino Callback 注入 —— 无法看到每步详情
- ❌ 无 OpenTelemetry —— 无法关联下游 RPC 调用

### 7.2 升级路径

#### 第一步：Eino Callback 注入

Eino 提供了 Callback Aspects（OnStart/OnEnd/OnError），在框架层面自动捕获 Agent 全生命周期事件：

```go
// 在 main.go 中注入 Callback
handler := callbacks.NewHandlerBuilder().
    OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
        log.Printf("[START] node=%s type=%s", info.Name, info.Type)
        return ctx
    }).
    OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
        log.Printf("[END] node=%s duration=%v", info.Name, output.Duration)
        return ctx
    }).
    OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
        log.Printf("[ERROR] node=%s err=%v", info.Name, err)
        return ctx
    }).Build()

// 注入到 Agent Generate 时
output, err := a.reactAgent.Generate(ctx, msgs,
    agent.WithComposeOptions(compose.WithCallbacks(handler)),
)
```

可以捕获的事件：

| 节点 | 捕获内容 | 用途 |
|------|---------|------|
| ChatModel | `model=deepseek-chat, prompt_tokens=1245, completion_tokens=356` | Token 计费、耗时 |
| Tools | `GetOrderContext: status=SUCCESS, duration=120ms` | 工具健康度 |
| 全图 | `step=3, last_completed=tools` | 诊断进度追踪 |

#### 第二步：OpenTelemetry 全链路追踪

```
Agent 诊断请求
    │
    ├── Span: chat_model_call (3.2s)
    │     ├── Attributes: model=deepseek-chat, tokens=1601
    │     └── Status: OK
    │
    ├── Span: tool_get_order_context (120ms)
    │     ├── Attributes: tool_name=GetOrderContext, order_id=test_001
    │     ├── Links: ──→ 后端订单服务 TraceID
    │     └── Status: OK
    │
    └── Span: tool_get_algo_snapshot (200ms)
          ├── Attributes: tool_name=GetAlgoSnapshot
          └── Status: OK
```

### 7.3 可观测性架构

```
                          ┌─────────────────┐
                          │    Grafana       │
                          │  (仪表盘 + 告警)  │
                          └────────┬────────┘
                                   │
Agent 日志/指标 ──► Loki/Prometheus
    │
    ├─ 审计日志 (logs/audit.jsonl)
    ├─ Eino Callback (结构化事件)
    └─ OpenTelemetry (Trace)
```

---

## 8. 方向六：基础设施与架构演进

### 8.1 工具替换为真实 RPC

当前 8 个工具全部返回 Mock 数据。这是**生产化最紧迫的任务**。

```go
// TODO(P1) 列表：
// tools.go:68   → orderClient.GetOrder(ctx, &pb.GetOrderReq{OrderId: in.OrderID})
// tools.go:103  → gpsService.GetVehicleGPS(ctx, in.VehicleID)
// tools.go:131  → algoService.GetSnapshot(ctx, in.OrderID)
// tools.go:161  → traceService.GetTrace(ctx, in.TraceID)
// tools.go:187  → fenceService.CheckGeoFence(ctx, in.CityID, lng, lat)
// tools.go:213  → stationService.GetStationFlow(ctx, in.StationID)
// tools.go:241  → mapApi.GetRoute(ctx, origin, dest)
// tools.go:265  → etaService.GetEtaSnapshot(ctx, in.OrderID)
```

替换后，`simulateLatency` 函数可删除。

### 8.2 多 Agent 协作架构（P3）

当业务复杂到需要多个专业 Agent 协作时：

```
                    ┌──────────────────────┐
                    │    Supervisor Agent   │
                    │  (任务分发与结果汇聚)   │
                    └──────┬───────┬───────┘
                           │       │
              ┌────────────┘       └────────────┐
              ▼                                  ▼
    ┌──────────────────┐              ┌──────────────────┐
    │  诊断 Agent       │              │  退款 Agent       │
    │  (当前系统)        │              │  (处理赔付/退款)   │
    │  读工具→分析→诊断   │              │  写操作→执行退款   │
    └──────────────────┘              └──────────────────┘
```

Eino 的 `multiagent` 包原生支持这种 Supervisor 模式。

### 8.3 流式输出（渐进式渲染）

技术文档规划了"1 秒骨架 → 3~5 秒深度诊断"的体验：

```
当前：同步等待完整诊断 → 一次性返回 JSON
升级：
  Step 1: 1s 内输出时空骨架（订单状态、Trace 状态）→ 立即渲染
  Step 2: 逐步输出深度分析 → 渐进刷新卡片
```

Eino react.Agent 的 `Stream()` 方法支持流式输出，可以在 ReAct 循环中边推理边输出：

```go
stream, _ := a.reactAgent.Stream(ctx, msgs)
for chunk, err := stream.Recv(); err != io.EOF; chunk, err = stream.Recv() {
    // 每步结果逐块推送前端
    ws.WriteJSON(chunk)
}
```

### 8.4 模型微调（P3 远期）

当积累到足够多的高质量诊断样本（2000+ 条）后，可以考虑对模型进行 LoRA 微调：

| 阶段 | 方案 | 样本量 | 预期收益 |
|------|------|--------|---------|
| P0~P2 | Prompt Engineering | 0 | 基线 |
| P3 | LoRA 微调 DeepSeek | 2000+ 条 | 格式更稳定、幻觉更低 |
| P4 | 全量微调 | 10000+ 条 | 领域知识内化，减少工具调用 |

### 8.5 配置中心化

当前配置散落在：
- `configs/config.yaml` — 基础配置
- `configs/config.local.yaml` — 本地覆盖
- `internal/rule/types.go` — 规则阈值（默认值在代码里）

升级方向：
```
当前：本地 YAML 文件
    │
    ▼
第一步：支持环境变量覆盖（已有 DEEPSEEK_API_KEY 模式）
    │
    ▼
第二步：接入配置中心（Nacos / etcd / Apollo）
    │   运行时热更新规则阈值、模型参数
    ▼
第三步：功能开关（Feature Flag）
    │   灰度发布、A/B 测试
```

---

## 9. 优先级与排期建议

### 9.1 短期（1-2 周，P0→P1）

| 任务 | 预估工作量 | 说明 |
|------|-----------|------|
| 规则引擎串联入 Diagnose | 1 天 | 需要在 ReAct 前先走规则引擎 |
| 工具替换 1-2 个真实 RPC | 2-3 天/个 | 优先替换 GetOrderContext 和 GetAlgoSnapshot |
| 建立 30 条黄金 Case 基线 | 3 天（配合运营） | 找运营梳理历史典型工单 |
| Eval Runner 实现 | 2 天 | 自动跑 Case 输出准确率指标 |

### 9.2 中期（2-4 周，P1→P2）

| 任务 | 预估工作量 | 说明 |
|------|-----------|------|
| 剩余 6 个工具替换真实 RPC | 2-3 天/个 | 并行对接 |
| RAG 知识库搭建 | 1 周 | 向量库选型 + SOP 向量化 + 检索链路 |
| Eino Callback 注入 | 2 天 | 全链路审计增强 |
| CI/CD Eval 门禁 | 2 天 | 合并前自动跑 Eval |
| SOP 从硬编码改为配置文件 | 1 天 | 不依赖代码部署 |

### 9.3 长期（1-2 月，P2→P3）

| 任务 | 预估工作量 | 说明 |
|------|-----------|------|
| MCP 协议集成 | 1-2 周 | 工具 MCP Server + Agent 作为 MCP Tool |
| OpenTelemetry 全链路追踪 | 1 周 | 引入 OTel SDK + 对接后端 |
| Human-in-the-Loop 暂停/恢复 | 1 周 | Eino Interrupt/Resume 机制 |
| 流式输出（渐进渲染） | 1 周 | Stream API + WebSocket |
| 规则飞轮自动化 | 2 周 | 高频命中场景自动归因为新规则 |

---

## 10. 架构演进全景图

### 当前 MVP 架构

```
┌──────┐   POST /api/v1/diagnose
│ curl  │ ──────────────────►┌──────────────────────────────────────┐
└──────┘                     │  HTTP Server (port:8080)             │
                             │  ├─ Recover + Timeout + AuditContext │
                             │  └─ DiagnoseHandler                  │
                             │       │                              │
                             │       ▼                              │
                             │  DiagAgent                           │
                             │  ├─ ruleEngine (未使用)               │
                             │  └─ reactAgent (react.NewAgent)      │
                             │       │                              │
                             │       ▼                              │
                             │  LLM: DeepSeech API                  │
                             │  Tools: 8×Mock (脱敏后)               │
                             │  SOP: 硬编码在 Prompt                 │
                             │  Audit: JSONL 文件                    │
                             └──────────────────────────────────────┘
```

### P2 演进架构

```                                                      
┌─────────────────┐
│ 客户端           │
│ 工单/Link/Alert  │
└────────┬────────┘
         │ POST /api/v1/diagnose
         ▼
┌───────────────────────────────────────────────────────┐
│  HTTP Server                                          │
│  └─ DiagnoseHandler → DiagAgent                       │
│       │                                               │
│       ├─ 1. 规则引擎前置 (R-01~R-N，零 Token)          │
│       │     ├─ 命中 → 直接输出 (Confidence=1.0)        │
│       │     └─ 未命中 → 继续                          │
│       │                                               │
│       ├─ 2. SOP Retriever (RAG 向量库)                 │
│       │     └─ Top-K 相关 SOP → 注入 Prompt           │
│       │                                               │
│       ├─ 3. Eino ReAct Agent                          │
│       │     ├─ MCP Client → MCP Server (工具层)       │
│       │     └─ Eino Callback → 审计/指标              │
│       │                                               │
│       ├─ 4. OpenTelemetry Trace                        │
│       └─ 5. 审计日志 (JSONL + 持久化 DB)              │
└───────────────────────────────────────────────────────┘
         │
         ▼
┌───────────────────────────────────────────────────────┐
│  MCP Server (工具层，可独立部署)                       │
│  ├─ GetOrderContext → 订单服务 gRPC                   │
│  ├─ GetVehicleGPS   → GPS 服务 gRPC                   │
│  ├─ GetAlgoSnapshot → 算法服务 gRPC                   │
│  ├─ GetTraceLog     → Jaeger API                     │
│  ├─ GetGeoFence     → 围栏服务 gRPC                   │
│  ├─ GetStationFlow  → 站点服务 gRPC                   │
│  ├─ GetMapRoute     → 地图 API (高德/百度)            │
│  └─ GetEtaSnapshot  → ETA 服务 gRPC                   │
└───────────────────────────────────────────────────────┘
         │
         ▼
┌───────────────────────────────────────────────────────┐
│  基础设施                                              │
│  ├─ 向量库 (pgvector/Milvus)                           │
│  ├─ OpenTelemetry Collector → Grafana                 │
│  ├─ CI/CD Eval 门禁 → 阻止劣化                        │
│  └─ 配置中心 (Nacos/etcd) → 热更新                    │
└───────────────────────────────────────────────────────┘
```

### P3 远期架构（MCP 生态化）

```
┌──────────────────────────────────────────────────────┐
│  任意 MCP 客户端                                      │
│  Claude Desktop / Link Bot / 工单系统 / 自定义应用    │
└────────────────────────┬─────────────────────────────┘
                         │ MCP 协议
                         ▼
┌──────────────────────────────────────────────────────┐
│  MCP Gateway（诊断能力统一入口）                       │
│  ├─ DiagnoseOrder(order_id) → 诊断报告                │
│  ├─ SOPLookup(category) → 排障 SOP                   │
│  └─ AuditQuery(audit_id) → 审计详情                   │
└────────────────────────┬─────────────────────────────┘
                         │
              ┌──────────┴──────────┐
              ▼                     ▼
┌─────────────────────┐  ┌─────────────────────┐
│  Supervisor Agent   │  │  退款 Agent          │
│  (诊断分发)           │  │  (自动处理/转人工)    │
└──────────┬──────────┘  └─────────────────────┘
           │
     ┌─────┴─────┐
     ▼           ▼
┌────────┐ ┌────────┐
│ 诊断    │ │ 规则    │
│ Agent  │ │ Engine  │
└────────┘ └────────┘
```

---

## 附录：关键依赖与选型参考

| 需求 | 推荐方案 | 备选 |
|------|---------|------|
| Go RAG 框架 | Eino-ext Retriever | LangChain Go |
| 向量库 | pgvector（复用现有 DB） | Milvus / ES / Chroma Go |
| Embedding | BGE-M3 (BAAI) / text-embedding-3-small | 本地 ollama 部署 |
| MCP Go SDK | mcp-go (mark3labs/mcp-go) | 自建 JSON-RPC |
| OpenTelemetry | otelgo | openzipkin |
| 评测框架 | Eino-ext Devops Eval | 自建 Runner |
| 配置中心 | Nacos Go SDK | etcd / Apollo |
| 流式输出 | WebSocket + SSE | 长轮询 |
