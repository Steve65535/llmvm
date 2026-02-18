# LLMVM Agentic Loop Design: Self-Correcting Leaf Nodes

## 1. Problem Statement
In the current LLMVM architecture, Leaf nodes execute a single action and then immediately return control to the parent node (`MoveUp`). This "one-shot" behavior prevents the Agent from:
1.  **Self-Correction**: Fixing a command that failed (e.g., `apt-get` failed, try `apk add`).
2.  **Multi-Step Reasoning**: Performing a "Think -> Act -> Observe -> Refine" loop within a single atomic task.
3.  **Robustness**: A single failure flushes the entire context up to the parent, potentially failing the whole task or requiring expensive global retries.

## 2. Core Solution: The Agentic Loop
We introduce an **internal loop** within Leaf nodes. The runtime will **NOT** move up from a Leaf node until the node explicitly signals that it is "Done" via a legitimate `mark_complete` action.

If a Leaf node executes a command (e.g., `execute_command`) but does **not** call `mark_complete`, the runtime interprets this as "I am still working on this task" and schedules another execution step for the **same node**.

### 2.1 The "SingleFinished" Concept
To implement this safely without breaking the global DFS recursion, we distinguish between two states:

*   **`SingleFinished` (Local State)**: The LLM has signaled that it believes the current step is done.
*   **`WetherFinished` (Global State)**: The Runtime confirms the node is visibly complete and ready to be archived in the traversal history.

For Leaf nodes:
- `mark_complete` action -> sets `SingleFinished = true`.
- Runtime checks `SingleFinished`.
    - If `true` -> Sets `WetherFinished = true`, moves Cursor UP.
    - If `false` -> Resets `WetherTraveled = false` (or keeps it active), stays on current node.

## 3. Detailed Architecture

### 3.1 TaskNode Structure Update
```go
type TaskNode struct {
    // ... existing fields ...
    SingleFinished bool // New: Signals local completion of the reasoning loop
}
```

### 3.2 Runtime Traversal Logic (`decideNextStep`)
The `decideNextStep` function in `pkg/runtime/runtime.go` governs the DFS flow. We modify the Leaf node handler:

```go
// Current Logic (Simplified)
if current.Type == tasknode.Leaf {
    r.cursor.MoveUp()
    return nil
}

// New Logic (Agentic Loop)
if current.Type == tasknode.Leaf {
    if current.SingleFinished {
        // The LLM explicitly said "I'm done"
        current.MarkFinished() // Sets WetherFinished = true
        r.cursor.MoveUp()
    } else {
        // The LLM executed an action (e.g., cmd) but didn't say "done".
        // It wants to continue/retry.
        
        // Safety Check: Max Iterations
        if current.RetryCount >= current.MaxRetries {
             current.MarkFinished() // Forced finish (or fail)
             r.cursor.MoveUp()
        } else {
             // Stay on this node for the next tick
             current.WetherTraveled = false // Re-enable for execution
             current.RetryCount++
        }
    }
    return nil
}
```

### 3.3 Action Handling (`executeAction`)
The `mark_complete` action needs to be updated to trigger this new state:

```go
case "mark_complete":
    // OLD: parent.MarkFinished()
    // NEW:
    parent.SingleFinished = true
    // We do NOT call MarkFinished() here. We let decideNextStep handle the transition.
    return nil
```

## 4. Prompt Engineering Adjustments
The System Prompt needs to explicitly inform the LLM of this capability:

> **Agentic Loop**: If you execute a command (like `ls` or `go test`) and do NOT call `mark_complete`, the system will show you the result and let you continue in the next step. active node. Use this to verify your work!
> Only call `mark_complete` when you are satisfied with the result.

## 5. Example Flow

### Task: "Find the file `data.txt` and print it"

**Turn 1:**
- **LLM**: Calls `execute_command("find . -name data.txt")`
- **Action**: Runtime runs `find`. Result: `./foo/bar/data.txt`
- **State**: `SingleFinished = false`.
- **Runtime**: Stays on node. Feeds result back to LLM.

**Turn 2:**
- **LLM**: Sees path. Calls `execute_command("cat ./foo/bar/data.txt")`
- **Action**: Runtime runs `cat`. Result: "Hello World"
- **State**: `SingleFinished = false`.
- **Runtime**: Stays on node.

**Turn 3:**
- **LLM**: "Okay, I have the content." Calls `mark_complete(result="Hello World")`
- **Action**: Sets `SingleFinished = true`.
- **Runtime**: Sets `WetherFinished = true`. Moves Cursor UP to parent.

## 6. Safety and Limits
To prevent infinite loops (e.g., LLM forgetting to call mark_complete), we strictly enforce `MaxRetries` (default: 5 or 9). If the limit is reached, the node is forcibly marked finished (possibly with a warning/error status).
