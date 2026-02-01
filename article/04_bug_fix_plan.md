# LLMVM 核心 Bug 修复计划

## 前言

根据架构分析，以下是需要修复的 **3 个核心 Bug**：

1. **LoopStack 管理时机错误**
2. **变量作用域缺少隔离**
3. **缺少错误处理机制**

**注意**：Loop 终止条件不需要修改，因为图灵机不需要保证停机（停机问题不可判定）。

---

## Bug #1: LoopStack 管理时机错误

### 问题描述

**当前实现**（`cursor.go`）：
```go
// MoveDown: 在向下移动到子节点时 push
func (c *Cursor) MoveDown() bool {
    nextChild := c.Current.GetNextUntraveledChild()
    if nextChild != nil {
        if c.Current.Type == tasknode.Loop {
            c.LoopStack = append(c.LoopStack, c.Current)  // ❌ Push 时机错误
        }
        c.Current = nextChild
        return true
    }
    return false
}

// MoveUp: 在向上移动时 pop
func (c *Cursor) MoveUp() bool {
    if c.Current.Type == tasknode.Loop {
        c.PopLoop(c.Current)  // ❌ Pop 时机不对称
    }
    c.Current = c.Current.Parent
    return true
}
```

**问题分析**：
1. **Push 时机**：在 `MoveDown` 时 push，但此时 Loop 节点还没有被处理
   - 如果 Loop 节点的首次 LLM 调用失败，Loop 已经在栈中了
   - 导致栈状态不一致

2. **Pop 时机**：在 `MoveUp` 时 pop
   - 但 push 是在进入子节点时（`MoveDown`）
   - 时机不对称，容易出错

3. **嵌套 Loop 问题**：
   ```
   Loop A
   └─ Loop B
      └─ Leaf
   ```
   - 进入 Leaf 时：LoopStack = [A, B]
   - 离开 Leaf 时：LoopStack = [A, B]（Leaf 不是 Loop，不 pop）
   - 离开 Loop B 时：LoopStack = [A]（正确）
   - 但如果 Loop B 重置子节点，再次进入 Leaf 时会再次 push B → LoopStack = [A, B, B] ❌

### 根本原因

**Push 和 Pop 的语义不一致**：
- Push：在"进入 Loop 的子节点"时
- Pop：在"离开 Loop 节点本身"时

### 修复方案

#### 方案 A：在 Runtime 中管理（推荐）

**核心思想**：在 Loop 节点首次被处理时 push，在 Loop 完成时 pop

**修改文件**：`pkg/runtime/runtime.go`

```go
// Execute 主循环中
func (r *Runtime) Execute(initialRequest string) error {
    for !r.cursor.Done() {
        current := r.cursor.Current
        
        // 🔧 FIX: 在 Loop 节点首次处理时 push
        if current.Type == tasknode.Loop && !current.WetherTraveled {
            r.cursor.PushLoop(current)
        }
        
        // ... 现有的 LLM 调用逻辑 ...
        
        // 决定下一步
        if err := r.decideNextStep(current); err != nil {
            return err
        }
    }
    return nil
}

// decideNextStep 中
func (r *Runtime) decideNextStep(current *tasknode.TaskNode) error {
    if current.Type == tasknode.Loop {
        if allFinished {
            // 🔧 FIX: Loop 完成时 pop
            r.cursor.PopLoop(current)
            current.MarkFinished()
            r.cursor.MoveUp()
            return nil
        }
        // Loop 未完成，重置子节点
        current.ResetChildrenStatus()
        return nil
    }
    // ... 其他逻辑 ...
}
```

**修改文件**：`pkg/cursor/cursor.go`

