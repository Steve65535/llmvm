# LLMVM 超级任务表现不优深度分析 (v1.0) - with Code-Level Analysis

## 1. 核心瓶颈：选择熵增 (O(N) Selection Entropy)

### 问题描述
LLMVM 宣称其执行上下文复杂度为 $O(1)$，但这仅指“执行阶段”。在“选择阶段”（`selectAttentionNodes`），系统会将**整棵树的索引**发送给 LLM。

### 表现
- **索引爆炸**: 当任务达到 500+ 节点时，树索引本身可能达到 10k-20k tokens。
- **中间失落 (Lost in the Middle)**: 随着索引变长，LLM 准确识别深层或早期关键节点的能力下降。
- **无效开销**: 每次执行一个叶子节点前都要进行一次全量扫描，对于长路径任务，这种“元认知”开销极大且容易引入噪声。

---

## 2. 状态抹杀与变量污染 (State Smearing & Variable Pollution)

### 问题描述
目前的 `CollectScopedVariables` 采用简单的 `Update` 覆盖逻辑，且缺乏严格的生命周期管理。

### 表现
- **变量冲突**: 在超级任务中，不同分支的节点可能使用相同的变量名（如 `tmp_file`, `result`），子节点会覆盖父节点或其他分支的导出变量。
- **冗余堆积**: `command_output_history` 等大变量虽然在 Leaf 节点会被清理，但在 `Loop` 或 `Normal` 的路径传播中，如果没有显式清理，会导致 Prompt 越来越重。
- **状态不连贯**: 缺乏显式的“状态自愈”能力。如果一个节点计算错误并保存了错误的变量，后续所有节点将基于错误事实进行推理。

---

## 3. 元认知过载 (Metacognitive Overload)

### 问题描述
LLM 在 LLMVM 中身兼数职：**JIT 编译器**（生成 AST）、**逻辑解题器**、**VM 管理员**（标记重要性、处理变量）。

### 表现
- **专注力流失**: 在处理逻辑极度复杂的业务（如编写复杂的编译器代码）时，LLM 很难同时保证输出的 JSON 格式正确、节点 ID 不重复、变量注入精准。
- **结构化疲劳**: 随着执行步数增加，LLM 往往会放弃复杂的 AST 构建，转而创建大量的“扁平”Leaf 节点，退化成普通的线性 Agent。

---

## 4. DFS 遍历的刚性 (DFS Rigidity)

### 问题描述
深度优先搜索是一种“一条路走到黑”的算法。

### 表现
- **缺乏全局修正**: 超级任务需要根据中间发现随时调整全局战略。目前的架构没有提供“战略回撤（Backtrack）”的机制。如果 LLM 在深度 10 发现之前的策略错了，它很难删除已有的节点重新建树，只能在错误的分支上不断重试（Agentic Loop 的副作用）。
- **递归陷阱**: 对于变动极大的环境，静态生成的 AST 会迅速过时。

---

## 5. 改进逻辑架构建议 (LLMVM vNext)

### 5.1 从 O(N) 到 O(log N) 的索引
引入子树摘要（Sub-tree Summary）机制。当子树完成时，将其压缩为一个单一的 `Summary` 变量，而不是在选择阶段展开所有节点。

### 5.2 变量作用域隔离
引入真正的 `Call Stack`。区分 `Local`, `Scoped`, 和 `Global` 变量。

### 5.3 协同演进：编译器与解题器分离
考虑使用两组 Prompt 或两个不同的模型调用：一个负责维护任务树的结构（OS/Manager），一个专注于具体节点的指令执行（ALU/Worker）。

### 5.4 战略重排接口
增加一种 Action 类型：`replan_node`，允许 LLM 在发现错误时直接重构其叔叔节点或父节点的子树。

---

## 6. 具体的代码层缺陷 (Code-Level Defects - v1.1 Added)

### 6.1 Agentic Loop 的盲目重试
- **File**: `pkg/runtime/agentic_loop.go`
- **Issue**: `HandleLeafAgenticLoop` 仅依赖 `IterationCount < MaxRetries` 来决定是否继续。
- **Impact**: LLM 即使连续 5 次犯同样的错误（如把 `vfs.Write` 写成 `os.Write`），系统也会机械地让它重试，直到 Token 耗尽或次数用完。
- **Fix**: 引入 `Error History Analysis`。如果连续 2 次错误相似度 > 90%，强制暂停或请求父节点干预。

### 6.2 上下文历史的无限膨胀
- **File**: `pkg/runtime/runtime.go` (`ExecuteAction`)
- **Issue**: `command_output_history` 变量会将被截断的命令输出（即使是 1000 字符）不断追加到列表中。
- **Impact**: 在长 Loop 中（例如 20 次尝试），Context 会被 20 条几乎一模一样的报错信息填满，导致 "Lost in the Middle" 效应加剧，LLM 忘记最初的 Task Goal。
- **Fix**: 实现 `Log Compression`。只保留最近 2 次的完整输出，之前的迭代只保留 "Hash/Summary" 或 "Error Category"。
