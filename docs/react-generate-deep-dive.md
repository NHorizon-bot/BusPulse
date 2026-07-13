# `a.reactAgent.Generate()` 深度解析：一行代码背后的 ReAct 循环

> 本文档基于 Eino v0.9.12 源码（`github.com/cloudwego/eino@v0.9.12`），逐层解析 `react.Agent.Generate()` 的内部执行机制。

---

## 目录

1. [问题：为什么一行代码能驱动多轮循环？](#1-问题为什么一行代码能驱动多轮循环)
2. [全局视角：Generate 的调用栈](#2-全局视角generate-的调用栈)
3. [第 1 步：NewAgent — 构建 ReAct 有向图](#3-第-1-步newagent--构建-react-有向图)
4. [第 2 步：Agent.Generate — 入口与委托](#4-第-2-步agentgenerate--入口与委托)
5. [第 3 步：runner.run — 核心执行循环](#5-第-3-步runnerrun--核心执行循环)
6. [第 4 步：modelPostBranchCondition — 决定下一步去向](#6-第-4-步modelpostbranchcondition--决定下一步去向)
7. [第 5 步：ToolsNode.Invoke — 并行执行工具调用](#7-第-5-步toolsnodeinvoke--并行执行工具调用)
8. [第 6 步：state 机制 — messages 累加的秘密](#8-第-6-步state-机制--messages-累加的秘密)
9. [第 7 步：熔断保护 — MaxRunSteps 如何阻止死循环](#9-第-7-步熔断保护--maxrunsteps-如何阻止死循环)
10. [完整执行示例：以 diagnose 请求为线索](#10-完整执行示例以-diagnose-请求为线索)
11. [总结：一行代码到底做了什么](#11-总结一行代码到底做了什么)

---

## 1. 问题：为什么一行代码能驱动多轮循环？

```go
output, err := a.reactAgent.Generate(ctx, []*schema.Message{userMsg})
```

这是 BusPulse 诊断 Agent 的核心调用。一眼看去，它只是一个普通的函数调用：

- **入参**：1 条 User Message
- **出参**：LLM 最终回答的 1 条 Message

但实际内部执行了**最多 10 步（step）** 的图执行，包含多次 LLM API 调用和多次工具调用。

**核心秘密**：`react.NewAgent` 在 `NewAgent` 时构建的不是一个"模型调用器"，而是一个**有状态、有循环的有向图（Graph）**。`Generate` 只是触发了这个图的执行引擎 `runner.run()`，而 runner 内部有一个 `for step := 0; ; step++` 的主循环。

---

## 2. 全局视角：Generate 的调用栈

```
main.go:  handler.DiagnoseHandler.ServeHTTP()
               │
               ▼
handler/diagnose.go:  h.diagnoser.Diagnose(ctx, domainReq)
               │
               ▼
agent/agent.go:  DiagAgent.Diagnose()
               │
               ▼
               a.reactAgent.Generate(ctx, []*schema.Message{userMsg})    ← 本节核心
               │
               ▼
react/react.go:  r.runnable.Invoke(ctx, input, opts...)                  ← 委托给 graph 的 runnable
               │
               ▼
compose/graph_run.go:  runner.invoke() → runner.run()                   ← 这里是真正的主循环！
               │
               │  for step := 0; ; step++ {
               │      // 有向图执行引擎，每一"步"执行一批就绪的节点
               │  }
               │
               ▼
         ┌─────────────────────────────────────────────────────────┐
         │  每步执行的节点（由图的拓扑结构和分支条件决定）            │
         │                                                         │
         │  第 1 步: START → ChatModel      ← 调 LLM API           │
         │  第 2 步: ChatModel → Tools       ← 执行工具             │
         │  第 3 步: Tools → ChatModel       ← 再次调 LLM API       │
         │  第 4 步: ChatModel → END         ← 无 tool_call，返回   │
         └─────────────────────────────────────────────────────────┘
```

---

## 3. 第 1 步：NewAgent — 构建 ReAct 有向图

### 源码位置：`react/react.go:284-397`

```go
func NewAgent(ctx context.Context, config *AgentConfig) (_ *Agent, err error) {
    // ── 解析配置 ──
    chatModel := config.ToolCallingModel  // DeepSeek ChatModel
    messageModifier := config.MessageModifier  // NewPersonaModifier(systemPrompt)

    // ── 获取工具信息 ──
    toolInfos, _ = genToolInfos(ctx, config.ToolsConfig)  // 8 个工具的 JSON Schema

    // ── 绑定工具到模型 ──
    chatModel, _ = ChatModelWithTools(config.Model, config.ToolCallingModel, toolInfos)

    // ── 注册工具节点中间件（用于收集工具结果） ──
    config.ToolsConfig.ToolCallMiddlewares = append(
        []compose.ToolMiddleware{newToolResultCollectorMiddleware()},
        config.ToolsConfig.ToolCallMiddlewares...,
    )

    // ── 创建 ToolsNode ──
    toolsNode, _ = compose.NewToolNode(ctx, &config.ToolsConfig)

    // ── 创建图 ──
    graph := compose.NewGraph[[]*schema.Message, *schema.Message](
        compose.WithGenLocalState(func(ctx context.Context) *state {
            return &state{Messages: make([]*schema.Message, 0, config.MaxStep+1)}
        }),
    )

    // ── 添加 ChatModel 节点（带 PreHandler：追加消息到 state.Messages） ──
    modelPreHandle := func(ctx context.Context, input []*schema.Message, state *state) ([]*schema.Message, error) {
        state.Messages = append(state.Messages, input...)  // ← 关键：消息累加
        if config.MessageRewriter != nil {
            state.Messages = config.MessageRewriter(ctx, state.Messages)
        }
        if messageModifier == nil {
            return state.Messages, nil
        }
        modifiedInput := make([]*schema.Message, len(state.Messages))
        copy(modifiedInput, state.Messages)
        return messageModifier(ctx, modifiedInput), nil  // ← 注入 System Prompt
    }
    graph.AddChatModelNode("chat", chatModel, compose.WithStatePreHandler(modelPreHandle))
    graph.AddEdge(START, "chat")  // START → ChatModel

    // ── 添加 Tools 节点（带 PreHandler） ──
    toolsNodePreHandle := func(ctx context.Context, input *schema.Message, state *state) (*schema.Message, error) {
        state.Messages = append(state.Messages, input)  // ← 追加 LLM 回复到 state
        state.ReturnDirectlyToolCallID = getReturnDirectlyToolCallID(input, config.ToolReturnDirectly)
        return input, nil
    }
    graph.AddToolsNode("tools", toolsNode, compose.WithStatePreHandler(toolsNodePreHandle))

    // ── 设置分支条件：ChatModel → 根据输出是否有 tool_call 决定去向 ──
    modelPostBranchCondition := func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (string, error) {
        if isToolCall, err := toolCallChecker(ctx, sr); err != nil {
            return "", err
        } else if isToolCall {
            return "tools", nil   // → 有 tool_call → 去 Tools 节点
        }
        return compose.END, nil   // → 无 tool_call → 直接输出
    }
    graph.AddBranch("chat", compose.NewStreamGraphBranch(modelPostBranchCondition, ...))

    // ── 设置 Tools 节点后的分支 ──
    // 判断是否有 tool 标记了 ReturnDirectly，决定返回 END 还是回到 ChatModel
    buildReturnDirectly(graph)    // → 默认无 ReturnDirectly 时返回 ChatModel

    // ── 编译图（设置 MaxRunSteps 限制循环次数） ──
    compileOpts := []compose.GraphCompileOption{
        compose.WithMaxRunSteps(config.MaxStep),  // ← 熔断保护
        compose.WithNodeTriggerMode(compose.AnyPredecessor),
    }
    runnable, _ := graph.Compile(ctx, compileOpts...)

    return &Agent{runnable: runnable, graph: graph}, nil
}
```

### 构建好的 ReAct 图结构

```
                    ┌──────────────────────────────────────┐
                    │              graph                    │
                    │                                       │
                    │    ┌──────────┐                       │
    input ──────────►   │  START    │                       │
                    │    └────┬─────┘                       │
                    │         │                             │
                    │  [edge] │                             │
                    │         ▼                             │
                    │  ┌──────────────┐                     │
                    │  │  ChatModel   │  ← LLM API 调用      │
                    │  │  (chat)      │                     │
                    │  └──────┬───────┘                     │
                    │         │                             │
                    │  [branch: modelPostBranchCondition]   │
                    │         │                             │
                    │    ┌────┴────────┐                    │
                    │    │             │                    │
                    │    ▼             ▼                    │
                    │  (有tool_call)   (无tool_call)        │
                    │    │             │                    │
                    │    ▼             │                    │
                    │  ┌──────────┐    │                    │
                    │  │  Tools   │    │                    │
                    │  │  (tools) │    │                    │
                    │  └────┬─────┘    │                    │
                    │       │          │                    │
                    │  [branch: ReturnDirectly?]            │
                    │    │          │                       │
                    │    ▼          │                       │
                    │  (回到 Chat)  │                       │
                    │    │          │                       │
                    │    ▼          ▼                       │
                    │  ┌────────────────┐                   │
                    │  │      END       │                   │
                    │  │  (最终输出)     │                   │
                    │  └────────────────┘                   │
                    └──────────────────────────────────────┘
```

这个图的有向边 `ChatModel → ChatModel` 通过 `Tools` 节点**间接形成了一条循环路径**：
`START → ChatModel → Tools → ChatModel → Tools → ... → END`

---

## 4. 第 2 步：Agent.Generate — 入口与委托

### 源码位置：`react/react.go:480-482`

```go
// Generate 就一行代码——直接委托给图的 Invoke
func (r *Agent) Generate(ctx context.Context, input []*schema.Message, opts ...agent.AgentOption) (*schema.Message, error) {
    return r.runnable.Invoke(ctx, input, agent.GetComposeOptions(opts...)...)
}
```

`r.runnable` 的类型是 `compose.Runnable[[]*schema.Message, *schema.Message]`：
- `Invoke(ctx, input)` → 同步调用，阻塞直到图执行完毕

这个 `runnable` 是在 `graph.Compile()` 时生成的。Compile 把图编译为一个 `runner` 实例，封装为 `composableRunnable`。

`runner.toComposableRunnable()`（`graph_run.go:994-1018`）把 runner 包装为符合 `Runnable` 接口的对象：

```go
func (r *runner) toComposableRunnable() *composableRunnable {
    return &composableRunnable{
        i: func(ctx context.Context, input any, opts ...any) (output any, err error) {
            tos, _ := convertOption[Option](opts...)
            return r.invoke(ctx, input, tos...)  // 调用 runner.invoke
        },
        // ...
    }
}
```

---

## 5. 第 3 步：runner.run — 核心执行循环

### 源码位置：`compose/graph_run.go:109-359`

这是整个框架**最核心的执行引擎**。它实现了一个**类似 Pregel 的超级步（superstep）并行调度算法**：

```go
func (r *runner) run(ctx context.Context, isStream bool, input any, opts ...Option) (result any, err error) {
    // ── 1. 初始化 ──
    cm := r.initChannelManager(isStream)  // 初始化通道管理器
    tm := r.initTaskManager(...)          // 初始化任务管理器
    maxSteps := r.options.maxRunSteps     // 最大步数（MaxStep）

    // ── 2. 计算初始任务：从 START 节点出发 ──
    nextTasks, result, isEnd, err = r.calculateNextTasks(ctx, []*task{{
        nodeKey: START,
        call:    r.inputChannels,
        output:  input,
    }}, ...)

    // ── 3. 主执行循环（for step） ──────────────────────────────────
    //    这是 Eino 图执行中最核心的循环机制
    //    每一"步"（superstep）执行一批就绪的节点
    //    循环直到到达 END 节点或步数超限
    for step := 0; ; step++ {

        // ── 3a. 检查 context 是否已取消 ──
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        default:
        }

        // ── 3b. 熔断检查：非 DAG 且 step 超过 maxSteps ──
        if !r.dag && step >= maxSteps {
            return nil, newGraphRunError(ErrExceedMaxSteps)  // ← 抛出"超出最大步数"错误
        }

        // ── 3c. 提交任务 ──
        //     将"就绪的节点"提交给线程池执行
        //     每个 task 对应一个图中的节点（如 ChatModel、Tools）
        err = tm.submit(nextTasks)

        // ── 3d. 等待任务完成 ──
        //     阻塞直到所有已提交的任务完成（或取消）
        completedTasks, canceled, canceledTasks := tm.wait()

        // ── 3e. 检查是否有中断（interrupt/resume 机制） ──
        //     处理子图中断、rerun 节点等复杂情况
        err = r.resolveInterruptCompletedTasks(tempInfo, completedTasks)
        //     如果涉及子图中断/rerun，进行特殊处理

        // ── 3f. 计算下一步的任务 ──
        //     将已完成节点的输出写入通道
        //     通过通道检查哪些下游节点变为就绪
        //     对 ChatModel 节点：执行分支逻辑（modelPostBranchCondition）
        //        → 有 tool_call 就去 Tools 节点
        //        → 无 tool_call 就去 END 节点
        nextTasks, result, isEnd, err = r.calculateNextTasks(ctx, completedTasks, ...)

        // ── 3g. 判断是否到达 END 节点 ──
        if isEnd {
            return result, nil  // ← 循环结束，返回最终结果
        }

        // ── 3h. 处理中断逻辑（HITL：Human-in-the-Loop） ──
        //     如果配置了 interruptBefore/AfterNodes，在此处理
        //     允许人工介入暂停/修改诊断流程
    }
}
```

**关键理解**：每一"步"可能只执行**一个节点**（如 ChatModel），也可能并发执行**多个节点**（如多个 Tools 可以并行跑）。

在 ReAct 图中，典型的执行轨迹是：

| step | 执行的节点 | 说明 |
|------|-----------|------|
| 0 | START | 初始化，准备输入 |
| 1 | ChatModel | 第一次调 LLM，输出 tool_calls |
| 2 | Tools | 并发执行所有 tool_call |
| 3 | ChatModel | 第二次调 LLM，观察工具结果 |
| 4 | Tools | 如果有更多 tool_call |
| ... | ... | 直到无 tool_call，去 END |
| N | END | 返回最终结果 |

---

## 6. 第 4 步：modelPostBranchCondition — 决定下一步去向

### 源码位置：`react/react.go:369-376`

这是决定 ReAct 能否循环的关键——ChatModel 节点后的分支逻辑：

```go
modelPostBranchCondition := func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (endNode string, err error) {
    // 检查 LLM 的输出流中是否包含 tool_call
    if isToolCall, err := toolCallChecker(ctx, sr); err != nil {
        return "", err
    } else if isToolCall {
        return nodeKeyTools, nil   // → 有 tool_call → 去 Tools 节点执行工具
    }
    return compose.END, nil        // → 无 tool_call → 走到 END，输出最终回答
}
```

### StreamToolCallChecker 的使用

```go
func firstChunkStreamToolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
    defer sr.Close()
    for {
        msg, err := sr.Recv()
        if err == io.EOF {
            return false, nil     // 流结束仍无 tool_call → 最终回答
        }
        if err != nil {
            return false, err
        }
        if len(msg.ToolCalls) > 0 {
            return true, nil      // 发现 tool_call → 继续循环
        }
        if len(msg.Content) == 0 {
            continue              // 跳过空 chunk
        }
        return false, nil         // 有内容但没有 tool_call → 最终回答
    }
}
```

这个 branch 被注册为 ChatModel 节点的后继分支：

```go
graph.AddBranch("chat", compose.NewStreamGraphBranch(
    modelPostBranchCondition,
    map[string]bool{"tools": true, compose.END: true},
))
```

在 `runner.run()` 的每一轮循环末尾，`calculateNextTasks()` 会调用 `calculateBranch()`（`graph_run.go:866-931`），根据 branch 函数的返回值决定下一步去哪个节点：

```go
func (r *runner) calculateBranch(ctx context.Context, curNodeKey string, startChan *chanCall, ...) ([]string, error) {
    for i, branch := range startChan.writeToBranches {
        // 执行分支函数
        ws, err := branch.invoke(ctx, input[i])
        // ws 是分支返回的节点名列表：["tools"] 或 ["__end__"]
        ret = append(ret, ws...)
    }
    return ret, nil
}
```

---

## 7. 第 5 步：ToolsNode.Invoke — 并行执行工具调用

### 源码位置：`compose/tool_node.go:1046-1144`

当分支将执行导向 Tools 节点时，`ToolsNode.Invoke()` 负责并发执行所有 tool_call：

```go
func (tn *ToolsNode) Invoke(ctx context.Context, input *schema.Message, opts ...ToolsNodeOption) ([]*schema.Message, error) {
    // ── 1. 解析 tool_calls ──
    // 从 input（LLM 的 Assistant Message）中提取所有 ToolCalls
    // 从 toolsTuple.indexes 按名字找到对应的工具实现
    tasks, _ := tn.genToolCallTasks(ctx, tuple, input, executedTools, executedEnhancedTools, false)

    // ── 2. 并发执行所有工具（默认并行） ──
    if tn.executeSequentially {
        sequentialRunToolCall(ctx, runToolCallTaskByInvoke, tasks, ...)
    } else {
        parallelRunToolCall(ctx, runToolCallTaskByInvoke, tasks, ...)  // ← 并发！
    }

    // ── 3. 收集结果 ──
    // 每个 task 的结果包装为 ToolMessage
    // 与 ToolCalls 的顺序一一对应
    for i := 0; i < n; i++ {
        output[i] = schema.ToolMessage(tasks[i].output, tasks[i].callID, schema.WithToolName(tasks[i].name))
    }

    return output, nil
}
```

### 并行执行实现（tool_node.go:985-1017）

```go
func parallelRunToolCall(ctx context.Context,
    run func(ctx2 context.Context, callTask *toolCallTask, opts ...tool.Option),
    tasks []toolCallTask, opts ...tool.Option) {

    if len(tasks) == 1 {
        run(ctx, &tasks[0], opts...)  // 只有一个工具，不用 goroutine
        return
    }

    var wg sync.WaitGroup
    for i := 1; i < len(tasks); i++ {
        wg.Add(1)
        go func(ctx_ context.Context, t *toolCallTask, opts ...tool.Option) {
            defer wg.Done()
            defer func() {
                if panicErr := recover(); panicErr != nil {
                    t.err = safe.NewPanicErr(panicErr, debug.Stack())
                }
            }()
            run(ctx_, t, opts...)     // 每个工具在独立 goroutine 中运行
        }(ctx, &tasks[i], opts...)
    }
    if !tasks[0].executed {
        run(ctx, &tasks[0], opts...)  // 第一个工具在主 goroutine 中运行
    }
    wg.Wait()
}
```

**这就是为什么 LLM 在同一轮发起多个 tool_call 时，工具会被并发执行**，互不阻塞。

### 单个工具的执行链

`runToolCallTaskByInvoke` (`tool_node.go:892-932`)：

```go
func runToolCallTaskByInvoke(ctx context.Context, task *toolCallTask, opts ...tool.Option) {
    // ── 工具调用信息注入 context ──
    ctx = callbacks.ReuseHandlers(ctx, &callbacks.RunInfo{Name: task.name, Type: ...})
    ctx = setToolCallInfo(ctx, &toolCallInfo{toolCallID: task.callID})
    ctx = appendToolAddressSegment(ctx, task.name, task.callID)

    // ── 执行最终的工具函数（经过中间件链） ──
    output, err := task.endpoint(ctx, &ToolInput{
        Name:      task.name,
        Arguments: task.arg,         // JSON 参数字符串
        CallID:    task.callID,
        CallOptions: opts,
    })
    task.output = output.Result       // 工具返回的字符串结果
    task.executed = true
}
```

#### 中间件链

在 `wrapToolCall` (`tool_node.go:576-593`) 中，工具调用被多层中间件包装：

```
endpoint → react 的 newToolResultCollectorMiddleware → 原始工具函数
```

`newToolResultCollectorMiddleware` (`react/react.go:65-125`) 用于收集工具结果，支持流式和非流式两种方式：

```go
func newToolResultCollectorMiddleware() compose.ToolMiddleware {
    return compose.ToolMiddleware{
        Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
            return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
                senders := getToolResultSendersFromCtx(ctx)
                output, err := next(ctx, input)     // 执行真正的工具
                if err != nil {
                    return nil, err
                }
                if senders != nil && senders.sender != nil {
                    senders.sender(input.Name, input.CallID, output.Result) // 发送结果
                }
                return output, nil
            }
        },
        // ... 还有 Streamable、EnhancedInvokable、EnhancedStreamable 的中间件
    }
}
```

---

## 8. 第 6 步：state 机制 — messages 累加的秘密

### 源码位置：`react/react.go:56-59`, `react/react.go:329-351`, `react/react.go:357-364`

ReAct 之所以能"记住"之前说了什么，是因为**图节点在每次触发时都会修改一个共享的 state**。

### state 定义

```go
type state struct {
    Messages                 []*schema.Message   // ← 累积的对话历史！
    ReturnDirectlyToolCallID string              // 用于 ReturnDirectly 特性
}
```

### PreHandler → 每次触发节点前追加消息

**ChatModel 节点**的 PreHandler（`react.go:333-347`）：

```go
modelPreHandle := func(ctx context.Context, input []*schema.Message, state *state) ([]*schema.Message, error) {
    state.Messages = append(state.Messages, input...)  // ← 追加本轮输入

    // 如果有 MessageRewriter（如消息压缩），先执行
    if config.MessageRewriter != nil {
        state.Messages = config.MessageRewriter(ctx, state.Messages)
    }

    // 如果有 MessageModifier（如 System Prompt 注入），
    // 复制后注入
    if messageModifier == nil {
        return state.Messages, nil
    }
    modifiedInput := make([]*schema.Message, len(state.Messages))
    copy(modifiedInput, state.Messages)
    return messageModifier(ctx, modifiedInput), nil
}
```

**Tools 节点**的 PreHandler（`react.go:357-364`）：

```go
toolsNodePreHandle := func(ctx context.Context, input *schema.Message, state *state) (*schema.Message, error) {
    if input == nil {
        return state.Messages[len(state.Messages)-1], nil  // 用于 resume
    }
    state.Messages = append(state.Messages, input)          // ← 追加 LLM 的回复（含 tool_calls）
    state.ReturnDirectlyToolCallID = getReturnDirectlyToolCallID(input, config.ToolReturnDirectly)
    return input, nil
}
```

### messages 随步骤变化的全过程

以 diagnose 请求为例，追踪 `state.Messages` 的变化：

```
初始状态: []

第1步 ChatModel PreHandler:
    ↳ 追加: [User Message(系统指令+问题)]
    → 传给 Model: System Prompt + [User]
    state.Messages = [User]

第1步 ChatModel Generate:
    → LLM 返回: Assistant Message(含 tool_calls: GetOrderContext, GetAlgoSnapshot)
    → 分支判断: 有 tool_call → 去 Tools 节点

第1步 Tools PreHandler:
    ↳ 追加: [Assistant(tool_calls)]
    state.Messages = [User, Assistant(tool_calls)]

第1步 Tools Invoke:
    → 并发执行 GetOrderContext + GetAlgoSnapshot
    → 返回: [ToolMessage(订单数据), ToolMessage(算法数据)]

第1步 Tools End:
    → 输出到通道的 ToolMessage 传给下一节点

    ↓ (wait for next step in runner.run)

第2步 ChatModel PreHandler:
    ↳ 追加: [ToolMessage(订单数据), ToolMessage(算法数据)]
    → 传给 Model: System Prompt + [User, Assistant(tool_calls), Tool, Tool]
    state.Messages = [User, Assistant(tool_calls), Tool, Tool]

第2步 ChatModel Generate:
    → LLM 看到工具结果后思考，输出最终结论（无 tool_call）
    → 分支判断: 无 tool_call → 去 END

最终 state.Messages = [User, Assistant(tool_calls), Tool, Tool, Assistant(最终JSON)]
```

---

## 9. 第 7 步：熔断保护 — MaxRunSteps 如何阻止死循环

### 配置来源

```go
// react/react.go:78 — BusPulse 的配置
maxStep := cfg.LLM.MaxIterations*2 + 2  // = 4*2+2 = 10
```

这个 `maxStep` 传入了编译选项：

```go
// react/react.go:386
compileOpts := append(compileOpts[:0],
    compose.WithMaxRunSteps(config.MaxStep),  // ← 设为 10
    compose.WithNodeTriggerMode(compose.AnyPredecessor),
    compose.WithGraphName(graphName),
)
runnable, err := graph.Compile(ctx, compileOpts...)
```

### 熔断逻辑（graph_run.go:241-251）

```go
for step := 0; ; step++ {
    // ── 检查 context 是否已取消 ──
    select {
    case <-ctx.Done():
        // 全局诊断超时（30s）也会触发此路径
        return nil, newGraphRunError(fmt.Errorf("context has been canceled: %w", ctx.Err()))
    default:
    }

    // ── 步数熔断：如果超过最大步数 ──
    if !r.dag && step >= maxSteps {
        return nil, newGraphRunError(ErrExceedMaxSteps)
    }

    // ... 继续执行
}
```

`maxRunSteps` 的实际校验在 `resolveMaxSteps` 中（`graph_run.go:362-380`）：

```go
func (r *runner) resolveMaxSteps(maxSteps int, opts []Option) (int, error) {
    // 对于非 DAG（含循环的图），必须有 maxSteps
    for i := range opts {
        if opts[i].maxRunSteps > 0 {
            maxSteps = opts[i].maxRunSteps
        }
    }
    if maxSteps < 1 {
        return 0, newGraphRunError(errors.New("max run steps limit must be at least 1"))
    }
    return maxSteps, nil
}
```

**为什么 LLM 输出最终结论也需要步数？** 因为当 LLM 在一轮中输出 tool_calls 时，channel 会分发到 Tools 节点，Tools 执行完毕后会回到 ChatModel。再下一轮如果 LLM 不再输出 tool_calls，branch 返回 END，循环结束。但如果 LLM 持续输出 tool_calls（如恶性循环或模型故障），步数熔断会在 10 步时终止。

---

## 10. 完整执行示例：以 diagnose 请求为线索

### BusPulse 实际的一次诊断请求

下面追踪 `a.reactAgent.Generate(ctx, []*schema.Message{userMsg})` 在 BusPulse 中实际执行的全过程。

### 配置参数

```yaml
llm:
  provider: deepseek
  model: deepseek-chat
  max_iterations: 4  # → MaxStep = 4*2+2 = 10
server:
  diagnose_timeout_ms: 30000  # 30s 全局超时
```

### 请求参数

```json
{"order_id":"test_001","city_id":"bj","free_text_context":"用户投诉附近有车但一直叫不到"}
```

### 执行轨迹（共 10 步，实际 4 步结束）

```
══════════════════════════════════════════════════════════════════════════
第 0 步: START
══════════════════════════════════════════════════════════════════════════
  初始化 channel，计算初始任务→去 ChatModel

══════════════════════════════════════════════════════════════════════════
第 1 步: ChatModel（第 1 轮）
══════════════════════════════════════════════════════════════════════════
  PreHandler: state.Messages = []
    ↳ 追加 userMsg → state.Messages = [User]
    ↳ MessageModifier 注入 System Prompt
  Generate: LLM 收到 [System, User]
  LLM 思考: "这是一个'有车叫不到'的客诉，属于R-04方向冲突类型。
             我需要先查订单上下文，再看算法派单矩阵。"
  LLM 输出: Assistant(Content="", ToolCalls=[
      GetOrderContext({"order_id":"test_001","city_id":"bj"}),
      GetAlgoSnapshot({"order_id":"test_001"})
  ])
  分支: StreamToolCallChecker → 检测到 ToolCalls → 去 Tools 节点!
  state.Messages 此时为: [User]

══════════════════════════════════════════════════════════════════════════
第 2 步: ToolsNode（第 1 轮）
══════════════════════════════════════════════════════════════════════════
  PreHandler: state.Messages = [User]
    ↳ 追加 Assistant(含 ToolCalls) → state.Messages = [User, Assistant(ToolCalls)]
  genToolCallTasks: 提取 ToolCalls → 映射到真实的工具函数
  parallelRunToolCall:
    Goroutine 1: GetOrderContext → simulateLatency(80ms)
      → 返回 {"order_id":"test_001","status":"DISPATCHED","cancel_count_24h":0,...}
      ↳ 经过 Sanitizer 脱敏截断
    Goroutine 2: GetAlgoSnapshot → simulateLatency(200ms)
      → 返回 {"filter_matrix":{"veh_002":["HEADING_MISMATCH"],"veh_003":["HEADING_MISMATCH"]},
             "heading_angle_deg":105,"eta_delta_min":8,...}
      ↳ 经过 Sanitizer 脱敏截断
  输出: [ToolMessage(订单数据), ToolMessage(算法数据)]
  分支: ReturnDirectly? → 否 → 回到 ChatModel!

══════════════════════════════════════════════════════════════════════════
第 3 步: ChatModel（第 2 轮）
══════════════════════════════════════════════════════════════════════════
  PreHandler: state.Messages = [User, Assistant(ToolCalls)]
    ↳ 追加 [ToolMessage, ToolMessage]
    → state.Messages = [User, Assistant(ToolCalls), ToolMessage, ToolMessage]
    ↳ MessageModifier 再次注入 System Prompt
  Generate: LLM 收到 [System, User, Assistant, Tool, Tool]
  LLM 观察: "订单已派单(dispatched)，24h取消次数=0，排除反作弊
              算法过滤矩阵显示2辆车因方向冲突被过滤(夹角105°>90°)
              已有一辆车veh_001被分配
              根因明确: ALGO_HEADING_MISMATCH"
  LLM 输出: Assistant(Content='{
    "issue_level":"P3",
    "root_cause_category":"ALGO_HEADING_MISMATCH",
    "root_cause_analysis":"...",
    "customer_service_script":"亲，您反馈的情况...",
    "recommended_actions":[...]
  }', ToolCalls=[])  ← 无 tool_call!
  分支: StreamToolCallChecker → 无 ToolCalls → 去 END!
  state.Messages 此时为: [User, Assistant(ToolCalls), Tool, Tool, Assistant(JSON)]

══════════════════════════════════════════════════════════════════════════
第 4 步: END
══════════════════════════════════════════════════════════════════════════
  runner.run 检测到 isEnd=true
  return result → Agent.Generate 返回最终 Message
  message.Content = '{"issue_level":"P3",...}'
══════════════════════════════════════════════════════════════════════════
总计: 10 步预算，实际 4 步结束
      LLM API 调用 2 次（第1轮思考+第2轮观察）
      工具调用 2 个（并发执行，一次完成）
      总耗时: 10938ms
```

### 如果 LLM 需要更多轮

假设问题更复杂，LLM 可能走更多步：

| Step | 节点 | LLM 看到的消息 | ToolCalls |
|------|------|---------------|-----------|
| 1 | ChatModel | System + User | GetOrderContext, GetTraceLog |
| 2 | Tools | — | 并发执行 2 个工具 |
| 3 | ChatModel | System + User + Tool(订单) + Tool(链路) | GetVehicleGPS |
| 4 | Tools | — | 执行 1 个工具 |
| 5 | ChatModel | System + User + Tool(订单) + Tool(链路) + Tool(GPS) | 无 → 输出结论 |
| 6 | END | — | 最终 JSON |

### 如果 LLM 出错或超时

```
第 1 步: ChatModel → DeepSeek API 超时 → Generate 返回 error
  → reactAgent.Generate() 返回 error
    → agent.Diagnose() 捕获 error
      → fallbackReport(reason="ReAct 推理失败：context deadline exceeded")
        → handler 返回降级报告
```

### 如果步数耗尽

```
第 9 步: ChatModel → 继续输出 tool_calls
第 10 步: Tools → 执行工具
第 11 步: 步数检查: step=10 >= maxSteps=10
    → return ErrExceedMaxSteps
    → fallbackReport("ReAct 推理失败：exceed max steps")
```

---

## 11. 总结：一行代码到底做了什么

```go
output, err := a.reactAgent.Generate(ctx, []*schema.Message{userMsg})
```

### 函数调用链

```
agent.Generate
  └→ runnable.Invoke (compose.Runnable)
       └→ runner.invoke
            └→ runner.run  ← 核心
                 ├→ 初始化 channel manager 和 task manager
                 ├→ 从 START 节点计算初始任务
                 └→ for step=0; ; step++ {  ← 主循环
                      ├→ 检查 ctx.Done() 和 maxSteps 熔断
                      ├→ tm.submit(nextTasks) — 提交就绪节点
                      ├→ tm.wait() — 等待节点完成
                      ├→ 解析完成结果（错误处理/中断处理）
                      └→ calculateNextTasks() — 计算下一步
                           ├→ resolveCompletedTasks — 通道传值
                           ├→ calculateBranch — 执行分支函数
                           │    └→ ChatModel 分支: 有 tool_call→Tools, 无→END
                           └→ createTasks — 创建下一批任务
                 }  ← 循环直到 isEnd=true
              └→ return result
```

### 背后的关键机制

| 机制 | 说明 | 源码位置 |
|------|------|---------|
| **有向图编排** | React Agent 被编译为一个图，节点为 ChatModel 和 Tools | `react/react.go:284-397` |
| **state 持久化** | 图编译时生成了一个共享 state，每次节点执行前 PreHandler 追加消息 | `react/react.go:329-347,357-364` |
| **branch 分支** | ChatModel 节点后的 streamGraphBranch 检查 LLM 输出是否包含 tool_call | `react/react.go:369-376` |
| **channel 通信** | 节点间通过 channel 传递数据，channel 存放下游节点需要的输入 | `compose/pregel.go:25-28` |
| **runner.run 循环** | 每轮（superstep）提交就绪节点、等待完成、计算下一轮任务 | `compose/graph_run.go:241-359` |
| **MaxRunSteps 熔断** | 非 DAG 图中 step 超过 maxSteps 时抛出错误，防止无限循环 | `compose/graph_run.go:249-251` |
| **工具并行执行** | 同一轮的多个 tool_call 通过 sync.WaitGroup 并发执行 | `compose/tool_node.go:985-1017` |
| **中间件链** | 工具执行经过多层中间件包装（回调收集、脱敏等） | `compose/tool_node.go:576-593` |

### 一句话总结

> **`a.reactAgent.Generate(ctx, msgs)` 不是一次"LLM 调用"，而是一次"有状态有向图的执行"——**
> 图包含 ChatModel 和 Tools 两个节点，通过 branch 条件形成循环。
> **循环引擎 `runner.run()` 的 `for step` 每轮执行一批就绪节点，并通过 PreHandler 不断累积消息历史（`state.Messages`），
> 直到 LLM 不再产生 tool_call（或步数耗尽），最终返回 LLM 的输出。**
>
> **这就是"一行代码驱动多轮循环"的全部秘密。**