```go
// 🔧 FIX: 移除 MoveDown 中的 push 逻辑
func (c *Cursor) MoveDown() bool {
    if c.Current == nil {
        return false
    }
    nextChild := c.Current.GetNextUntraveledChild()
    if nextChild != nil {
        // ❌ 删除这段代码
        // if c.Current.Type == tasknode.Loop {
        //     c.LoopStack = append(c.LoopStack, c.Current)
        // }
        c.Current = nextChild
        return true
    }
    return false
}

// 🔧 FIX: 移除 MoveUp 中的 pop 逻辑
func (c *Cursor) MoveUp() bool {
    if c.Current == nil || c.Current.Parent == nil {
        c.Current = nil
        return false
    }
    // ❌ 删除这段代码
    // if c.Current.Type == tasknode.Loop {
    //     c.PopLoop(c.Current)
    // }
    c.Current = c.Current.Parent
    return true
}

// 🆕 新增：显式的 Push 方法
func (c *Cursor) PushLoop(node *tasknode.TaskNode) {
    if node.Type == tasknode.Loop {
        c.LoopStack = append(c.LoopStack, node)
    }
}

// ✅ 保留：PopLoop 方法（已存在）
func (c *Cursor) PopLoop(node *tasknode.TaskNode) {
    if len(c.LoopStack) > 0 && c.LoopStack[len(c.LoopStack)-1] == node {
        c.LoopStack = c.LoopStack[:len(c.LoopStack)-1]
    }
}
```

#### 方案 B：完全在 Cursor 中管理（备选）

**核心思想**：Cursor 自己维护栈的一致性

```go
func (c *Cursor) MoveDown() bool {
    // 记录当前节点
    parent := c.Current
    
    nextChild := c.Current.GetNextUntraveledChild()
    if nextChild != nil {
        c.Current = nextChild
        
        // 如果刚刚进入的是 Loop 的第一个子节点，且 Loop 未在栈中
        if parent.Type == tasknode.Loop && !c.isInStack(parent) {
            c.LoopStack = append(c.LoopStack, parent)
        }
        return true
    }
    return false
}

func (c *Cursor) isInStack(node *tasknode.TaskNode) bool {
    for _, n := range c.LoopStack {
        if n == node {
            return true
        }
    }
    return false
}
```

**问题**：这个方案更复杂，且 Cursor 需要知道 Loop 的语义。

### 推荐方案

**方案 A**（在 Runtime 中管理），原因：
1. ✅ 语义清晰：Loop 首次处理时 push，完成时 pop
2. ✅ 时机对称：都在 Runtime 的控制流中
3. ✅ Cursor 保持简单：只负责移动，不关心 Loop 语义
4. ✅ 易于测试：可以单独测试 LoopStack 的正确性

### 测试用例

```go
func TestLoopStackManagement(t *testing.T) {
    // 测试 1：单层 Loop
    root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, nil)
    loop1 := tasknode.NewTaskNode("loop1", "Loop1", tasknode.Loop, nil)
    leaf1 := tasknode.NewTaskNode("leaf1", "Leaf1", tasknode.Leaf, nil)
    root.AddChild(loop1)
    loop1.AddChild(leaf1)
    
    engine := &MockEngine{/* ... */}
    runtime := NewRuntime(engine, root)
    
    // 执行前：LoopStack = []
    assert.Equal(t, 0, len(runtime.cursor.LoopStack))
    
    // 进入 Loop1 后：LoopStack = [Loop1]
    runtime.Execute("test")
    // ... 验证 LoopStack 状态 ...
    
    // 测试 2：嵌套 Loop
    // 测试 3：Loop 重置后的栈状态
}
```

---

## Bug #2: 变量作用域缺少隔离

### 问题描述

**当前实现**（`runtime.go`）：
```go
func (r *Runtime) collectScopedVariables(current *tasknode.TaskNode) map[string]interface{} {
    vars := make(map[string]interface{})
    path := []*tasknode.TaskNode{}
    node := current
    for node != nil {
        path = append([]*tasknode.TaskNode{node}, path...)
        node = node.Parent
    }
    
    // 从根到当前，后面的覆盖前面的
    for _, n := range path {
        for k, v := range n.Variables {
            vars[k] = v  // ❌ 所有变量都是全局可见的
        }
    }
    return vars
}
```

**问题场景**：
```
Loop: "外层循环" (i = 0)
├─ Leaf: "设置 i = 0"
├─ Loop: "内层循环"
│  ├─ Leaf: "设置 i = 0"  ← 覆盖了外层的 i！
│  └─ Leaf: "i += 1"
└─ Leaf: "使用 i"  ← 期望是外层的 i，但已被内层修改
```

