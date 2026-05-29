# LLMVM 架构评审：可收敛的问题清单

本文档记录一次客观评审中识别出的、值得收敛的问题。已排除"两套 context 装配路径"这一误判（精确检索 vs 模糊检索是独立维度，`buildGlobalContext` 已是 `BuildContextPack` 的薄壳）。

整体判断：骨架（AST + stateless prompt + budget-driven + position）清晰且少见；当前实现层有"中期复杂度膨胀"迹象——抽象比能力跑得快。最该砍的不是新功能，是已有的冗余路径与失效保护。

---

## 1. Stagnation detection 实际不响（高优先级）

### 现状

`pkg/runtime/execute.go:149`：

```go
if output.Response == r.lastResponse {
    r.stagnationCount++
```

整段原始 JSON 字符串完全相等比较。`stagnationCount >= 2` 触发警告，`>= 4` 强制 Failed。

### 问题

**真 LLM 几乎不会逐字符重复**。temperature > 0、thoughts 文字漂移、字段顺序、空白都会导致 hash 不同。结果：

- 计数器在生产路径上**几乎到不了 2**，更到不了 4
- 这套机制只在 `StubEngine` canned response 重发时有意义——是测试夹具的副作用，被升格成了"生产安全阀"

由于"无限连续推理"已删除 `maxIterations` 硬上限（见 `project_goals`），stagnation 是**唯一的**节点级安全阀。它不响等于没有安全阀。

### 真正的死循环模式

字符串比较看不见的情况：

- 同一个 grep 反复改 pattern 都搜不到（command 字符串变了）
- 同一文件分多段读（artifact ID 不同）
- `mark_complete` 被 acceptance 拒了之后再次 `mark_complete`（thoughts 不同）

### 改法（按代价排序）

**选项 A — 行为指纹**：只对 `Action` 数组做指纹，不看 thoughts。

```go
// 伪码
parts := []string{}
for _, a := range response.Actions {
    parts = append(parts, a.ActionType, a.Command, a.FilePath, a.ArtifactID)
}
fingerprint := sha256(strings.Join(parts, "|"))
```

特定 action 给更短阈值（如 `mark_complete` 被拒 2 次直接 Failed）。

**选项 B — 进展指纹（更 Occam）**：完全删掉 stagnation，改成"同一节点连续 N 个 turn 没产生新 artifact 且节点结构化字段无变化"。这是真正的"无进展"定义，AST + artifact store 已有数据，不增加新状态。

推荐 B：在"无限推理"语义下，"无进展"比"重复响应"是更准确的失败信号。

---

## 2. Prompt 预算口径在两处算（中优先级）

### 现状

`pkg/runtime/prompt.go:129-135`：

```go
totalCharBudget := r.budget.ContextBudget * 4 * 80 / 100
remainingChars := totalCharBudget - fixedChars
globalContextBudget := remainingChars * 50 / 100
variablesBudget := remainingChars * 40 / 100
```

写死 80% / 50% / 40% 三层比例，与 `BudgetConfig` 已有的子预算（`ContextTokenLimit` / `RetrievalTokenLimit` / `ArtifactInlineTokenLimit`）重叠。

### 问题

- 同一个"分给上下文 X tokens"的决策在两处算，互不感知
- `BudgetConfig` 是构造期参数化的，prompt 里的硬编码不是——改预算策略要改两处
- `* 4 * 80 / 100` 这种 token→char 换算系数散落在各处（`tokens.go` 也有自己的估算）

### 改法

把比例写进 `BudgetConfig`（`PromptVariablesShare`、`PromptContextShare`），prompt 模板只读字段。统一 token↔char 换算到一个 helper。

---

## 3. 死代码（低优先级、纯清理）

`pkg/runtime/prompt.go`:

- `buildPrompt`（13 行）：不带 globalContext 的版本，无调用方
- `buildPromptWithWorkspace`（23 行）：旧 workspace 接口，无调用方

只有 `buildPromptWithGlobalContext` 在 `execute.go:88` 被调。前两个删除即可，不影响行为。

---

## 4. Authority 几乎没区分度（中优先级）

### 现状

`pkg/runtime/position.go:127-183`：四个 role 的 `AuthorityDescriptor`。Root / Planner / Executor / ErrorHandler **真正差异只有 `CanCreateChild` 一项**：

| Role | CanCreateChild | CanAppendSibling | 其它 10 个字段 |
|---|---|---|---|
| Root | ✓ | ✗ | 全 ✓ |
| Planner | ✓ | ✓ | 全 ✓ |
| Executor | ✗ | ✓ | 全 ✓ |
| ErrorHandler | ✓ | ✗ | 全 ✓ |

### 问题

