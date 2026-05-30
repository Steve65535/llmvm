# ArtifactGetter as an isolated mini agentic-loop + unify all large-output paths

## 目标

把当前 `artifactGetterSummary`（把一整坨文本塞进 prompt 让 LLM 总结）替换成你描述的正确模型：

> ArtifactGetter 和 main loop 用同一个 engine、同一套 ReAct 协议，但**上下文隔离**——它只知道 artifact ID(指针) + goal + acceptance criteria，通过工具**主动**探索 artifact（按行切片 / 关键词搜索），而不是被动接收全文。输出是"我主动找到的精确上下文"，返回 main loop 写进 history。

同时做**评价 2 的统一化**：`execute_command`、`read_file`、`search`、`read_artifact` 四条大输出路径全部走同一个 ArtifactGetter 入口。

## 为什么这样设计（和当前实现的本质区别）

| | 当前 `artifactGetterSummary` | 新 ArtifactGetter mini-loop |
|---|---|---|
| LLM 看到什么 | 原始输出前 12000 chars 直接进 prompt | 只看到 artifact 元信息(ID/类型/总行数/summary) + goal |
| 获取方式 | 被动接收全文 | 主动 `read_artifact_slice` / `search_in_artifact` 探索 |
| 轮次 | 单次 call | ReAct 小循环（≤5 轮） |
| 上下文污染 | 大输出污染 getter 的 prompt | getter 的 context 完全隔离，main loop 状态不进入，大输出也不整体进入 |
| 和 main loop 关系 | 不同的 prompt，本质是另一个总结器 | 同一个 engine + 同构的 action 协议，只是工具集更小、上下文被隔离 |

## 新增文件：`pkg/runtime/artifact_getter.go`

一个自包含的 ReAct mini-loop。**不复用 main loop 的 prompt/parser**（main loop 的 action schema 太大，会让 getter 越权）；用一个**最小 action 子集**，自己的 system prompt，自己的 parse。

### Getter 的 action 子集（自定义最小 JSON 协议）

```jsonc
// 探索：读 artifact 的某几行
{"tool": "read_slice", "start_line": 40, "end_line": 80}
// 探索：在 artifact 内容里关键词搜索（返回命中行号 + 上下文）
{"tool": "search", "keyword": "panic"}
// 结束：输出最终摘要（纯文本）
{"tool": "done", "summary": "..."}
```

用一个独立的小 struct + `encoding/json` 解析，**不动 `pkg/llm/parser.go`**（那是 main loop 的契约，getter 的协议是 runtime 内部私有的，没必要污染全局 schema）。

### Getter 循环结构（镜像 main loop 的 call→parse→act，但工具在 artifact 内）

```
runArtifactGetter(artifactID, goal, acCriteria, exitFailed) string:
    art := r.artifacts.Get(artifactID)          // 拿元信息：TotalLines / Type / Summary
    transcript := []                              // getter 自己的 observation 历史（隔离的）
    for turn in 0..maxTurns(=5):
        prompt := buildGetterPrompt(art, goal, acCriteria, exitFailed, transcript)
        out := r.engine.Call(prompt)              // 同一个 engine
        tool := parseGetterTool(out.Response)     // 私有最小 parser
        switch tool:
          read_slice: text := art.ReadSlice(...); transcript += observation(text 截断到单槽上限)
          search:     hits := searchInArtifact(artifactID, keyword); transcript += observation(hits)
          done:       return formatGetterResult(artifactID, tool.summary)
    // 用尽轮次没 done → 退化：resolver 关键词切片 → 仍失败 → 元信息占位摘要
    return fallbackSummary(artifactID, goal)
```

要点：
- **上下文隔离**：`transcript` 只含 getter 自己的探索结果，单槽覆盖式截断（每个 observation 截到 `MaxCommandResultChars`），main loop 的 history/variables 一概不进入。
- **超时**：整个 getter 循环包一层 `LeafTurnTimeout`（已有字段；没有则默认 60s）。用 goroutine + select 实现，超时立即退化到 resolver。
- **不递归**：getter 的工具里没有 `execute_command`，不会再触发 ArtifactGetter，无递归风险。
- **退化链**：done 正常 → 用尽轮次/parse 失败/超时 → resolver 切片 → resolver 也空 → 返回元信息占位（`[artifact=X type=Y total_lines=N summary=...]`），调用方兜底硬截断。

### `searchInArtifact(artifactID, keyword)` helper