### 根本原因

**所有节点的变量都在同一个命名空间**，没有作用域隔离。

### 修复方案

#### 方案 A：局部变量 + 导出变量（推荐）

**核心思想**：
- 每个节点有两个变量空间：
  - `LocalVariables`：局部变量（只在当前节点可见）
  - `ExportedVars`：导出变量（子节点可见）

**修改文件**：`pkg/tasknode/tasknode.go`

```go
type TaskNode struct {
    ID             string
    Name           string
    // ... 其他字段 ...
    
    // 🔧 FIX: 拆分变量空间
    LocalVariables map[string]interface{} // 局部变量（不会传递给子节点）
    ExportedVars   map[string]interface{} // 导出变量（子节点可见）
    
    // ❌ 删除原有的 Variables 字段
    // Variables      map[string]interface{}
}

func NewTaskNode(id, name string, typ TaskType, info []string) *TaskNode {
    return &TaskNode{
        // ... 其他字段 ...
        LocalVariables: make(map[string]interface{}),
        ExportedVars:   make(map[string]interface{}),
    }
}
```

**修改文件**：`pkg/runtime/runtime.go`

```go
// 🔧 FIX: 变量查找规则
func (r *Runtime) collectScopedVariables(current *tasknode.TaskNode) map[string]interface{} {
    vars := make(map[string]interface{})
    
    // 1. 从根到当前，收集所有 ExportedVars
    path := []*tasknode.TaskNode{}
    node := current
    for node != nil {
        path = append([]*tasknode.TaskNode{node}, path...)
        node = node.Parent
    }
    
    for _, n := range path {
        for k, v := range n.ExportedVars {
            vars[k] = v  // 父节点的导出变量
        }
    }
    
    // 2. 最后加入当前节点的 LocalVariables（优先级最高）
    for k, v := range current.LocalVariables {
        vars[k] = v
    }
    
    return vars
}

// 🔧 FIX: executeAction 中的变量更新
func (r *Runtime) executeAction(action llm.Action, parent *tasknode.TaskNode) error {
    switch action.ActionType {
    case "update_variables":
        if action.Variables != nil {
            // LLM 可以指定变量的作用域
            for k, v := range action.Variables {
                // 默认更新为导出变量（子节点可见）
                if parent.ExportedVars == nil {
                    parent.ExportedVars = make(map[string]interface{})
                }
                parent.ExportedVars[k] = v
            }
        }
        return nil
    
    case "execute_command":
        // 命令结果默认保存为局部变量
        if parent.LocalVariables == nil {
            parent.LocalVariables = make(map[string]interface{})
        }
        parent.LocalVariables["last_command_result"] = result
        
        // 命令历史保存为导出变量（子节点可能需要）
        if parent.ExportedVars == nil {
            parent.ExportedVars = make(map[string]interface{})
        }
        parent.ExportedVars["command_output_history"] = history
        return nil
    }
}
```

**LLM Prompt 指导**：
```markdown
## Variable Scopes

You can manage two types of variables:

1. **Local Variables** (default for command results):
   - Only visible in the current node
   - Example: temporary computation results

2. **Exported Variables** (default for update_variables):
   - Visible to all child nodes
   - Example: loop counters, shared state

Example:
{
  "action_type": "update_variables",
  "variables": {
    "loop_counter": 5,     // Exported (children can see)
    "total_sum": 100       // Exported (children can see)
  }
}
```

#### 方案 B：变量前缀标记（备选）

**核心思想**：使用前缀区分作用域

```go
// 局部变量：以 "local_" 开头
parent.Variables["local_temp"] = value

// 导出变量：以 "export_" 开头
parent.Variables["export_counter"] = value

// 全局变量：以 "global_" 开头
parent.Variables["global_config"] = value
```

**问题**：
- ❌ 依赖命名约定（不可靠）
- ❌ LLM 可能忘记加前缀
- ❌ 不够优雅

#### 方案 C：显式的作用域声明（备选）

**核心思想**：在创建节点时声明变量作用域

