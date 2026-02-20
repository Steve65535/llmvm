# LLMVM 调用逻辑缺陷总结

> 本文档总结当前项目运行逻辑中发现的潜在缺陷，供后续修复参考。
> 
> **审计版本 v2** (2026-02-19) — 经过源码逐行交叉验证

---

## 缺陷 1：`mark_complete` 对 Loop/Normal 节点无效 ✅ 已确认

**严重程度**：🔴 高

**位置**：`pkg/runtime/runtime.go` L870-887，`decideNextStep` L1222-1243

**现象**：
- `mark_complete`（L871）只设置 `parent.SingleFinished = true`
- `decideNextStep` 对 Loop 节点（L1224）只检查 `current.WetherFinished`，**从不检查 `SingleFinished`**
- 因此当 LLM 对 Loop 节点返回 `mark_complete` 试图结束循环时，`WetherFinished` 永远不会被设置为 true
- **Loop 将永远无法被 LLM 主动终止**

**根因**：
`SingleFinished` 这个概念是为 Leaf 的 Agentic Loop 设计的（`agentic_loop.go` L16 检查它）。但 `mark_complete` 是一个通用 Action，对于 Loop/Normal 节点，`SingleFinished` 被写入后无人读取，形成**死变量**。

**修复建议**：
在 `decideNextStep` 的 Loop 分支中增加：
```go
if current.WetherFinished || current.SingleFinished {
    r.cursor.MoveUp()
    return nil
}
```

---

## 缺陷 2：MaxRetries 后 `decideNextStep` 被调用两次 ✅ 已确认

**严重程度**：🟡 中

**位置**：`pkg/runtime/runtime.go` L191-200（内层 retry 循环），L220（外层主循环）

**现象**：
- L197：达到 MaxRetries 时，在内层 for 循环中调用了一次 `r.decideNextStep(current)`，然后 `break` 跳出内层循环
- L220：跳出内层循环后，外层主循环**再次无条件调用** `r.decideNextStep(current)`
- 结果：`decideNextStep` 对同一个节点被调用了**两次**

**后果**：
- Cursor 可能多移动一次（例如 MoveUp 两次，直接跳过了父节点）
- Loop 的变量传播逻辑（L1190-1202）也会被执行两次，但这通常是幂等的，影响较小
- **最严重的情况**：如果第一次 `decideNextStep` 把 Cursor 移到了父节点，第二次又在父节点上调用 `decideNextStep`，可能导致树的整体遍历顺序错乱

**修复建议**：
在 MaxRetries 分支的 `break` 之后，应 `continue` 跳过外层的 `decideNextStep`：
```go
if current.RetryCount > current.MaxRetries {
    // ...existing logic...
    if err := r.decideNextStep(current); err != nil {
        return err
    }
    break // 跳出内层循环
}
```
然后在外层，L220 之前添加一个 guard：
```go
// 如果内层已经处理过 decideNextStep（Failed 情况），跳过
if current.Status == tasknode.Failed {
    continue
}
if err := r.decideNextStep(current); err != nil {
    ...
}
```

---

## 缺陷 3：Failed Leaf 不会上移，死循环 ✅ 已确认（这就是 Raft 卡死的根因）

**严重程度**：🔴🔴 致命

**位置**：`pkg/runtime/runtime.go` L191-200；`pkg/runtime/agentic_loop.go` L11-41

**现象**：
这是 Raft SOTA 任务卡在 `phase2_leaf1` 的直接原因。执行流程如下：

1. LLM 对 `phase2_leaf1` 返回了一个无法解析的 JSON → `actionErr = true`
2. 内层重试循环的 `retryCount` 达到 `maxRetries=9` → 进入 L193 分支
3. L195：设置 `current.Status = tasknode.Failed`
4. L197：调用 `decideNextStep(current)` → 进入 `HandleLeafAgenticLoop`（L1205）
5. **关键**：`HandleLeafAgenticLoop` 检查的是 `SingleFinished`（L16）和 `IterationCount`（L26），**它完全不检查 `Status == Failed`**
6. 由于 `SingleFinished == false` 且 `IterationCount < MaxRetries(20)`，它走到了 L39：`current.WetherTraveled = false` + `IterationCount++`
7. 结果：Failed Leaf 被重新标记为"未遍历"，下一轮主循环又会重新处理它
8. 新一轮处理中，`retryCount` 被重置为 0（因为是新的一轮处理），于是它再次走完 10 次重试 → 再次 Failed → 再次被 Agentic Loop 重置 → **无限循环**

