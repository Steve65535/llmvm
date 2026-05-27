# LLMVM 工具生态与可插拔工具系统改进计划

## 核心判断

LLMVM 当前已经有一组基础工具能力：

- `execute_command`
- `read_file`
- `write_file`
- `append_to_file`
- `list_dir`
- `search`
- `read_artifact`
- `query_memory`
- `request_human_input`
- `append_sibling_node`

这些工具足够支撑原型验证，但还不够支撑一个长期可扩展的 agent runtime。

现在的问题不是“再手写几个 action”就能解决，而是需要把工具从 runtime 内部硬编码能力升级成一套可发现、可校验、可授权、可观测、可热插拔的工具生态。

一句话：

> LLMVM 的工具系统应该从 hardcoded actions 演进为 runtime-managed tool plugins。

---

## 为什么工具生态很关键

Claude Code 和 Codex 的强项之一是工具 harness 成熟：读写文件、执行命令、搜索代码、应用 patch、跑测试、查看 diff、权限确认、MCP 或外部服务接入，这些都是 agent 真正完成工程任务的基础。

LLMVM 如果只停留在固定 JSON action 列表，会遇到几个瓶颈：

- 每加一个工具都要改 `llm.Action`、parser、prompt、runtime dispatch 和测试。
- 工具权限无法按节点位置细分。
- 工具输入输出 schema 无法自动暴露给模型。
- 工具执行结果缺少统一 provenance、日志和重试语义。
- 第三方工具无法独立开发和安装。
- 不同任务域需要不同工具集合，例如代码、浏览器、数据库、文档、图像、部署、测试。

因此工具生态应该成为 LLMVM 的一等架构层。

---

## 目标状态

目标是引入 Tool Registry 和 Plugin Runtime：

```text
Tool Plugin
  -> manifest
  -> input schema
  -> output schema
  -> permission policy
  -> executor
  -> observation formatter
  -> artifact writer
  -> runtime registry
  -> prompt tool catalog
```

模型不应该直接知道所有 runtime 内部实现。它应该只看到当前位置允许使用的工具目录：

```json
{
  "tool": "fs.read_file",
  "description": "Read a sandboxed file and store the result as an artifact.",
  "input_schema": {
    "path": "string"
  },
  "output": "artifact_ref"
}
```

Runtime 根据 tool name 找到注册工具，校验输入，检查权限，执行工具，保存 observation 和 artifact，然后把结果以结构化方式反馈给节点。

---

## 工具系统分层

建议分成五层：

### 1. Tool Definition

工具定义是工具的静态契约：

```go
type ToolDefinition struct {
    Name        string
    Version     string
    Description string
    Category    string
    InputSchema  JSONSchema
    OutputSchema JSONSchema
    Permissions  []Permission
    ProducesArtifact bool
    SideEffectLevel  SideEffectLevel
}
```

字段含义：

- `Name`：稳定工具名，例如 `fs.read_file`、`shell.exec`、`memory.query`。
- `Version`：工具 schema 版本。
- `Category`：`fs`、`shell`、`memory`、`artifact`、`browser`、`db`、`test` 等。
- `InputSchema`：模型必须遵守的输入 JSON schema。
- `OutputSchema`：runtime observation 的结构。
- `Permissions`：需要的权限。
- `ProducesArtifact`：是否默认把结果写入 artifact。
- `SideEffectLevel`：副作用等级。

### 2. Tool Executor

工具执行器是实际代码：

```go
type ToolExecutor interface {
    Definition() ToolDefinition
    Execute(ctx ToolContext, input json.RawMessage) (ToolResult, error)
}
```

`ToolContext` 应提供：

- current node
- position
- sandbox root
- artifact store
- memory store
- logger
- cancellation context
- budget
- environment policy

工具不应该直接绕过 runtime 状态写入。所有 artifact、memory、event log 都应通过 `ToolContext` 提供的受控 API。

### 3. Tool Registry

Registry 管理可用工具：

```go
type ToolRegistry interface {
    Register(tool ToolExecutor) error
    Lookup(name string) (ToolExecutor, bool)
    List(filter ToolFilter) []ToolDefinition
}
```

Runtime 每次构造 prompt 时，不再写死 action 列表，而是从 registry 取“当前位置允许的工具 catalog”。

### 4. Tool Policy

Tool Policy 决定当前节点能不能用某个工具：

```text
Position + NodeType + TaskStatus + SandboxMode + HumanApprovalPolicy
  -> allowed tools
  -> denied tools
  -> approval required tools
```

例子：

| 工具 | 默认权限 |
|---|---|
| `fs.read_file` | Leaf / Normal 可用，限制 sandbox |
| `fs.write_file` | Leaf 可用，Normal 需看位置策略 |
| `shell.exec` | 高风险，按命令和节点位置判断 |
| `memory.query` | 大多数节点可用 |
| `artifact.read` | 大多数节点可用 |
| `browser.open` | 需要网络权限 |
| `db.query` | 需要数据库连接权限 |
| `git.commit` | 需要人类审批 |

