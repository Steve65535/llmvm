# Prompt 优化方案

## 当前 Prompt 分析

### Prompt 1: 注意力选择 Prompt（`selectAttentionNodes`）

**当前实现**（第 198-212 行）：
```
You are an Attention Selector for LLMVM.
Your task is to scan the current state of the task tree and identify nodes whose Variables or Results are useful for the current step.

## Task Tree Index (Compact)
[树结构]

## Current Target Node
- ID: xxx
- Name: xxx
- Goal: xxx

## Your Task
Identify which nodes from the Tree Index contain information (variables/results) that might be needed to solve the current goal. 
Return ONLY a comma-separated list of Node IDs. If none are useful, return "none".
Example: "node_1, node_5, create_dir"
```

### Prompt 2: 主执行 Prompt（`buildPromptInternal`）

**当前实现**（第 364-415 行）：
```
## Current Context
**Task Path**: root -> task1 -> subtask2
**Current Node**: [详细信息]
**Parent Node**: [父节点信息]
**Children Status**: [子节点状态]
**Loop Context**: [循环信息]

## Global Workspace (Ephemeral RAM)
[选中节点的信息]

## Structured Request Data
[JSON 格式]

## Scoped Variables (Current Path Context)
[变量信息]

## Request
[用户请求]

## Your Task
Please respond with valid JSON in the required format.

## Execution Requirements (STRICT):
1. Physical Persistence check: ...
2. Persistence: ...
3. Decomposition: ...
4. Tool Use: ...
```

---

## 优化点列表（供审核）

### 🔴 高优先级优化（强烈建议）

#### 优化 #1: 明确 Loop 控制指令

**问题**：
- 当前只告诉 LLM "Currently inside Loop node"
- 没有明确说明如何结束循环
- 没有提供循环变量信息

**建议优化**：
```markdown
**Loop Context**:
Currently inside Loop node: "验证偶数" (ID: loop_goldbach)
- All children finished: false
- **To end this loop**: Mark the child node as 'finished' when the loop condition is met
- **Loop variables**: Check the scoped variables below for loop counters (e.g., current_even, iteration_count)
- **Important**: If you want to continue iterating, do NOT mark children as finished
```

**预期效果**：
- LLM 更清楚如何控制循环
- 减少"忘记标记 finished"导致的无限循环
- 明确循环变量的位置

---

#### 优化 #2: 强化变量命名指导

**问题**：
- 没有指导 LLM 如何命名变量
- 可能导致嵌套 Loop 中的变量冲突

**建议优化**：
在 "Execution Requirements" 中添加：
```markdown
5. **Variable Naming**: 
   - Use descriptive names that include context (e.g., 'outer_loop_counter', 'file_processing_index')
   - Avoid generic names like 'i', 'temp', 'data' in nested structures
   - If you're in a Loop, prefix loop-specific variables with the loop's purpose (e.g., 'goldbach_current_even')
```

**预期效果**：
- 减少变量冲突
- 提高代码可读性
- 更容易调试

---

#### 优化 #3: 明确节点类型的使用场景

**问题**：
- 当前只说 "If the current node is a Leaf node, process it now"
- 没有明确说明何时使用 Normal vs Loop vs Leaf

**建议优化**：
```markdown
## Node Type Guidelines

**When to create each type**:
- **Normal**: For tasks that can be decomposed into sequential sub-tasks
  - Example: "Build web app" → [Setup, Frontend, Backend, Deploy]
  
- **Loop**: For tasks that need iteration until a condition is met
  - Example: "Verify all even numbers 4-1000" → Loop with condition check
  - **Critical**: You MUST mark child nodes as 'finished' when the loop should end
  
- **Leaf**: For atomic tasks that can be completed in one step
  - Example: "Read file.txt", "Calculate sum", "Print result"
  - Should NOT have child nodes

**Current node type**: {current.Type}
- If Normal and has no children yet: Consider decomposing into sub-tasks
- If Loop and children not finished: Continue iteration
- If Leaf: Execute the task directly using commands or mark_complete
```

**预期效果**：
- LLM 更准确地选择节点类型
- 减少不必要的分解或过度分解
- 更清晰的任务树结构

