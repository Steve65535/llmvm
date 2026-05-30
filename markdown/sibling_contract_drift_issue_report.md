# Sibling Contract Drift 问题报告

> 日期：2026-05-29
> 背景：`test/sandbox/agent-notes-db` 大项目评测暴露出多个 sibling leaf 节点各自实现模块时，接口契约发生漂移，导致最终集成反复失败。

---

## 1. 问题定义

**Sibling contract drift** 指的是：

在 AST-first 任务树中，父节点把一个工程任务拆成多个 sibling leaf，例如：

```text
core_types
collection
embedding
persistence
cli
unit_tests
final_verify
```

每个 leaf 独立执行、独立持有局部上下文、独立写文件。由于缺少一个强制共享的接口契约，这些 sibling 会各自假设不同 API，最终形成模块之间的不兼容。

典型表现：

```text
CLI 期望 Store.AddNote(collection, note)
collection 实际实现 Collection.AddNote(id, title, content, tags)

tests 期望 Store.Collections 是导出字段
types 实际实现 Store.collections 是私有字段

persistence 期望 Load(path) 是包级函数
实现实际是 (*Store).Load(path)
```

这不是单个 leaf 写错代码，而是多个 leaf 对同一系统边界没有共享 contract。

---

## 2. 本次评测中的证据

任务：在 `test/sandbox/agent-notes-db` 内创建一个完整 Go 项目，包含 core types、collection、embedding/search、persistence、CLI、tests。

运行中出现过的典型错误：

```text
undefined: notesdb.Load
store.Collections undefined
store.ListNotes undefined
too many arguments in call to notesdb.DeterministicEmbedding
store.AddNote undefined
undefined: parseArgs
TestCreateCollection redeclared
not enough arguments in call to col.AddNote
```

这些错误共同指向一个问题：不同文件/节点对同一 API 的理解不一致。

最终状态中，多个核心 sibling 曾耗尽 retry：

```text
implement_core_types: Failed, IterationCount=20
implement_collection: Failed, IterationCount=20
implement_embedding: Failed, IterationCount=20
implement_cli: Failed, IterationCount=20
```

后续节点继续推进并部分修复项目，但 AST 中失败节点不会自动回填成功，父节点也没有在 sibling failed 后强制 replan。

---

## 3. 为什么 ArtifactGetter 不能解决这个问题

ArtifactGetter 解决的是上下文污染问题：

```text
large output -> artifact store
              -> isolated getter reads slices/searches
              -> focused summary enters main loop
```

它能让 agent 看见更精确的失败信息，但它不负责定义跨模块契约。

Sibling contract drift 属于 coordination / architecture control 问题，不是 retrieval 问题。即使每个 leaf 都能精准读取错误，多个 leaf 仍可能继续围绕不同 API 修补。

---

## 4. 根因分析

### 4.1 缺少 Project Contract Node

父节点创建 sibling 前没有先产出一个权威 API contract。

应该先有类似：

```go
type Store struct { ... }
func NewStore() *Store
func (s *Store) CreateCollection(name string) (*Collection, error)
func (s *Store) GetCollection(name string) (*Collection, error)
func (s *Store) Save(path string) error
func (s *Store) Load(path string) error

type Collection struct { ... }
func (c *Collection) AddNote(id, title, content string, tags []string) (Note, error)
func (c *Collection) SearchByText(query string, topK int) []SearchResult
```

然后所有 sibling 必须引用这个 contract。

### 4.2 Sibling handoff 不够强

当前 sibling handoff 更多是摘要，不是 machine-checkable contract。

例如 `core_types` 完成后，应该交接：

```json
{
  "exports": [
    "type Note struct {...}",
    "func NewStore() *Store",
    "func (s *Store) CreateCollection(name string) (*Collection, error)"
  ],
  "forbidden_assumptions": [
    "Store.Collections is exported",
    "Load is a package-level function"
  ]
}
```

但当前 handoff 没有这种 typed structure，下游只能从自然语言摘要里猜。

### 4.3 Failed dependency 没有阻断下游

当 `core_types` failed 后，`collection`、`persistence`、`cli` 仍继续推进。

对工程任务来说，基础接口节点失败后，下游默认应进入 Blocked 或触发 parent replan，而不是继续实现。

### 4.4 Final integration 节点权力不足

最后集成节点需要能跨模块重写 contract drift，而不只是跑测试。

如果 final node 看到：

```text
CLI expects package-level Load
persistence implemented method Load
tests expect parseArgs
main.go directly os.Exit
```