这一步非常重要：工具权限应该由 runtime 强制，而不是只写在 prompt 里。

### 5. Tool Observation

所有工具结果都应该统一成 observation：

```go
type ToolResult struct {
    ToolName     string
    CallID       string
    Status       string
    Summary      string
    ArtifactRefs []string
    Data         json.RawMessage
    Error        *ToolError
    StartedAt    int64
    FinishedAt   int64
    Provenance   Provenance
}
```

好处：

- prompt 可以稳定引用工具结果。
- visualizer 可以展示工具调用轨迹。
- memory 可以索引工具 observation。
- retry 可以根据结构化错误做修复。
- artifact refs 和 provenance 不会丢。

---

## 工具命名建议

从现在的平铺 action name，演进为命名空间工具名：

```text
fs.read_file
fs.write_file
fs.append_file
fs.list_dir
fs.search

shell.exec

artifact.read
artifact.add
artifact.pin
artifact.search

memory.query
memory.upsert_note

task.create_child
task.append_sibling
task.mark_complete
task.request_human_input

test.go
test.npm
test.command

git.diff
git.status
git.apply_patch

browser.fetch
browser.open
browser.screenshot

db.query
db.schema
```

这样可以避免 action 列表越来越混乱，也方便按 namespace 做权限控制。

---

## 第一批应补齐的工具

### 文件与代码工具

当前已有基础文件工具，但还缺少 coding agent 常用工具：

- `fs.stat`：查看文件大小、mtime、是否目录。
- `fs.read_range`：按行范围读取普通文件，区别于 artifact slice。
- `fs.tree`：受限深度目录树。
- `fs.glob`：按模式列文件。
- `code.symbols`：列出文件内函数、类型、方法。
- `code.references`：基于 grep 或 LSP 查引用。
- `code.patch`：应用 unified diff。
- `code.format`：按语言运行 formatter。

### Git 工具

建议加入：

- `git.status`
- `git.diff`
- `git.show`
- `git.log`
- `git.branch`

高风险操作默认需要人类审批：

- `git.commit`
- `git.push`
- `git.reset`
- `git.clean`
- `git.checkout`

### 测试工具

测试是 coding agent 的核心反馈来源：

- `test.go`：运行 `go test`，解析 package、失败测试、错误位置。
- `test.npm`：运行 npm scripts。
- `test.command`：通用测试命令，但需要 policy 约束。
- `test.parse_output`：把测试输出转成结构化 failure artifact。

测试工具的输出应该包含：

```json
{
  "passed": false,
  "command": "go test ./pkg/llm -v",
  "failed_tests": ["TestParseResponse"],
  "errors": [
    {
      "file": "pkg/llm/parser_test.go",
      "line": 47,
      "message": "expected error for broken json"
    }
  ],
  "artifact_ref": "art_12"
}
```

### Artifact 工具

Artifact 应该成为一等工具族：

- `artifact.add`：模型显式提交一个细粒度 artifact。
- `artifact.read`：读取 artifact slice。
- `artifact.pin`：固定重要 artifact。
- `artifact.link`：建立 artifact 之间或 artifact 与 node 的关系。
- `artifact.summarize`：对大 artifact 生成任务相关摘要。
- `artifact.resolve`：针对当前 context need 从大 artifact 中提取证据片段。

### Memory / Retrieval 工具

当前 `query_memory` 是好的起点，但需要扩展：

- `memory.query_ancestors`
- `memory.query_siblings`
- `memory.query_artifacts`
- `memory.search_artifacts`
- `memory.query_variables`
- `memory.query_failures`
- `memory.query_decisions`
- `memory.query_open_questions`

不建议开放任意 SQL。应继续使用受限 query type。

### 浏览器与网络工具

如果 LLMVM 要做真实工程任务，需要网络资料获取能力：

- `web.fetch`：抓取 URL 文本。
- `web.search`：搜索入口。
- `browser.open`：打开网页。
- `browser.screenshot`：截图。
- `browser.extract`：提取页面文本或 DOM。

这类工具必须有网络权限策略，避免默认开放。

### 数据库工具

对后端任务有价值：

- `db.connect`
- `db.schema`
- `db.query`
- `db.explain`
- `db.migrate_preview`

数据库工具默认只读，写操作和 migration 必须审批。

### 文档与办公工具

后续可以作为 plugin：

- `docx.read`
- `docx.write`
- `pptx.read`
- `pptx.write`
- `pdf.extract`
- `image.generate`
- `image.inspect`

这些工具适合放在插件层，而不是写死进 runtime。

---

## 插件系统设计

### 插件目录结构

