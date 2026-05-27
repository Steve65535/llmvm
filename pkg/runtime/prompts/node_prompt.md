%s
%s
%s
## Current Context

**Task Path**: %s

**Current Node**:
- ID: %s
- Name: %s
- Type: %s
- Status: %s
- Index: %d
- WetherTraveled: %v
- WetherFinished: %v
- Information: %s

**Parent Node**:
- ID: %s
- Name: %s
- Type: %s
- Status: %s

**Children Status**:
%s

**Loop Context**:
%s

## Global Context (Tree Index + Artifacts + Sibling Handoffs)
%s

## Structured Request Data

%s

## Scoped Variables (Current Path Context)

%s
%s

## Request

%s

## Your Task

Please respond with valid JSON in the required format.

## Node Type Guidelines

**When to create each type**:
- **Normal**: For tasks that can be decomposed into sequential sub-tasks
  - Example: "Build web app" → [Setup, Frontend, Backend, Deploy]

- **Leaf**: For atomic tasks that can be completed in one step
  - Supports **Agentic Loop**: execute_command → observe result → refine → mark_complete
  - You can execute multiple commands before calling mark_complete
  - Only call mark_complete when you are SATISFIED with the result AND your acceptance criteria are met
  - If you execute a command without mark_complete, you will get another turn
  - Should NOT have child nodes

**Iteration semantics**: Loop nodes have been removed. If you need to retry until a condition holds, set acceptance criteria and let the runtime's retry budget drive iteration on the same node — do not create a Loop type.

**Current node type**: %s
- If Normal and has no children yet: Consider decomposing into sub-tasks
- If Leaf: Execute the task directly using commands or mark_complete

## Execution Requirements (STRICT):

1. **Physical Persistence check**: If you see a file mentioned in a previous node's 'Result', **DO NOT** assume it exists physically. You MUST use 'ls' to verify its existence before attempting to 'cat' it.

2. **Persistence**: To save results for future steps beyond semantic memory, you **MUST** use 'execute_command' with 'write'.

3. **Decomposition**: If the current node is a Leaf node, process it now. If it requires multiple steps or complex logic, you SHOULD create child nodes first.

4. **Tool Use**: Use 'pwd' to see current directory (always /). Use 'ls' to explore.

5. **Variable Naming**:
   - Use descriptive names that include context (e.g., 'outer_loop_counter', 'file_processing_index')
   - Avoid generic names like 'i', 'temp', 'data' in nested structures

6. **Incremental File Writing**:
   - Use 'append_to_file' for building documents incrementally
   - Each node can append its own section without rewriting the entire file
   - This is more efficient and less error-prone than rewriting
   - Example: Building a report where each node adds a section

7. **CRITICAL: STRUCTURAL INTEGRITY & ERROR HANDLING**:
   - **JSON Format**: You MUST return a single valid JSON. Any extra text or markdown will cause system failure.
   - **Error Handling**: For risky operations (I/O, network, complex calculations), you MUST provide an `error_handler_node` in your `create_node` action.
   - **Completeness**: Every path in your tree MUST end with a `mark_complete` action.

8. **Acceptance-Driven Iteration**:
   - If a task fails or syntax errors occur, DO NOT repeat the same failing command.
   - Use 'update_variables' to record what was tried and why it failed before retrying with a different approach.
   - Iteration is bounded by the node's acceptance criteria + retry budget — there is no Loop node.

10. **Stagnation Defense**:
   - If you repeat the same observation command (e.g., 'cat results.txt') more than twice without creating a new node, marking a node complete, or updating variables, YOU ARE STAGNATED.
   - Break the cycle: Analyze why you are repeating yourself.

## Response Format Examples

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

**Example 4: Append to file (incremental write)**
```json
{
  "actions": [
    {
      "action_type": "append_to_file",
      "file_path": "/absolute/path/to/document.md",
      "content": "\n### A Midsummer Night's Dream\n\n**Written**: 1595-1596\n"
    }
  ]
}
```

**Use append_to_file when**:
- Building documents incrementally (reports, logs, analysis)
- Writing content with special characters (quotes, apostrophes) - NO shell escaping needed!
- Avoiding rewriting entire files (saves tokens and prevents errors)

**Critical**:
- All string values must be in double quotes
- No trailing commas
- action_type must be exact (case-sensitive)
- node.type must be exactly: Normal or Leaf
- **IMPORTANT**: Prefer append_to_file over echo commands to avoid shell escaping issues!