```go
type TaskNode struct {
    Variables      map[string]interface{}
    VariableScopes map[string]string  // "local" | "exported" | "global"
}
```

**问题**：
- ❌ 需要维护两个 map 的一致性
- ❌ 更复杂

### 推荐方案

**方案 A**（局部变量 + 导出变量），原因：
1. ✅ 语义清晰：明确区分局部和导出
2. ✅ 类型安全：编译时检查
3. ✅ 灵活性：LLM 可以选择变量的可见性
4. ✅ 符合编程语言惯例：类似于 Python 的 `local` vs `nonlocal`

### 向后兼容性

**问题**：现有代码使用 `Variables` 字段

**解决方案**：
```go
// 添加兼容性方法
func (t *TaskNode) GetVariable(key string) (interface{}, bool) {
    // 先查 LocalVariables
    if v, ok := t.LocalVariables[key]; ok {
        return v, true
    }
    // 再查 ExportedVars
    if v, ok := t.ExportedVars[key]; ok {
        return v, true
    }
    return nil, false
}

func (t *TaskNode) SetVariable(key string, value interface{}) {
    // 默认设置为导出变量（保持向后兼容）
    if t.ExportedVars == nil {
        t.ExportedVars = make(map[string]interface{})
    }
    t.ExportedVars[key] = value
}
```

### 测试用例

```go
func TestVariableScoping(t *testing.T) {
    // 测试 1：局部变量不会泄露
    parent := tasknode.NewTaskNode("parent", "Parent", tasknode.Normal, nil)
    child := tasknode.NewTaskNode("child", "Child", tasknode.Leaf, nil)
    parent.AddChild(child)
    
    parent.LocalVariables["temp"] = "parent_value"
    
    // 子节点不应该看到父节点的局部变量
    vars := runtime.collectScopedVariables(child)
    _, exists := vars["temp"]
    assert.False(t, exists)
    
    // 测试 2：导出变量可以被子节点访问
    parent.ExportedVars["counter"] = 10
    vars = runtime.collectScopedVariables(child)
    assert.Equal(t, 10, vars["counter"])
    
    // 测试 3：嵌套 Loop 的变量隔离
    // ...
}
```

---

## Bug #3: 缺少错误处理机制

### 问题描述

**当前实现**（`runtime.go`）：
```go
for _, action := range response.Actions {
    if err := r.executeAction(action, current); err != nil {
        if strings.Contains(err.Error(), "EMERGENCY_SHUTDOWN") {
            return err  // 立即退出
        }
        lastErr = err
        actionErr = true
        break  // 触发重试
    }
}

if actionErr {
    retryCount++
    continue  // 重试（最多 9 次）
}
```

**问题**：
1. **只有重试**：错误只会触发重试，没有其他恢复机制
2. **无法向上传播**：错误无法传递给父节点处理
3. **缺少 try-catch 语义**：无法实现"尝试 A，失败则执行 B"

**问题场景**：
```
Normal: "数据处理流水线"
├─ Normal: "读取数据"
│  ├─ Leaf: "打开文件"  ← 可能失败（文件不存在）
│  └─ Leaf: "解析 CSV"
├─ Normal: "错误处理"  ← 应该在上一步失败时执行
│  └─ Leaf: "使用默认数据"
└─ Normal: "继续处理"
```

**当前行为**：
- "打开文件"失败 → 重试 9 次 → 整个系统退出 ❌
- "错误处理"分支永远不会被执行

### 根本原因

**缺少错误传播和恢复机制**。

### 修复方案

#### 方案 A：ErrorHandler 节点（推荐）

**核心思想**：每个节点可以指定一个错误处理节点

**修改文件**：`pkg/tasknode/tasknode.go`

```go
type TaskNode struct {
    ID             string
    Name           string
    // ... 其他字段 ...
    
    // 🆕 新增：错误处理节点
    ErrorHandler   *TaskNode  // 当前节点失败时执行的节点
    MaxRetries     int        // 最大重试次数（默认 9）
    RetryCount     int        // 当前重试次数
}

func NewTaskNode(id, name string, typ TaskType, info []string) *TaskNode {
    return &TaskNode{
        // ... 其他字段 ...
        ErrorHandler: nil,
        MaxRetries:   9,
        RetryCount:   0,
    }
}
```

