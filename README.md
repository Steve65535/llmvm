# LLMVM (LLM Virtual Machine)

**LLMVM** is an advanced Agent Runtime that fundamentally reimagines how Large Language Models (LLMs) execute complex tasks. Instead of the traditional "Chain of Thought" loop, LLMVM acts as a semantic state machine that dynamically constructs and executes a dedicated Program Syntax Tree (AST) for each task.

## 🚀 Key Highlights

*   **Explicit Control Flow**: Unlike standard Agents that rely on probabilistic loops, LLMVM implements explicit control flow structures.
    *   **Loop Nodes**: Managed by a dedicated runtime stack, ensuring cyclic logic is executed faithfully until exit conditions are met.
    *   **DFS Execution**: Uses Depth-First Search for task execution, mimicking the call stack of a compiled program rather than a flat list of actions.

*   **Stateless Architecture**: Solves the "Context Window Explosion" problem by never feeding the entire conversation history to the model. At each step, the LLM receives only a precise snapshot of the current state.

*   **Artifact-Based Memory System**: A structured information management layer that replaces raw variable dumping.
    *   **Stable IDs**: Every tool result (file reads, searches, command outputs) is stored as an artifact with a globally unique ID (`art_1`, `art_2`, ...).
    *   **Summaries, Not Full Text**: Variables only hold artifact references. The LLM sees summaries in the prompt and uses `read_artifact` for on-demand slice access.
    *   **LRU Eviction + Disk Spill**: Store holds up to 50 active artifacts. Large objects (>8KB) spill to disk. Evicted artifacts retain their summary as tombstones.
    *   **Pin Protection**: Important artifacts can be pinned to survive eviction.

*   **SQLite Structured Memory** (`pkg/memory/`): A queryable retrieval index that mirrors the AST state. The AST remains the control-flow authority; SQLite is a secondary index for fast lookups.
    *   Indexes nodes, structured handoffs, scoped variables, and artifacts (including FTS5 full-text search).
    *   LLM can query it via the `query_memory` action — no extra API calls needed.
    *   Auto-rebuilt from the AST on `--load` so the index is never stale after a restore.

*   **Structured Node Handoff**: Every completed node produces a structured report:
    *   `summary`: What was accomplished
    *   `key_facts`: Key findings as a bullet list
    *   `decisions`: Important choices made during this node
    *   `assumptions`: Unverified assumptions made
    *   `outputs`: Files, values, or state produced
    *   `open_questions`: Known unresolved issues for downstream nodes
    *   `artifact_refs`: Referenced artifact IDs
    *   `handoff`: One-line guidance for downstream nodes
    *   `confidence`: `"high"` / `"medium"` / `"low"` (auto-set to `"auto_generated"` if omitted)
    *   Runtime auto-generates operation logs as fallback if the LLM omits these fields.

*   **Budget-Controlled Global Context**: Runtime auto-assembles context with hard character limits derived proportionally from a total token budget:
    *   Tree index with result summaries (5% of budget)
    *   Artifact index (3% of budget, 20 entries max)
    *   Sibling handoffs (2% of budget)
    *   Replaces the old `selectAttentionNodes` LLM call — zero extra API calls.
    *   Total budget defaults to 64K tokens; override with `CONTEXT_BUDGET=<tokens>` env var.

*   **Infinite Continuous Reasoning**: No hard iteration limit. Stagnation detection (3× identical responses) is the only safety valve, allowing arbitrarily long task chains.

*   **Context Overflow Recovery**: When the API returns a context overflow error, the runtime progressively compresses the prompt through 4 levels (trim global context → trim history → strip all ephemeral variables) before falling back to retry limits.

*   **Runtime-Enforced Sandbox**: All file operations (`read_file`, `write_file`, `list_dir`, `search`, `append_to_file`) are validated at runtime to stay within `test/sandbox/`. Uses separator-aware path checking to prevent prefix bypass attacks.

*   **Structured File Tools**: Six dedicated tools beyond raw shell execution:

    | Tool | Description |
    |---|---|
    | `read_file` | Read file → artifact |
    | `write_file` | Create/overwrite file |
    | `list_dir` | List directory → artifact |
    | `search` | Recursive grep → artifact (distinguishes no-match vs error) |
    | `append_to_file` | Incremental file building |
    | `read_artifact` | Slice-based artifact access (default 50 lines) |

*   **Full Shell Injection**: Real shell execution (`sh -c`) with piping, redirection, and access to all host utilities.

*   **State Persistence**: Full task tree + artifact store serialize to JSON. Supports `--save` / `--load` for resuming execution. Auto-saves on each step and on Ctrl+C. Spill files are validated on restore; missing ones gracefully degrade to tombstones.

