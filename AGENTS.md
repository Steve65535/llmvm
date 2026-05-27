# Repository Guidelines

## Project Structure & Module Organization

This repository implements LLMVM, a Go runtime that executes LLM-generated task trees with explicit control flow, structured state, artifacts, and retrieval-backed memory.

- `cmd/`: CLI entry point. Handles flags such as `--save`, `--load`, resume flow, state serialization, and bootstrapping the runtime.
- `pkg/runtime/`: core execution engine. Owns the DFS loop, prompt construction, node activation, action execution, sandbox enforcement, context budget handling, human-in-the-loop waiting, and memory synchronization.
- `pkg/llm/`: LLM interface layer. Contains API request/response types, system prompt construction, response parsing, action schema validation, and async engine wrappers.
- `pkg/tasknode/`: task tree model. Defines node types, statuses, scoped variables, structured handoff fields, human request/response payloads, and parent restoration after JSON load.
- `pkg/cursor/`: traversal logic for the task tree, including cursor movement and loop-related execution state.
- `pkg/artifact/`: artifact store. Tool results are stored as stable artifact IDs with summaries, line slicing, eviction, pinning, and spill-to-disk behavior.
- `pkg/memory/`: SQLite-backed structured memory. Indexes nodes, handoffs, variables, artifacts, and FTS records for deterministic retrieval.
- `pkg/vfs/`: virtual filesystem code; treat as legacy unless a task explicitly targets it.
- `visualizer/`: Go visualization server for reading saved runtime state and exposing tree/artifact APIs.
- `visualizer/frontend/`: Vite + React frontend for the visualizer.
- `markdown/`: design notes and experimental architecture documents. This directory is currently ignored by git, so changes there will not appear in normal `git status`.
- `deep_compiler.json`: sample saved runtime state used by the visualizer.

## Architecture Notes

The AST/task tree is the authority for control flow. SQLite memory is a rebuildable query index, not the source of truth. Artifact content should stay in the artifact store; variables and handoffs should reference artifact IDs rather than copying large content into node state.

Runtime calls should remain stateless from the LLM's perspective: build a deterministic context snapshot, call the model, parse a structured action list, execute actions, persist state, then move the cursor. Avoid adding hidden dependence on chat history or process-local transient state that cannot be recovered from saved JSON plus artifact metadata.

When changing action handling, update the full path:

1. Prompt/action documentation in `pkg/llm/api.go`.
2. DTOs and validation in `pkg/llm/parser.go`.
3. Runtime execution in `pkg/runtime/`.
4. Task node or artifact/memory structs if state must persist.
5. Tests for parser validation and runtime behavior.

## Build, Test, and Development Commands

Root Go module:

```bash
go test ./...
go test ./pkg/llm -v
go test ./pkg/runtime -v
go test ./pkg/artifact -v
go test ./pkg/memory -v
go run cmd/main.go "Analyze this repository"
go run cmd/main.go --save state.json "Run a complex task"
go run cmd/main.go --load state.json
```

Visualizer server:

```bash
cd visualizer
go run server.go -file ../deep_compiler.json
```

Frontend:

```bash
cd visualizer/frontend
npm run dev
npm run build
npm run lint
```

There is no root Makefile. Prefer direct `go test`, `go run`, and frontend `npm` scripts.

## Coding Style & Naming Conventions

Use `gofmt` for all Go files. Keep package names short, lowercase, and aligned with directory names. Exported Go names use PascalCase; unexported helpers use camelCase. Test names should describe behavior, for example `TestParseResponseRejectsInvalidActionType`.

Prefer explicit structs, typed constants, and small validation helpers over loosely typed maps. The LLM boundary is the main exception: JSON payloads may initially decode into DTO structs or `map[string]interface{}` where action-specific data requires it, but validation should convert ambiguity into precise errors as early as possible.

Keep runtime behavior deterministic. Stable ordering matters for prompt cache behavior, reproducible tests, and visualizer output. Sort map-derived slices before rendering prompts or serializing index-like data when order could otherwise vary.

Avoid large unrelated refactors. This codebase has several tightly coupled runtime paths; scoped changes are easier to verify.

## Parser and Action Schema Guidelines

The parser is a safety boundary. Treat malformed LLM output as expected input, not as an exceptional edge case.

When adding or modifying an action:

- Add fields to `Action` or related DTOs in `pkg/llm/parser.go`.
- Validate required fields and enum values in `ParseResponse`.
- Keep error messages specific enough to feed back into the next model retry.
- Add parser tests for valid payloads, missing required fields, invalid enum values, and wrong field types where relevant.
- Update the system prompt in `pkg/llm/api.go` so the model sees the same contract the parser enforces.

Do not rely only on prompt instructions for correctness. Runtime validation must reject invalid or unsafe actions.

## Runtime and Sandbox Guidelines

File tool actions must remain constrained to `test/sandbox/`. Preserve separator-aware path checks and do not introduce prefix-only path validation. Be especially careful around `..`, symlinks, absolute paths, and platform path separators.

Shell execution is intentionally powerful. If changing command execution, distinguish between tool-level file operations, which are sandboxed, and explicit shell execution, which has broader host access by design.

State persistence is a core feature. Any new runtime state that affects execution must be serializable, restorable, and compatible with `--save` / `--load`. If the state is only an index, document how it is rebuilt.

## Artifact and Memory Guidelines

Artifacts should be the default home for large tool outputs, file reads, command output, and reusable evidence. Store summaries and references in variables or handoffs instead of embedding full content.

When writing memory-related changes:

- Keep AST/task node state authoritative.
- Treat SQLite as a secondary index that can be rebuilt.
- Include provenance in query results where possible: node ID, artifact ID, source table, timestamp, or producer.
- Avoid exposing arbitrary SQL to the LLM. Prefer constrained query types such as `sibling_handoffs`, `ancestor_chain`, `pinned_artifacts`, `recent_artifacts`, and `fts_artifacts`.

## Testing Guidelines

Tests use Go's standard `testing` package. Place tests beside the package under test and name files `*_test.go`.

Important existing test areas:

- `pkg/llm/*_test.go`: parser and variable/action decoding.
- `pkg/runtime/runtime_new_test.go`: runtime behavior and structured handoff flow.
- `pkg/artifact/store_test.go`: artifact add/read/slice/eviction/pin/spill behavior.
- `pkg/memory/memory_test.go`: SQLite schema, indexing, and query behavior.
- `pkg/tasknode/tasknode_new_test.go`: task node serialization and structured fields.

For parser changes, include table-driven tests. For runtime changes, test the persisted state shape when possible. For memory changes, test both write/index behavior and query results. For visualizer changes, run frontend lint/build and, when behavior changes, verify against `deep_compiler.json`.

## Frontend and Visualizer Guidelines

The visualizer is a developer tool, not a marketing page. Keep UI dense, readable, and focused on inspecting runtime state. Prefer clear panels for tree nodes, artifacts, variables, handoffs, and errors. Do not add decorative UI that makes debugging harder.

Frontend code should follow the existing Vite/React structure under `visualizer/frontend/src/`. Use the provided `npm run lint` and `npm run build` scripts before considering UI work complete.

## Commit & Pull Request Guidelines

Recent commit history uses Conventional Commit-style prefixes:

- `feat: add structured runtime memory`
- `fix: store command output in artifacts`
- `docs: add position engineering plan`
- `security: remove .env file and add to gitignore`
- `chore: remove research materials from repository`

Use the same style. Keep the subject imperative and scoped. Good examples:

- `feat: add parser error classification`
- `fix: preserve artifact refs on restore`
- `docs: expand RAG artifact design notes`

Pull requests should include:

- Problem statement.
- Summary of implementation.
- Tests run, with exact commands.
- State format or migration notes if persistence changed.
- Screenshots or short recordings for visualizer UI changes.
- Security notes for sandbox, shell, file, or API-key related changes.

## Security & Configuration Tips

Do not commit `.env` files, API keys, saved credentials, or local database files. `DEEPSEEK_API_KEY` enables live model calls. `CONTEXT_BUDGET` controls the total context budget.

Be careful with saved execution states. They may contain prompts, task descriptions, artifact metadata, command output summaries, or file paths. Treat them as potentially sensitive unless explicitly sanitized.

## Agent-Specific Instructions

Before editing, read the relevant package and tests. Prefer `rg` and `rg --files` for code search. Keep edits narrow and preserve user changes in the working tree.

When implementing a behavioral change, update tests in the same turn whenever feasible. When changing documentation in `markdown/`, remember that the directory is gitignored; mention that explicitly if the user expects git-visible changes.

If a task touches OpenAI/LLM provider behavior, distinguish provider-level output constraints from prompt-only guidance. Parser and runtime validation remain required even when the provider supports JSON mode or schema-constrained output.