**修改文件**：`pkg/llm/parser.go`

```go
type NodeDTO struct {
    ID             string                 `json:"id"`
    Name           string                 `json:"name"`
    Type           string                 `json:"type"`
    Information    string                 `json:"information"`
    
    // 🆕 新增：错误处理节点
    ErrorHandlerID string                 `json:"error_handler_id,omitempty"`
    MaxRetries     int                    `json:"max_retries,omitempty"`
}

type Action struct {
    ActionType     string                 `json:"action_type"`
    Node           NodeDTO                `json:"node"`
    
    // 🆕 新增：设置错误处理器
    ErrorHandlerNode NodeDTO              `json:"error_handler_node,omitempty"`
}
```

**修改文件**：`pkg/runtime/runtime.go`

```go
// 🔧 FIX: 错误处理逻辑
func (r *Runtime) Execute(initialRequest string) error {
    for !r.cursor.Done() {
        current := r.cursor.Current
        
        // ... LLM 调用 ...
        
        actionErr := false
        for _, action := range response.Actions {
            if err := r.executeAction(action, current); err != nil {
                if strings.Contains(err.Error(), "EMERGENCY_SHUTDOWN") {
                    return err
                }
                
                // 🔧 FIX: 错误处理逻辑
                if r.handleError(current, err) {
                    // 错误已被处理，继续执行
                    actionErr = false
                    break
                } else {
                    // 错误未被处理，触发重试
                    lastErr = err
                    actionErr = true
                    break
                }
            }
        }
        
        if actionErr {
            current.RetryCount++
            if current.RetryCount > current.MaxRetries {
                return fmt.Errorf("max retries exceeded for node [%s]: %w", current.ID, lastErr)
            }
            continue  // 重试
        }
        
        // 重置重试计数
        current.RetryCount = 0
        
        // ... 决定下一步 ...
    }
    return nil
}

// 🆕 新增：错误处理函数
func (r *Runtime) handleError(node *tasknode.TaskNode, err error) bool {
    if node.ErrorHandler == nil {
        return false  // 没有错误处理器
    }
    
    fmt.Printf("  ⚠️ Node [%s] failed: %v\n", node.ID, err)
    fmt.Printf("  🔧 Executing error handler: %s\n", node.ErrorHandler.Name)
    
    // 保存错误信息到变量
    if node.LocalVariables == nil {
        node.LocalVariables = make(map[string]interface{})
    }
    node.LocalVariables["last_error"] = err.Error()
    
    // 将错误处理节点添加为子节点（如果还没有）
    if !r.isChildOf(node, node.ErrorHandler) {
        node.AddChild(node.ErrorHandler)
    }
    
    // 标记当前节点为已遍历（跳过正常子节点）
    node.MarkTraveled()
    
    return true  // 错误已被处理
}

func (r *Runtime) isChildOf(parent, child *tasknode.TaskNode) bool {
    for _, c := range parent.Children {
        if c == child {
            return true
        }
    }
    return false
}

// 🔧 FIX: executeAction 中支持设置错误处理器
func (r *Runtime) executeAction(action llm.Action, parent *tasknode.TaskNode) error {
    switch action.ActionType {
    case "create_node":
        childNode := action.Node.ToTaskNode()
        parent.AddChild(childNode)
        
        // 🆕 如果指定了错误处理节点
        if action.ErrorHandlerNode.ID != "" {
            errorHandler := action.ErrorHandlerNode.ToTaskNode()
            childNode.ErrorHandler = errorHandler
        }
        return nil
    
    // ... 其他 action ...
    }
}
```

**LLM Prompt 指导**：
```markdown
## Error Handling

You can specify an error handler for any node:

{
  "action_type": "create_node",
  "node": {
    "id": "read_file",
    "name": "Read Data File",
    "type": "Leaf",
    "information": "Read data.csv"
  },
  "error_handler_node": {
    "id": "use_default",
    "name": "Use Default Data",
    "type": "Leaf",
    "information": "Load default dataset"
  }
}

If "read_file" fails after max retries, "use_default" will be executed automatically.
The error message will be available in the variable "last_error".
```