artifact 内容已在 store 里（或 spill 到磁盘）。复用 `art.ReadSlice` 拿全文 → 逐行 `strings.Contains` → 返回 `行号: 内容`（命中 ±2 行上下文，最多 N 条）。纯本地，不调 grep（artifact 可能 spill 到临时文件，逐行更稳）。

## 统一化：四条大输出路径共用入口

新增一个统一入口：

```go
// 大输出 → ArtifactGetter 精确提取；返回写进调用方 history/variables 的摘要。
func (r *Runtime) summarizeLargeArtifact(art *artifact.Artifact, fullLen int, goal string, acCriteria []tasknode.AcceptanceCriterion, exitFailed bool) string
```

判定阈值统一为 `threshold := r.budget.ArtifactAsyncTokenThreshold * 4`（沿用现有 char 估算），`<=0` 时退回 `MaxHistoryEntryLength`。

改造四个 handler：

1. **`actionExecuteCommand`**（已部分实现）：把现有 `artifactGetterSummary(...)` 调用替换成 `summarizeLargeArtifact(...)`。逻辑位置不变（落 artifact 后、写 history 前）。

2. **`actionReadFile`**：当前只把内容落 artifact、`last_read` 存 ID，**不进 history**。改为：内容超阈值时跑 getter，把摘要写进 `parent.Variables["_artifact_view"]`（复用 read_artifact 的单槽展示位），让模型当轮就能看到"这个文件里和 goal 相关的部分"。未超阈值维持原样（只存 ID）。

3. **`actionSearch`**：同 read_file，超阈值时 getter 摘要写进 `_artifact_view`；search 结果通常已较小，多数情况不触发。

4. **`actionReadArtifact`**：当前超 `MaxCommandResultChars` 直接硬截断。改为：当请求的 slice 超阈值时，跑 getter（goal 取自 parent）提取相关部分，替代裸截断。**注意**：read_artifact 是模型显式按行请求，若模型给了明确 start/end 且范围本身不大，应尊重原样返回，只在范围过大时才介入 getter。

> goal 提取统一为 helper：`parent.Goal` → 空则 `parent.Information[0]` → 空则跳过 getter（无 goal 无法精确提取，直接硬截断）。
> acCriteria 取 `parent.AcceptanceCriteria`（可空，getter prompt 里作可选段）。

## 删除/收敛

- 旧的 `artifactGetterSummary`（把全文塞 prompt 的版本）删除，被 `summarizeLargeArtifact` + `runArtifactGetter` 取代。
- `formatResolvedSpans` 保留（退化链里 resolver 仍用）。

## 测试（`pkg/runtime/artifact_getter_test.go`）

用现有 `stubEngine` 模式（enqueue 预设响应）。stub 需支持返回 getter 协议的 JSON（`{"tool":...}`），所以测试用一个能识别 prompt 特征返回不同响应的小 stub，或扩展 `stubEngine` 顺序返回。

1. **happy path**：超长 command 输出 → getter `read_slice` → `done` → history 里是 getter summary 而非全文。断言 history entry 含 summary、不含全文尾部。
2. **search 工具**：getter 用 `search` 命中关键词 → done。断言摘要含命中行。
3. **用尽轮次退化**：stub 一直返回 `read_slice` 不 done → 触发 resolver 退化 → 断言返回 resolver spans 格式或占位。
4. **超时退化**：stub 阻塞 → 断言 getter 在 timeout 后退化，不挂死。
5. **read_file 超阈值**：断言 `_artifact_view` 被 getter 摘要填充。
6. **隔离性**：断言 getter 的 prompt 不包含 main loop 的 `command_output_history` / 兄弟 handoff（grep prompt 内容）。

## 验证

```bash
go build ./...
go test -race ./pkg/runtime/ -v -run ArtifactGetter
go test -race ./...        # 全量回归
```

## 不做（明确排除，对应之前讨论）

- **AgentJob snapshot / event-apply 边界**（Codex 1）：main loop 仍同步等 getter 返回——这是正确语义（history 需要摘要才能继续），不是缺口。
- **结构化 context buffer 分区**（Codex 3）：`short_observations / focused_slices / failure_state / handoff_draft` 是 RFC `LoopAttempt` 的事，独立大改，本次不做。
- **强 timeout 穿透 ctx 进 engine.Call**：getter 超时用 goroutine+select 软隔离，不改 `llm.Engine` 接口。
