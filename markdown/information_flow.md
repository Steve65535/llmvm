# LLMVM 信息流架构分析 (Information Flow Architecture)

LLMVM 的核心挑战在于：如何在保证图灵完备性和长程任务执行深度的同时，**死守大模型的最佳上下文窗口界限**，避免传统 Chain of Thought (CoT) 导致的“上下文爆炸”和“严重幻觉”。

为了解决这个难题，系统在信息流（Information Flow）的传递上，采用了区别于传统平面对话的**立体路由机制**。

## 一、信息的立体路由机制 (The 3D Routing)

LLMVM 的信息传递并非传统的“一维长对话”，而是一种基于**树形结构的垂直聚合（Vertical Aggregation）**、**全局扫描的水平旁路（Horizontal Bypass）**，以及**物理隔离的宿主挂载**。

### 1. 垂直向上传递 (状态冒泡 Bubbling)

当叶子节点（Leaf Node）在一个循环或控制分支中执行成功后，它产生的核心数据不能只停留在本地，必须向历史沉淀。

*   **机制**：`runtime.go -> decideNextStep()`
*   **现象**：节点如果处于 `Loop` 的子节点列中，它会将其核心的 `Variables`（如生成的代码片段、找到的素数集合）**向上传递给父节点**，从而实现跨迭代周期的状态累积，这构成了 Agentic Loop 能够收敛和感知整体进度的关键。

### 2. 水平跨节点传递 (Global Attention 语义索引)

当处于执行树极深处（如第四层代码生成节点）需要访问极浅处（如第一层词法定义节点）的数据契约时，传统的按部就班追溯会污染整条路上的上下文。

*   **机制**：`runtime.go -> collectGlobalAttention()`
*   **现象**：系统通过 DFS 全树扫描，自动剥离并提取出所有被标记为 `IsImportant = true` 或是带有明确总结提取 `Result` 的节点。
*   这就好比在庞大的企业中跳过了层层汇报，直接将关键部门的“核心通告（Variables + Result）”推送到大总线（Prompt/Context）中，供当前节点查阅。

### 3. 宿主物理系统旁路传递 (The VFS / Shell Bypass)

这是最硬核、也是真正打破 Token 限制的信息流。

*   **机制**：`pkg/vfs` 与 `sh -c` (Command Execution)
*   **现象**：并不是所有数据都要送进大脑（LLM）。节点 A 可以将几十 KB 的爬虫数据写入 `data.json`，节点 B 通过执行 `jq` 在 Shell 层面直接过滤读取。这招完全绕过了语言模型的 Context 限制，实现了 $O(N)$ 级庞大数据的“暗箱传递”。

---

## 二、信息的消亡与物理隔绝 (Information Blackholes)

作为一个高度自律的“受限上下文”运行时，LLMVM 为了防止上下文被垃圾数据污染，主动设置了严酷的**“信息隔离与清洗阀门”**。这也是为什么某些信息会“无法传递”。

### ❌ 1. “刮刀机制”导致的临时变量物理切断

这是最容易被忽视，但最保证系统纯洁性的机制。

*   **代码溯源**：`agentic_loop.go -> clearLeafScratchpad()`
*   **断流场景**：当一个叶子节点在执行 Shell 探索时（如执行 `ls`, `cat`, 甚至是报错的 Stack Trace），这些冗长的输出会挂载在临时变量 `command_output_history` 和 `last_command_result` 中。一旦该节点被标记为完成（`SingleFinished`）并准备退出活动栈，运行时会像“刮刀”一样，**强行 `delete()` 这些中间输出过程**。
*   **后果**：系统坚决不让试错日志泄漏到主总线。如果在后续节点中，模型突然需要引用刚才 `cat` 出来的某行细节，对不起，信息流已经断裂，必须重新执行查询。

### ❌ 2. 非重点节点的平行信息壁垒 (Unmarked Nodes)

兄弟节点之间默认是**平行隔绝**的，除非满足特定提权条件。

*   **代码溯源**：`runtime.go -> collectGlobalAttention()` 的拦截逻辑判断 `if node.IsImportant || node.Result != ""`
*   **断流场景**：假设节点 A 费尽心思推演出了一个复杂的正则表达式，但它既没有设置自己为 `IsImportant`，也没有将正则凝练到 `Result` 摘要中，仅仅保存在了自身的 `Variables` 字典。那么随后执行的平行节点 B，将**完全无法感知**存在这个正则。A 的输出被彻底封死在了自己的孤岛作用域中，形成了信息的“黑洞壁垒”。

## 总结 (Philosophy)

LLMVM 的信息流设计哲学完美兼容了软件工程的奥卡姆剃刀原理：

> **“除非主动声明重要（IsImportant/Result）或向父级上抛（Parent Variables），否则出了作用域，剩下的推理中间过程全部物理抹除。”**
>
> **“让文件系统（VFS）扛重活，让运行时上下文（Runtime Prompt）只传指针和轻量契约。”**
