# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build / Run / Test

```bash
# Run from a natural-language command
go run cmd/main.go "your task"

# Interactive mode (no args)
go run cmd/main.go

# Persistent execution (autosaves on each step + on Ctrl+C)
go run cmd/main.go --save state.json "your task"
go run cmd/main.go --load state.json
go run cmd/main.go --load state.json --resume <node-id>   # inject human reply for WaitingHuman node

# All tests
go test ./...

# Per-package (-v gets the structured logs LLMVM emits)
go test ./pkg/llm/      -v
go test ./pkg/runtime/  -v
go test ./pkg/artifact/ -v
go test ./pkg/memory/   -v

# Single test (Go pattern)
go test ./pkg/runtime/ -run TestRuntime_AppendSibling -v
```

`--save foo.json` derives a sibling `foo.sqlite` for the structured-memory index. On `--load`, the SQLite index is rebuilt from the AST so a missing/stale `.sqlite` is non-fatal — the AST is the authority.

### Environment

| Variable | Default | Effect |
|---|---|---|
| `DEEPSEEK_API_KEY` | — | If unset, runtime falls back to `llm.StubEngine` (deterministic, no network). All tests run on the stub. |
| `CONTEXT_BUDGET` | `64000` | Total token budget; sub-budgets (tool results, tree index, artifact index, handoffs) derive proportionally — see `newBudgetConfig` in `pkg/runtime/runtime.go`. |

`.env` / `.env.local` are auto-loaded via `godotenv`.

## Architecture (the parts that span files)

LLMVM is a **stateless agent runtime built around a task syntax tree**. The LLM is the ALU; Go is the CPU. Crucially, the model is **never given conversation history** — each LLM call gets only a JSON snapshot of the current node plus a deterministically-assembled global context.

### The data flow you must understand before changing runtime code

```
cmd/main.go
  → runtime.NewRuntime(engine, root, dbPath)
  → rt.Execute(initialRequest)
      loop:
        cursor.Current()                          (pkg/cursor)
        runtime.buildNodeActivation(node)         (pkg/runtime/activation.go)
          ├ hierarchy path + parent goal
          ├ sibling handoffs (from memory.Store)
          ├ artifact index (from artifact.Store + memory.Store FTS)
          └ open questions
        runtime.buildPromptWithGlobalContext(...) (pkg/runtime/runtime.go)
        engine.Generate(prompt)                   (pkg/llm/api.go or StubEngine)
        llm.ParseResponse(json)                   (pkg/llm/parser.go) — strict schema validation
        runtime.executeAction(action)             (pkg/runtime/actions.go)
          ├ create_node / append_sibling_node / mark_complete
          ├ read_file / write_file / list_dir / search / append_to_file (sandbox-validated)
          ├ execute_command (raw shell)
          ├ read_artifact (slice access)
          ├ request_human_input → WaitingHuman    (returns from Execute; resume via --resume)
          └ query_memory (sibling_handoffs | ancestor_chain | pinned_artifacts | recent_artifacts | fts_artifacts)
        memStore.Upsert*                          (pkg/memory/memory.go) — mirrors AST changes
        cursor.Advance()                          (DFS; loop stack for Loop nodes)
        OnStepComplete(node)                      (autosave hook)
```

### The four memory layers — they are not interchangeable

| Layer | Authority for | Lives in |
|---|---|---|
| **TaskNode AST** | Control flow, node status, structured handoff fields | `pkg/tasknode` (in-memory, JSON-serialized) |
| **Artifact Store** | Tool results (file reads, command output, search hits) — stable IDs `art_1`, `art_2`, …; LRU=50, >8KB spills to disk, `pinned` survives eviction | `pkg/artifact` |
| **SQLite Memory Store** | Queryable index over nodes, handoffs, scoped variables, artifacts (FTS5) | `pkg/memory` |
| **Scoped Variables** | Per-node KV bag; nearest-ancestor wins on lookup | `TaskNode.Variables` |

The AST is **always** the source of truth. SQLite is a rebuildable index — `rt.RebuildIndexFromAST()` runs on `--load`. When you mutate a node, handoff, or artifact, write through to `memStore` so query_memory stays consistent. See existing call sites in `pkg/runtime/runtime.go` for the pattern.

### Why prompts look the way they do

`buildPromptWithGlobalContext` is **deterministic and budget-bound**. It does not call the LLM to pick what to include. Sub-budgets come from `newBudgetConfig(totalTokens)`. The order of sections is stable to keep the upstream prompt cache warm.

When the API returns a context-overflow error, `compressionLevel` escalates 0→4 (drop workspace, trim history, strip ephemeral variables) before giving up. Don't add new context fields without picking a compression level that drops them.

### Stagnation detection is the only safety valve

There is **no hard iteration cap** (`Execute` loops forever in principle). The only break condition besides task completion is `stagnationCount` — three identical LLM responses on the same node escalate to a warning, then a forced strategy change, then node failure. If you add a new action type that legitimately produces identical responses (e.g., a poll), reset `lastResponse` after handling it.

### Action schema — `pkg/llm/parser.go` is the contract

`ParseResponse` enforces required fields per `action_type`. Adding a new action means: (1) extend `Action` struct, (2) add validation in `ParseResponse`, (3) handle in `executeAction` (`pkg/runtime/actions.go`), (4) document in the system prompt at `pkg/llm/api.go`, (5) add parser tests in `pkg/llm/parser_*_test.go`. All five steps — skipping the system prompt is the most common source of "the model never emits this" bugs.

### Sandbox

`read_file`, `write_file`, `list_dir`, `search`, `append_to_file` are validated to stay under `test/sandbox/` using separator-aware prefix checking (not raw `strings.HasPrefix` — that allows `test/sandbox-evil/` to pass). `execute_command` is **not** sandboxed — it shells out to `sh -c`. Treat it as the privileged escape hatch.

### Human-in-the-loop suspends the process

`request_human_input` sets node status to `WaitingHuman`, persists state, and **returns from `Execute`**. Resumption is a fresh process invocation: `--load state.json --resume <node-id>` reads stdin (or `HumanInputFunc` if you've injected one for tests/integration) and continues. This is intentional — it makes long-running flows survive crashes, but it means you cannot resume in-process without calling `rt.ResumeWithHumanResponse` directly.

## Repo conventions worth knowing

- **Comments and identifiers are bilingual** (Chinese + English). Match the surrounding style of the file you're editing rather than normalizing.
- `markdown/` and `AGENTS.md` / `CLAUDE.md` are gitignored — design docs live there but don't ship.
- The visualizer (`visualizer/`) is a separate Go module. It reads the same save-state JSON; if you change `SaveState` in `cmd/main.go` or `TaskNode` fields, update the visualizer's parsing too.
- Tests use `llm.StubEngine` — no real API calls. When adding runtime tests, drive scenarios by enqueuing canned responses on the stub (see `pkg/runtime/runtime_new_test.go`).
- Default to editing existing files; new packages should justify themselves against the four-layer split above.