它应该有权限统一调整 API、CLI 和 tests。

### 4.5 Runtime 缺少 contract drift detector

当前 runtime 能看到 command failure，但不会归类：

```text
undefined method
wrong arity
unexported field
redeclared test
```

这些都应该被识别为 contract drift，而不是普通编译错误。

---

## 5. 影响

### 5.1 LLM 调用数暴涨

本次日志统计：

```text
LLM calls: 567
ArtifactGetter prompts: 206
avg input tokens: ~8.1k
p95 input tokens: ~16.6k
```

上下文没有失控，但因为 sibling 之间反复修 API，不必要的 turn 很多。

### 5.2 节点状态和实际文件状态脱节

一些节点早期 Failed，但后续节点可能部分修复了对应文件。AST 仍显示该 sibling failed。

这导致：

- 父节点看到失败状态，但项目文件可能已被修复。
- visualizer 展示的状态和磁盘状态不完全一致。
- memory 中 failed handoff 可能继续误导后续节点。

### 5.3 大项目成功率不稳定

小任务可以靠局部 ReAct 修好。大项目需要稳定接口。没有 contract 层时，成功依赖模型临场记忆和偶然收敛。

---

## 6. 修复建议

### Phase 1：Project Contract Node

父节点拆大项目前，先创建 contract leaf：

```text
project_contract
  - define package API
  - define file layout
  - define test expectations
  - define CLI command behavior
```

contract 产出必须进入 artifact，并 pin：

```json
{
  "artifact_type": "project_contract",
  "exports": [...],
  "cli_contract": {...},
  "test_contract": {...}
}
```

所有 implementation sibling 的 prompt 都必须包含 contract ref。

### Phase 2：Typed Handoff

扩展 mark_complete handoff：

```json
{
  "abstract": "Implemented collection CRUD",
  "typed_handoff": {
    "kind": "go_package_contract",
    "package": "notesdb",
    "exports": [
      "func (c *Collection) AddNote(id, title, content string, tags []string) (Note, error)"
    ],
    "files": ["notesdb/collection.go"],
    "tests": ["notesdb/collection_test.go"]
  }
}
```

自然语言 `handoff` 保留给人看，typed handoff 给 runtime 和下游节点使用。

### Phase 3：Failed Dependency Replan

Normal parent 调度规则改为：

```text
if required sibling failed:
  stop starting downstream siblings
  reactivate parent
  parent must choose:
    - retry failed child
    - create repair child
    - replace contract
    - explicitly continue despite failure
```

这能避免基础 API 节点失败后，CLI/tests 继续围绕错误假设开发。

### Phase 4：Contract Drift Detector

对 Go 编译错误做轻量分类：

```text
undefined: X
type T has no field or method M
too many arguments in call to F
not enough arguments in call to F
redeclared in this block
```

归一化成：

```text
contract_drift:missing_symbol:notesdb.Load
contract_drift:method_arity:DeterministicEmbedding
contract_drift:field_visibility:Store.Collections
contract_drift:duplicate_test:TestCreateCollection
```

连续出现 contract drift 时，prompt 应进入 contract repair mode，而不是继续局部修补。

### Phase 5：Final Integration Node

大项目必须自动追加 final integration node，职责：

```text
read project contract
run full test
inspect failures
modify any module necessary
update contract if changed
mark_complete only after full acceptance passes
```

final integration node 不应被限制在单个模块上下文里。

---

## 7. 验收标准

P0：

- 大项目父节点会先生成 project contract artifact。
- 所有 sibling prompt 都引用同一个 contract artifact。
- `undefined method / wrong arity / unexported field` 被归类为 contract drift。

P1：

- required sibling Failed 后，下游 sibling 默认不继续。
- parent 被重新激活并收到 failed dependency + contract drift summary。
- final integration node 能统一修复跨模块 API 不一致。

P2：

- visualizer 展示 contract artifact、typed handoff 和 contract drift timeline。
- 如果后续节点修复了 failed sibling 的文件，runtime 能标记该 sibling 为 superseded/repaired，而不是永久 Failed。

---

## 8. 一句话总结

ArtifactGetter 解决了“上下文太大看不清”的问题；Sibling contract drift 是“多个兄弟节点对同一工程接口各自理解”的问题。前者是 context isolation，后者是 project coordination。LLMVM 要稳定完成大项目，下一步必须引入 project contract、typed handoff、failed dependency replan 和 final integration。
