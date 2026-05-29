# TaskNode 数据模型

上级：[[../任务树与游标|任务树与游标]]

## `TaskNode`

- 源码：[pkg/tasknode/tasknode.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/tasknode/tasknode.go:62)
- 作用：表示任务树中的一个执行节点。
- 关键字段：
  - `ID`, `Name`, `Type`, `Status`: 节点身份和状态。
  - `Information`: 节点任务说明。
  - `Parent`, `Children`: 树结构；`Parent` 不进入 JSON。
  - `Variables`: 节点局部变量。
  - `KeyFacts`, `ArtifactRefs`, `Handoff`: 结构化交接。
  - `Goal`, `Summary`, `Decisions`, `Assumptions`, `Outputs`, `OpenQuestions`, `Confidence`: 扩展报告字段。
  - `HumanRequest`, `HumanResponse`: human-in-the-loop 暂停/恢复。
  - `AcceptanceCriteria`, `AcceptanceResults`: 节点验收协议。

## `NewTaskNode(id, name string, typ TaskType, info []string) *TaskNode`

- 源码：[pkg/tasknode/tasknode.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/tasknode/tasknode.go:102)
- 作用：创建 pending 状态节点，并初始化 children、variables、retry budget 等默认值。

## `AddChild(child *TaskNode)`

- 源码：[pkg/tasknode/tasknode.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/tasknode/tasknode.go:120)
- 作用：设置 child parent 并追加到 children。
- 副作用：更新父节点 `UpdatedAt`。

## `IsTerminal() bool`

- 源码：[pkg/tasknode/tasknode.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/tasknode/tasknode.go:161)
- 作用：判断节点是否已经终止。
- 当前语义：`WetherFinished || Status == Failed`。

## `RestoreParents(node *TaskNode)`

- 源码：[pkg/tasknode/tasknode.go](/Users/steve/Desktop/llmvm-rag-exp/pkg/tasknode/tasknode.go:261)
- 作用：JSON load 后恢复父指针。
- 重要性：保存状态中不序列化 `Parent`，所以恢复任务树必须重建 parent links。
