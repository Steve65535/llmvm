# LLMVM (LLM Virtual Machine) - 中文版

**LLMVM** 是一个图灵完备的 Agent 运行时，它从根本上重新想象了大语言模型（LLM）执行复杂任务的方式。LLMVM 不再依赖传统的“思维链（CoT）”循环，而是作为一个语义状态机，为每个任务动态构建并执行专用的程序语法树（AST）。

## 🚀 核心亮点

*   **真正的图灵完备**：不同于依赖概率循环的标准 Agent，LLMVM 实现了显式的控制流结构。
    *   **循环节点 (Loop Nodes)**：由专用运行时栈管理，确保循环逻辑忠实执行，直至满足退出条件。
    *   **DFS 执行**：使用深度优先搜索进行任务执行，模拟编译程序的调用栈，而非扁平的动作列表。

*   **无状态架构 (Stateless)**：通过绝不向模型输入完整的对话历史，解决了“上下文窗口爆炸”问题。在每一步，LLM 仅接收当前状态的精准快照。

*   **全局注意力感知 (Global Attention v4)**：一种基于优先级的“全树扫描”机制。它会实时扫描整棵任务树，并将最近完成的 10 个关键节点结果（跨分支）Pick 进当前上下文窗口，提供全局“内存（RAM）”视野。

*   **物理文件操作 (Physical VFS v2)**：内置 `execute_command` 动作，支持在宿主系统上执行 `ls`, `cat`, `write`, `rm` 等真实文件操作。

*   **作用域节点变量 (Scoped Variables)**：遵循 DFS 生命周期的分布式内存，在保持路径特定状态的同时防止上下文污染。

*   **上下文感知叶节点**：将“叶节点”定义为大小足以完美适配 LLM 最佳上下文窗口的任务单元，通过主动拆解确保高质量推理。

*   **自主纠错 (Robustness)**：
    *   **Try-Catch 机制**：内置重试循环（默认 3 次），用于处理解析或执行失败。
    *   **错误反馈**：运行时自动捕获错误并将其反馈给 LLM，引导其自我修正。

## 🛠 架构原理

LLMVM 将 **逻辑（控制流）** 与 **语义（智能）** 分离。

```mermaid
graph TD
    User["用户输入"] --> Root["根任务节点"]
    Root --> Runtime["运行时引擎"]
    
    subgraph "LLMVM 运行时 (Go)"
        Runtime <--> Cursor["游标 (读写头)"]
        Cursor <--> TaskTree["任务语法树 (AST)"]
        Runtime -- 状态快照 --> Adapter["提示词适配器"]
    end
    
    subgraph "语义处理 (LLM运算器)"
        Adapter -- 无状态提示词 --> LLM["DeepSeek / LLM"]
        LLM -- 结构化动作 --> Adapter
    end
    
    Adapter -- "新节点 / 状态更新" --> Runtime
```

1.  **TaskTree**: 存储程序状态的动态树结构。节点类型包括 `Normal`, `Loop`, 或 `Leaf`。
2.  **Cursor**: 追踪当前执行点，管理遍历和循环栈。
3.  **Stateless Prompting**: 运行时构建当前节点及其近邻的 JSON 结构化快照，最大化上下文效率。

## 📦 安装指南

```bash
git clone https://github.com/Steve65535/llmvm.git
cd llmvm
go mod download
```

### 🔑 环境变量
你需要设置 DeepSeek API key 才能使用实时引擎：
```bash
export DEEPSEEK_API_KEY="your_api_key_here"
```

## ⚡ 使用方法

使用自然语言指令运行 VM：

```bash
go run cmd/main.go "分析此项目的代码结构并突出关键架构模式"
```

或进入交互模式：

```bash
go run cmd/main.go
# 然后在提示符下输入指令
```

## 📂 项目结构

*   `cmd/`: CLI 入口。
*   `pkg/runtime/`: 核心 VM 引擎（控制器/CPU）。
*   `pkg/cursor/`: 游标逻辑与栈管理（读写头）。
*   `pkg/tasknode/`: AST 数据结构（内存/程序存储）。
*   `pkg/llm/`: LLM 接口适配器（算术逻辑单元/ALU）。

## 📄 开源协议

MIT