---

#### 优化 #4: 增强错误重试的上下文

**问题**：
- 当前只显示错误信息
- 没有告诉 LLM 这是第几次重试

**建议优化**：
```markdown
> [!IMPORTANT]
> **Previous Attempt Failed (Retry {retryCount}/{maxRetries})**:
> Error: {error}
> 
> **Common causes**:
> - Invalid JSON format (missing quotes, trailing commas)
> - Wrong action_type (must be: create_node, mark_complete, update_variables, execute_command, shutdown)
> - Missing required fields (e.g., node.id, node.name, node.type)
> 
> **Please**:
> 1. Fix the JSON syntax error
> 2. Verify all required fields are present
> 3. Double-check action_type spelling
```

**预期效果**：
- LLM 知道还有多少次重试机会
- 提供常见错误的提示
- 更快地修复错误

---

### 🟡 中优先级优化（建议考虑）

#### 优化 #5: 优化注意力选择 Prompt

**问题**：
- 当前只说 "identify nodes whose Variables or Results are useful"
- 没有给出选择标准

**建议优化**：
```markdown
## Your Task
Scan the Task Tree Index and select nodes that contain information relevant to the current goal.

**Selection criteria**:
- Nodes with variables that the current task might need (e.g., file paths, computed values)
- Nodes with results that provide context (e.g., "file exists", "data loaded")
- Recent nodes (higher index) are often more relevant
- Nodes in the same branch or parent chain

**Output format**: Comma-separated Node IDs (e.g., "node_1, node_5, create_dir")
- If no nodes are relevant: return "none"
- Limit to 5-10 most relevant nodes (avoid selecting too many)
```

**预期效果**：
- 更精准的节点选择
- 减少不相关节点的干扰
- 控制上下文长度

---

#### 优化 #6: 添加 JSON 格式示例

**问题**：
- 当前只说 "respond with valid JSON"
- 没有给出具体示例

**建议优化**：
```markdown
## Response Format

You MUST respond with a JSON object containing an "actions" array:

**Example 1: Create child nodes**
```json
{
  "actions": [
    {
      "action_type": "create_node",
      "node": {
        "id": "read_data",
        "name": "Read Data File",
        "type": "Leaf",
        "information": "Read data.csv and parse"
      }
    }
  ]
}
```

**Example 2: Mark current node complete**
```json
{
  "actions": [
    {
      "action_type": "mark_complete",
      "result": "Task completed successfully",
      "is_important": true
    }
  ]
}
```

**Example 3: Execute command**
```json
{
  "actions": [
    {
      "action_type": "execute_command",
      "command": "ls -la"
    }
  ]
}
```

**Critical**: 
- All string values must be in double quotes
- No trailing commas
- action_type must be exact (case-sensitive)
```

**预期效果**：
- 减少 JSON 格式错误
- 提供清晰的参考
- 加快 LLM 的响应速度

---

#### 优化 #7: 优化子节点状态显示

**问题**：
- 当前只显示 "Traveled: Yes/No, Finished: Yes/No"
- 没有说明这些状态的含义

**建议优化**：
```markdown
**Children Status**:
Total children: 3

1. [Leaf] Read File (ID: read_file) - Traveled: Yes, Finished: Yes
   → This child has been executed and completed
   
2. [Normal] Process Data (ID: process) - Traveled: Yes, Finished: No
   → This child has been visited but not fully completed (may have unfinished sub-tasks)
   
3. [Leaf] Save Result (ID: save) - Traveled: No, Finished: No
   → This child has not been executed yet

**Status meanings**:
- Traveled: Whether this node has been visited by the execution cursor
- Finished: Whether this node has completed its task (for Loop nodes, this controls iteration)

All children have been traveled: Yes
All children have been finished: No
```

**预期效果**：
- LLM 更清楚子节点的状态
- 更好地决定下一步行动
- 减少混淆

---

### 🟢 低优先级优化（可选）

#### 优化 #8: 添加任务进度提示

**问题**：
- LLM 不知道整体任务的进度