**为什么 stagnation 计数到了 87 还没停？**
因为 `stagnationCount` 是 Runtime 级别的全局变量（L35），不会在节点切换时重置。更关键的是，stagnation 检测只会把错误加入 `lastErr` 并递增 `retryCount`，但 `retryCount` 达到上限后节点被 Failed → 又被 Agentic Loop 复活 → retryCount 又从 0 开始。所以 stagnationCount 只增不减，形成了日志中看到的 `Level 87/3` 的奇观。

**修复建议（最关键的修复）**：
在 `HandleLeafAgenticLoop` 最前面增加对 `Failed` 状态的检查：
```go
func (r *Runtime) HandleLeafAgenticLoop(current *tasknode.TaskNode) bool {
    if current.Type != tasknode.Leaf {
        return false
    }

    // 🔧 FIX: 如果节点已经 Failed，强制完成并上移
    if current.Status == tasknode.Failed || current.WetherFinished {
        fmt.Printf("  ❌ Leaf [%s] is Failed/Finished, forcing MoveUp\n", current.ID)
        r.clearLeafScratchpad(current)
        if !current.WetherFinished {
            current.MarkFinished()
        }
        r.cursor.MoveUp()
        return true
    }

    // ...existing SingleFinished and IterationCount logic...
}
```

---

## 缺陷 4：`loadPath` 时 `Information[0]` 可能越界 ✅ 已确认

**严重程度**：🟢 低

**位置**：`cmd/main.go` L51

**现象**：
- `initialRequest = root.Information[0]` 在 `root.Information` 为空切片时会 panic（index out of range）
- 仅在 `--load` 加载了一个异常的 JSON 文件（Information 字段为空数组）时触发

**修复建议**：
```go
if len(root.Information) > 0 {
    initialRequest = root.Information[0]
} else {
    log.Fatalf("❌ Loaded state has no initial request in root.Information")
}
```

---

## 🆕 缺陷 5：Stagnation 计数器跨节点泄漏（新发现）

**严重程度**：🟡 中

**位置**：`pkg/runtime/runtime.go` L34-35（定义），L160-171（使用）

**现象**：
- `lastResponse` 和 `stagnationCount` 是挂在 `Runtime` 结构体上的全局变量
- 当 Cursor 从节点 A 移动到节点 B 时，这两个变量**不会被重置**
- 如果节点 A 的最后一次 LLM 回复恰好和节点 B 的首次回复相同（在任务描述相似时很可能发生），系统会**错误地触发 Stagnation 检测**
- 这会导致一个正常节点的首次调用就因为"与上一个节点的回复相同"而浪费一次重试

**修复建议**：
在节点切换时（如 `decideNextStep` 的 `MoveUp`/`MoveDown` 之后，或主循环的新一轮开始时）重置状态：
```go
// 在主循环，获取 current 之后
current := r.cursor.Current
if current.ID != r.lastNodeID {
    r.lastResponse = ""
    r.stagnationCount = 0
    r.lastNodeID = current.ID
}
```

---

## 修复优先级建议

| 优先级 | 缺陷 | 原因 |
|--------|------|------|
| **P0** | **缺陷 3** | Raft 卡死的直接根因。Failed Leaf → Agentic Loop 复活 → 无限循环 |
| **P0** | **缺陷 1** | Loop 节点永远无法被 `mark_complete` 终止 |
| **P1** | **缺陷 2** | `decideNextStep` 双重调用导致 Cursor 跳跃 |
| **P1** | **缺陷 5** | Stagnation 检测跨节点误报 |
| **P2** | **缺陷 4** | 仅 `--load` 异常数据时触发 |