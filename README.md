# LLMVM (LLM Virtual Machine)

**LLMVM** is a Turing-Complete Agent Runtime that fundamentally reimagines how Large Language Models (LLMs) execute complex tasks. Instead of the traditional "Chain of Thought" loop, LLMVM acts as a semantic state machine that dynamically constructs and executes a dedicated Program Syntax Tree (AST) for each task.

## 🚀 Key Highlights

*   **True Turing Completeness**: Unlike standard Agents that rely on probabilistic loops, LLMVM implements explicit control flow structures.
    *   **Loop Nodes**: Managed by a dedicated runtime stack, ensuring cyclic logic is executed faithfully until exit conditions are met.
    *   **DFS Execution**: Uses Depth-First Search for task execution, mimicking the call stack of a compiled program rather than a flat list of actions.

*   **Stateless Architecture**: Solves the "Context Window Explosion" problem.
    *   **No History Dependency**: The LLM is not fed the entire conversation history.
    *   **Structured State**: At each step, the LLM receives a precise, stateless slice of the current execution context (Current Node, Parent Info, Loop State). This allows LLMVM to run indefinitely without degrading performance or increasing costs.

*   **Bootstrapped Program Construction**:
    *   **JIT Planning**: The program isn't pre-written; it's compiled *Just-In-Time*.
    *   The LLM acts as the "ALU" (Arithmetic Logic Unit) for semantics, while the Go runtime acts as the "CPU" for control flow and memory.

## 🛠 Architecture

LLMVM separates **Logic (Control Flow)** from **Semantics (Intelligence)**.

```mermaid
graph TD
    User[User Input] --> Root[Root Task Node]
    Root --> Runtime
    
    subgraph "LLMVM Runtime (Go)"
        Runtime[Runtime Engine] <--> Cursor[Cursor (Read/Write Head)]
        Cursor <--> TaskTree[Task Syntax Tree]
        Runtime -- State Snapshot --> Adapter[Prompt Adapter]
    end
    
    subgraph "Semantic Processing (LLM)"
        Adapter -- Stateless Prompt --> LLM[DeepSeek/LLM]
        LLM -- Structured Action --> Adapter
    end
    
    Adapter -- New Nodes / Status --> Runtime
```

1.  **TaskTree**: A dynamic tree structure representing the program state. Nodes can be `Normal`, `Loop`, or `Leaf`.
2.  **Cursor**: Tracks the current execution point, managing traversal and loop stacks.
3.  **Stateless Prompting**: The Runtime constructs a JSON-structured snapshot of the current node and its immediate neighbors. The LLM only sees this snapshot.

## 📦 Installation

```bash
git clone https://github.com/Steve65535/llmvm.git
cd llmvm
go mod download
```

## ⚡ Usage

Run the VM with a natural language command:

```bash
export DEEPSEEK_API_KEY="your_api_key"
go run cmd/main.go "Analyze this project's code structure and highlight key architectural patterns"
```

Or enter interactive mode:

```bash
go run cmd/main.go
# Then type your command
```

## 📂 Project Structure

*   `cmd/`: Entry point.
*   `pkg/runtime/`: The core VM engine (The "CPU").
*   `pkg/cursor/`: Pointer logic and Stack management.
*   `pkg/tasknode/`: The data structure for the AST (The "Memory").
*   `pkg/llm/`: Interface adapters for LLMs (The "ALU").

## 📄 License

MIT