*   **Human-in-the-Loop**: The LLM can pause execution and request human input via `request_human_input`. The node enters `WaitingHuman` status, state is persisted, and execution resumes after the human responds. The runtime exposes a pluggable `HumanInputFunc` for custom integrations.

*   **Autonomous Self-Correction**:
    *   **Stagnation Detection**: Identical responses trigger escalating intervention (warning → forced strategy change → node failure).
    *   **Error Feedback**: Runtime captures errors and feeds them back to the LLM for self-correction.
    *   **Error Handlers**: Nodes can specify `error_handler_node` for structured error recovery.

*   **Bootstrapped JIT Logic**: The program isn't pre-written; it's compiled *Just-In-Time* by the LLM (acting as the ALU) and executed by the Go runtime (acting as the CPU).

## 🛠 Architecture

LLMVM separates **Logic (Control Flow)** from **Semantics (Intelligence)**.

```mermaid
graph TD
    User["User Input"] --> Root["Root Task Node"]
    Root --> Runtime["Runtime Engine"]

    subgraph "LLMVM Runtime (Go)"
        Runtime <--> Cursor["Cursor (Read/Write Head)"]
        Cursor <--> TaskTree["Task Syntax Tree"]
        Runtime <--> ArtStore["Artifact Store"]
        Runtime -- State Snapshot --> Adapter["Prompt Adapter"]
    end

    subgraph "Semantic Processing (LLM)"
        Adapter -- Stateless Prompt --> LLM["DeepSeek / LLM"]
        LLM -- Structured Action --> Adapter
    end

    Adapter -- "New Nodes / Status" --> Runtime
```

1.  **TaskTree**: A dynamic tree structure representing the program state. Nodes can be `Normal`, `Loop`, or `Leaf`.
2.  **Cursor**: Tracks the current execution point, managing traversal and loop stacks.
3.  **Artifact Store**: Manages tool results as stable-ID objects with summaries, eviction, and disk spill.
4.  **Memory Store**: SQLite index of nodes, handoffs, variables, and artifacts. Queryable by the LLM via `query_memory`; rebuilt automatically on `--load`.
5.  **Stateless Prompting**: The Runtime constructs a JSON-structured snapshot of the current node, global context (tree index + artifact index + sibling handoffs), and scoped variables.

## 📦 Installation

```bash
git clone https://github.com/Steve65535/llmvm.git
cd llmvm
go mod download
```

### 🔑 Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DEEPSEEK_API_KEY` | — | DeepSeek API key (required for live engine; falls back to `StubEngine` if unset) |
| `CONTEXT_BUDGET` | `64000` | Total token budget; all sub-budgets (tool results, tree index, artifact index, handoffs) are derived proportionally |

## ⚡ Usage

Run the VM with a natural language command:

```bash
go run cmd/main.go "Analyze this project's code structure and highlight key architectural patterns"
```

Or enter interactive mode:

```bash
go run cmd/main.go
# Then type your command at the prompt
```

Save and resume execution:

```bash
# Save state after each step
go run cmd/main.go --save state.json "your complex task"

# Resume from saved state
go run cmd/main.go --load state.json
```

## 📂 Project Structure

*   `cmd/`: CLI entry point with save/load support.
*   `pkg/runtime/`: The core VM engine (The "CPU") — execution loop, global context assembly, sandbox enforcement, context compression, human-in-the-loop handling.
*   `pkg/cursor/`: Pointer logic and stack management.
*   `pkg/tasknode/`: The data structure for the AST (The "Memory") — includes structured handoff fields and `WaitingHuman` status.
*   `pkg/llm/`: Interface adapters for LLMs (The "ALU") — system prompt, action parsing, response validation.
*   `pkg/artifact/`: Artifact Store — stable-ID information objects with LRU eviction, disk spill, and slice-based access.
*   `pkg/memory/`: SQLite structured retrieval layer — queryable index of nodes, handoffs, scoped variables, and artifacts (FTS5).
*   `pkg/vfs/`: Virtual filesystem (legacy).
*   `visualizer/`: Tree visualization web server with artifact panel.

## 🧪 Testing

```bash
# All tests
go test ./...

# LLM parser tests
go test ./pkg/llm/ -v

# Artifact store tests (add, slice, eviction, pin, spill, index)
go test ./pkg/artifact/ -v

# SQLite memory store tests
go test ./pkg/memory/ -v

# Runtime integration tests
go test ./pkg/runtime/ -v
```

## 📄 License

MIT