- Position 的概念有了（`BuildPosition` 推导、`CheckAuthority` fail-fast 校验），但能力没下沉——目前更像装饰
- 文档 `Cannot create child nodes; if work needs decomposition, append a sibling planner` 暗示 Executor 应该受限更多，但实际上能写文件、能 shell、能查内存——和 Planner 同等危险

### 改法（按野心排序）

**A. 删除冗余 role**：合并成 Planner / Executor 两类，差异点也只剩 `CanCreateChild`。如果只想编码"能否往下分解任务"，用一个 bool 就够，整个 `AuthorityDescriptor` 是过度设计。

**B. 让 role 真正约束**：
- Executor 默认禁 `execute_command`（强制走 sandboxed `read_file`/`write_file`/`search`），需要 shell 时显式 `request_context` 升权
- Root 禁 `execute_command`（顶层不该跑 shell）
- ErrorHandler 默认只读，禁所有 mutation 直到诊断完成

如果不打算做 B，就该选 A——保留 4 个 role 但只编码 1 个 bool 是负资产。

---

## 5. execute_command 是沙箱的洞（中优先级，安全相关）

### 现状

`read_file` / `write_file` / `list_dir` / `search` / `append_to_file` 走 `sandboxPath`，强制留在 `test/sandbox/` 下。

`execute_command` 直接 `sh -c`，**不沙箱**。CLAUDE.md 把它叫"特权 escape hatch"。

### 问题

LLM 完全可以用 `execute_command rm -rf /path` 绕开所有沙箱检查。在"无限连续推理"语义下，stagnation 又抓不住意外行为（见问题 1），后果不可控。

沙箱的安全价值取决于最弱的工具。当前最弱的工具是无沙箱的——其它工具的沙箱代码本质上是**自欺**。

### 改法

**A. 默认禁用，按需提权**：`execute_command` 默认走允许列表（`go test` / `go build` / `grep` / 几个常用工具），通配命令需要 `request_human_input` 拿到一次性授权。

**B. chroot 或 OS 级沙箱**：macOS `sandbox-exec`、Linux `bwrap`/`firejail`。代价：增加平台依赖。

**C. 接受现状，但写明**：在 system prompt 里明确"`execute_command` 是不受限通道，模型应优先使用 `read_file`/`search` 等沙箱工具"。代价低，但只是约定，不是约束。

短期 C，中期 A。B 收益不抵成本。

---

## 6. ~~Agentic loop 翻转 WetherTraveled 是 cursor 抽象的漏洞~~（已撤回）

### 撤回理由

原批评建议给 cursor 加显式的 `Stay()` 操作，让"留守"成为一等概念。这是把抽象洁癖当成问题。

实际设计意图相反：**Agentic loop 本身要尽量轻**——两行 flag 翻转就够了。复杂度不应该塞进 loop 机制本身，而是当**预算压力到来时**，开 sub-goroutine 做：

- 异步压缩（让 main loop 用降级 context 先继续，async worker 算好新 summary 再 swap）
- 异步精确检索（rerank / resolver 缩小走 worker，不阻塞 turn）

`IndexNodeArtifactsAsync` 已经是这个模式（mark_complete 后异步 embed）。把它扩展到压缩/检索路径才是正确方向，而不是给 loop 加仪式感。

`WetherTraveled = false` 重用现有字段确实有语义混叠的味道，但代价是**新增字段或新增 cursor 状态机**——后者更重。在"loop 是廉价机制"的前提下，重用 flag 是合理取舍。

### 真正可以做的（独立 issue）

把 `execute.go:99-117` 的同步压缩升级路径改造成"main loop 不阻塞 + async worker 提供更好压缩"。这是新增能力，不是修复 bug。等到压缩成为瓶颈再做。

---

## 优先级建议

按"修复成本 × 实际伤害"排序：

1. **问题 1（stagnation）**——安全阀失效，直接影响"无限推理"目标的可用性。改成行为/进展指纹是低成本高收益。
2. **问题 3（死代码）**——10 分钟清理。
3. **问题 2（预算重叠）**——下次改预算策略时一并做。
4. **问题 4（authority 区分度）**——选定 A 或 B 之前不要再加 role。
5. **问题 5（execute_command）**——短期写文档，长期做允许列表。
6. ~~问题 6（agentic loop）~~ —— 已撤回。Loop 机制本身就该轻；复杂度应该放在"预算压力触发的 async worker"那一侧，不是 loop 本身。

---

## 不在本清单的事

- "两套 context 装配路径"——误判，已撤回。`buildGlobalContext` 是 `BuildContextPack` 的薄壳，精确（`NodeActivation`：祖先链/兄弟 handoff）与模糊（`Retrieved`/`ResolvedSpans`：跨节点语义召回）是独立维度，合并会破坏语义。
- "检索栈过厚"（FTS + 向量 + LRU + spill + resolver）——研究性 runtime，组件维护成本是探索本身的一部分，不算问题。