建议本地插件结构：

```text
plugins/
  fs/
    plugin.json
    tools/
      read_file.go
      write_file.go
  git/
    plugin.json
    tools/
      status.go
      diff.go
  browser/
    plugin.json
    tools/
      fetch.go
```

`plugin.json`：

```json
{
  "name": "llmvm-git-tools",
  "version": "0.1.0",
  "description": "Git inspection and controlled mutation tools.",
  "tools": [
    "git.status",
    "git.diff",
    "git.log",
    "git.commit"
  ],
  "permissions": [
    "workspace.read",
    "workspace.write",
    "git.read",
    "git.write"
  ]
}
```

### 插件加载方式

第一阶段不要过早引入动态 Go plugin，因为 Go plugin 在跨平台和构建上很麻烦。

推荐演进路线：

1. **内置插件包**
   - 仍然编译进 runtime。
   - 但通过统一 `RegisterTools(registry)` 注册。
   - 先把硬编码 dispatch 拆掉。

2. **外部进程插件**
   - 插件是独立可执行文件。
   - 通过 JSON-RPC、stdio 或 HTTP 通信。
   - runtime 负责启动、超时、取消和权限。

3. **MCP 兼容层**
   - 允许 LLMVM 接入 MCP server。
   - 把 MCP tools 映射成 LLMVM ToolDefinition。
   - 保留 LLMVM 自己的 policy 和 artifact 写入。

4. **远程工具服务**
   - 通过受控 HTTP/gRPC 接入团队内部工具。
   - 适合数据库、部署、CI、文档系统。

---

## 与现有 Action 系统的迁移路径

不要一次性推翻现有 action。建议分阶段迁移：

### Phase 1：抽象 ToolDefinition 和 ToolRegistry

保留当前 JSON action：

```json
{"action_type": "read_file", "file_path": "..."}
```

但 runtime 内部把它转换成：

```json
{"tool": "fs.read_file", "input": {"path": "..."}}
```

这样外部协议先不变，内部 dispatch 先统一。

### Phase 2：引入通用 `tool_call`

新增 action：

```json
{
  "action_type": "tool_call",
  "tool": "fs.read_file",
  "input": {
    "path": "test/sandbox/main.go"
  }
}
```

旧 action 继续兼容，但 prompt 开始推荐 `tool_call`。

### Phase 3：Prompt 工具目录动态生成

Prompt 不再硬编码所有 action 说明，而是按当前位置注入：

```text
Available Tools:
- fs.read_file ...
- artifact.read ...
- memory.query ...
```

Tool catalog 由 registry + position policy 生成。

### Phase 4：废弃大部分旧 action

保留少数 runtime 原语：

- `task.create_child`
- `task.mark_complete`
- `task.request_human_input`

其他都走 tool plugin。

---

## Runtime 需要新增的核心结构

```go
type ToolCall struct {
    Tool  string          `json:"tool"`
    Input json.RawMessage `json:"input"`
}

type ToolError struct {
    Kind       string `json:"kind"`
    Message    string `json:"message"`
    Retryable  bool   `json:"retryable"`
    RepairHint string `json:"repair_hint,omitempty"`
}

type Permission string

const (
    PermissionWorkspaceRead  Permission = "workspace.read"
    PermissionWorkspaceWrite Permission = "workspace.write"
    PermissionShellExec      Permission = "shell.exec"
    PermissionNetwork        Permission = "network"
    PermissionDatabaseRead   Permission = "database.read"
    PermissionDatabaseWrite  Permission = "database.write"
    PermissionGitRead        Permission = "git.read"
    PermissionGitWrite       Permission = "git.write"
)
```

### SideEffectLevel

```go
type SideEffectLevel string

const (
    SideEffectNone        SideEffectLevel = "none"
    SideEffectWorkspace   SideEffectLevel = "workspace"
    SideEffectExternal    SideEffectLevel = "external"
    SideEffectDestructive SideEffectLevel = "destructive"
)
```

Side effect level 可以直接驱动 human approval：

- `none`：默认允许。
- `workspace`：按节点权限允许。
- `external`：通常需要审批或明确配置。
- `destructive`：默认需要人类确认。

---

## 工具结果如何进入 Artifact 和 Memory

每个工具可以声明结果处理策略：

```go
type ResultPersistencePolicy struct {
    StoreRawAsArtifact bool
    StoreSummaryOnly   bool
    IndexInFTS         bool
    AddToNodeVariables bool
    PinByDefault       bool
}
```

例子：

- `fs.read_file`：raw content 写 artifact，summary 进变量，FTS 可选。
- `shell.exec`：stdout/stderr 写 artifact，exit code 进 observation。
- `git.diff`：diff 写 artifact，summary 进 handoff 候选。
- `memory.query`：小结果可直接 observation，大结果写 artifact。
- `browser.screenshot`：图片 artifact，OCR 文本另建 text artifact。