#### 方案 B：Try-Catch 节点（备选）

**核心思想**：新增 `TryCatch` 节点类型

```go
type TaskType int

const (
    Normal TaskType = iota
    Loop
    Leaf
    TryCatch  // 🆕 新增
)

type TaskNode struct {
    // ... 其他字段 ...
    
    // 对于 TryCatch 节点
    TryBranch   *TaskNode
    CatchBranch *TaskNode
}
```

**问题**：
- ❌ 需要修改核心节点类型
- ❌ 增加系统复杂度
- ❌ LLM 需要理解新的节点类型

#### 方案 C：错误状态传播（备选）

**核心思想**：节点失败时，设置 `Status = Failed`，父节点检查子节点状态

```go
func (r *Runtime) decideNextStep(current *tasknode.TaskNode) error {
    // 检查子节点是否有失败的
    for _, child := range current.Children {
        if child.Status == tasknode.Failed {
            // 执行错误恢复逻辑
            return r.handleChildFailure(current, child)
        }
    }
    // ...
}
```

**问题**：
- ❌ 需要在每个节点检查子节点状态
- ❌ 错误处理逻辑分散
- ❌ 不够直观

### 推荐方案

**方案 A**（ErrorHandler 节点），原因：
1. ✅ 语义清晰：每个节点可以指定错误处理器
2. ✅ 灵活性：LLM 可以为不同节点设置不同的错误处理策略
3. ✅ 向后兼容：不修改现有节点类型
4. ✅ 易于理解：类似于 try-catch，但更灵活

### 测试用例

```go
func TestErrorHandling(t *testing.T) {
    // 测试 1：错误处理器被正确调用
    root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, nil)
    failNode := tasknode.NewTaskNode("fail", "Fail Node", tasknode.Leaf, nil)
    errorHandler := tasknode.NewTaskNode("handler", "Error Handler", tasknode.Leaf, nil)
    
    failNode.ErrorHandler = errorHandler
    root.AddChild(failNode)
    
    engine := &MockEngineWithError{/* 模拟失败 */}
    runtime := NewRuntime(engine, root)
    
    err := runtime.Execute("test")
    
    // 验证：错误处理器被执行
    assert.True(t, errorHandler.WetherTraveled)
    
    // 验证：错误信息被保存
    assert.NotNil(t, failNode.LocalVariables["last_error"])
    
    // 测试 2：没有错误处理器时，重试后退出
    // 测试 3：嵌套错误处理
}
```

---

## 总结：修复优先级

| Bug | 严重程度 | 修复难度 | 推荐方案 | 预计工作量 |
|-----|---------|---------|---------|-----------|
| **LoopStack 管理** | ⭐⭐⭐⭐ | 低 | 方案 A（Runtime 管理） | 2-3 小时 |
| **变量作用域隔离** | ⭐⭐⭐⭐ | 中 | 方案 A（局部+导出） | 4-6 小时 |
| **错误处理机制** | ⭐⭐⭐⭐ | 中 | 方案 A（ErrorHandler） | 4-6 小时 |

**总计**：10-15 小时（约 2 个工作日）

---

## 实施顺序建议

1. **先修复 LoopStack**（最简单，风险最低）
2. **再修复变量作用域**（影响面较大，需要仔细测试）
3. **最后添加错误处理**（新功能，可以逐步完善）

---

## 需要您审核的问题

1. **LoopStack 管理**：
   - 是否同意在 Runtime 中管理 push/pop？
   - 还是更倾向于在 Cursor 中管理？

2. **变量作用域**：
   - 是否同意拆分为 LocalVariables 和 ExportedVars？
   - 默认行为：命令结果是局部变量，update_variables 是导出变量，是否合理？

3. **错误处理**：
   - 是否同意 ErrorHandler 节点的设计？
   - MaxRetries 默认值 9 是否合适？
   - 是否需要支持多个错误处理器（例如：不同类型的错误用不同的处理器）？

请审核以上方案，我会根据您的反馈进行实施！