**建议优化**：
```markdown
**Task Progress**:
- Current depth: 3 (root → task1 → subtask2 → current)
- Total nodes created: 15
- Nodes completed: 8
- Estimated progress: ~53%
```

**预期效果**：
- LLM 有全局视角
- 可以估计剩余工作量

---

#### 优化 #9: 添加性能提示

**问题**：
- LLM 可能创建过多不必要的节点

**建议优化**：
```markdown
**Performance Tips**:
- Prefer Leaf nodes for simple tasks (avoid unnecessary decomposition)
- Use execute_command for file operations instead of creating multiple nodes
- Combine related operations in a single Leaf node when possible
```

**预期效果**：
- 减少不必要的节点
- 提高执行效率

---

#### 优化 #10: 优化 Global Workspace 的展示

**问题**：
- 当前只是简单列出选中节点的信息
- 没有突出重点

**建议优化**：
```markdown
## Global Workspace (Ephemeral RAM)

**Relevant information from previous nodes**:

📌 [IMPORTANT] Node #5: Create Directory
   Result: Directory '/data' created successfully
   Variables: {"dir_path": "/data"}

📄 Node #8: Read Config
   Result: Config loaded
   Variables: {"config": {"host": "localhost", "port": 8080}}

💡 **Key takeaways**:
- Working directory: /data
- Server config: localhost:8080
```

**预期效果**：
- 更易读的信息展示
- 突出重要信息
- 减少 LLM 的认知负担

---

## 优化优先级总结

| 优化点 | 优先级 | 预期收益 | 实施难度 | 建议 |
|--------|--------|---------|---------|------|
| #1 Loop 控制指令 | ⭐⭐⭐⭐⭐ | 高 | 低 | **强烈推荐** |
| #2 变量命名指导 | ⭐⭐⭐⭐⭐ | 高 | 低 | **强烈推荐** |
| #3 节点类型指导 | ⭐⭐⭐⭐⭐ | 高 | 低 | **强烈推荐** |
| #4 错误重试上下文 | ⭐⭐⭐⭐ | 中 | 低 | **推荐** |
| #5 注意力选择优化 | ⭐⭐⭐ | 中 | 低 | 建议 |
| #6 JSON 格式示例 | ⭐⭐⭐ | 中 | 低 | 建议 |
| #7 子节点状态说明 | ⭐⭐⭐ | 中 | 低 | 建议 |
| #8 任务进度提示 | ⭐⭐ | 低 | 中 | 可选 |
| #9 性能提示 | ⭐⭐ | 低 | 低 | 可选 |
| #10 Workspace 优化 | ⭐⭐ | 低 | 中 | 可选 |

---

## 建议的实施顺序

### 第一批（核心优化，立即实施）
1. **优化 #1**: Loop 控制指令
2. **优化 #2**: 变量命名指导
3. **优化 #3**: 节点类型指导

**预计工作量**：2-3 小时
**预期收益**：显著提升 Loop 控制准确性和变量管理

### 第二批（增强优化，建议实施）
4. **优化 #4**: 错误重试上下文
5. **优化 #6**: JSON 格式示例
6. **优化 #7**: 子节点状态说明

**预计工作量**：2-3 小时
**预期收益**：减少错误率，提高响应质量

### 第三批（可选优化，按需实施）
7. **优化 #5**: 注意力选择优化
8. **优化 #8-10**: 其他可选优化

**预计工作量**：1-2 小时
**预期收益**：边际改进

---

## 需要您审核的问题

1. **优先级是否合理**？
   - 您认为哪些优化最重要？
   - 是否有我遗漏的优化点？

2. **优化内容是否合适**？
   - Loop 控制指令的表述是否清晰？
   - 变量命名指导是否过于严格？
   - JSON 示例是否需要更多？

3. **实施策略**？
   - 是否同意分三批实施？
   - 还是一次性实施所有高优先级优化？
   - 是否需要先在测试中验证效果？

4. **其他考虑**？
   - Prompt 长度是否会过长？
   - 是否需要针对不同 LLM（GPT-4 vs DeepSeek）调整？
   - 是否需要添加中文版本的 Prompt？

请审核后告知您的决定，我会根据您的反馈执行优化！