这样工具结果不会随意塞进 prompt，而是统一进入 evidence pipeline。

---

## Visualizer 与调试能力

工具系统成熟后，visualizer 应展示 Tool Trace：

```text
Node
  -> Tool Calls
      - tool name
      - input
      - status
      - duration
      - permission decision
      - artifact refs
      - error kind
```

这能回答几个关键调试问题：

- 当前节点为什么看到了这个证据？
- 哪个工具调用失败了？
- 是模型参数错、权限拒绝、sandbox 拒绝，还是工具内部错误？
- 工具输出去了哪个 artifact？
- 这次执行是否有外部副作用？

---

## 与 Position Engineering 的关系

工具系统必须接入 position policy。

Position 不只决定上下文，还应该决定工具权限：

```text
Position
  -> Role
  -> Scope
  -> Authority
  -> Allowed Tools
  -> Approval Requirements
```

例子：

- Root 可以创建顶层任务，但不默认执行 shell。
- Normal 节点可以规划和查询 memory，但写文件权限有限。
- Leaf 节点可以读写 sandbox 文件、跑测试和创建 artifact。
- Error handler 可以读取失败上下文、查询 memory、请求人类输入。
- Loop controller 可以重置子节点状态，但不应该随意追加 sibling。

这比“所有节点看到同一套工具列表”稳定得多。

---

## 与 JSON 输出增强的关系

可插拔工具会让 action schema 更复杂，因此必须配合 JSON 输出增强：

- `tool_call` 使用严格 schema。
- 工具输入用 `json.RawMessage` 延迟到具体工具校验。
- parser 先校验 tool name 存在，再用工具 input schema 校验 input。
- 错误返回 typed parse error：
  - unknown tool
  - missing input field
  - invalid field type
  - permission denied
  - sandbox violation
  - tool timeout

下一轮 retry prompt 应该给模型精确修复提示，而不是泛泛地说 JSON 解析失败。

---

## 推荐实施阶段

### Phase 0：文档和接口草案

- 固化工具系统术语。
- 定义 ToolDefinition、ToolExecutor、ToolRegistry、ToolResult。
- 明确 permission 和 side effect taxonomy。

### Phase 1：内部 Registry 化

- 把现有 action executor 映射为内置 tools。
- 保留旧 action JSON。
- Runtime 内部统一走 `ExecuteTool`。
- 为每个现有工具补 input/output schema。

### Phase 2：通用 `tool_call`

- parser 支持 `tool_call`。
- prompt 注入 dynamic tool catalog。
- 旧 action 标记为兼容层。
- tests 覆盖 unknown tool、schema mismatch、permission denied。

### Phase 3：工具观察与可视化

- 增加 tool call event log。
- ToolResult 写入 artifact/memory。
- visualizer 展示每个节点的 tool trace。

### Phase 4：插件边界

- 内置插件按 package 注册。
- 增加 plugin manifest。
- 支持启用/禁用插件。
- 支持按 workspace 配置 allowed plugins。

### Phase 5：外部插件和 MCP

- 支持 stdio JSON-RPC 插件。
- 支持 MCP tools adapter。
- 所有外部工具仍走 LLMVM policy、artifact、memory 和 observation 格式。

---

## 第一版最小落地范围

第一版不要做太大。建议只做：

1. `ToolDefinition`
2. `ToolRegistry`
3. `ToolExecutor`
4. `ToolResult`
5. 把现有文件工具、artifact 工具、memory 工具注册进去
6. 新增 `tool_call`
7. prompt 动态列出 allowed tools
8. runtime policy 先实现简单 allow/deny

暂时不做：

- 外部进程插件
- MCP adapter
- 远程工具服务
- GUI marketplace
- 动态下载安装

先把内部硬编码 action 打散，形成稳定工具层，再扩展生态。

---

## 成功标准

工具系统改造完成后，应满足：

- 新增一个工具不需要修改 runtime 主循环。
- 工具 schema 能自动进入 prompt tool catalog。
- 工具输入由 runtime 按 schema 校验。
- 工具权限由 position policy 强制执行。
- 工具结果统一变成 observation。
- 大结果默认进入 artifact。
- 工具调用可在 visualizer 中追踪。
- 外部插件可以在不侵入 runtime 核心的情况下接入。
- 旧 action 有兼容路径，迁移期间不破坏现有任务。

---

## 总结

LLMVM 的核心优势是显式任务树、位置工程、artifact 证据和结构化记忆。工具系统应该沿着同一个方向演进：工具不是 prompt 里的字符串命令，而是 runtime 管理的能力单元。

把工具做成可插拔系统后，LLMVM 才能从“一个有固定工具的 agent runtime”升级为“一个可以承载不同任务域工具生态的 agent VM”。

