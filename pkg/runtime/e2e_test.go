package runtime

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Steve65535/llmvm/pkg/llm"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// scriptedEngine 是一个按 prompt 计数返回预设响应序列的 LLM 替身。
//
// 与 StubEngine 不同：scriptedEngine 不会无限制创建子节点，每一个回合输出
// 一组测试预先准备好的 actions，跑完所有脚本后该节点应该 mark_complete。
type scriptedEngine struct {
	mu       sync.Mutex
	scripts  []string
	idx      int
	captured []string
}

func (s *scriptedEngine) Call(prompt string) (*llm.Output, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured = append(s.captured, prompt)
	if s.idx >= len(s.scripts) {
		return nil, errors.New("scripted engine: ran out of scripted responses")
	}
	out := s.scripts[s.idx]
	s.idx++
	return &llm.Output{Response: out}, nil
}

func (s *scriptedEngine) CallAsync(prompt string) <-chan *llm.Output {
	ch := make(chan *llm.Output, 1)
	go func() {
		o, _ := s.Call(prompt)
		ch <- o
		close(ch)
	}()
	return ch
}

// TestEndToEndAddArtifactAndAcceptance 端到端验证：
//
//	create_node + acceptance_criteria（manual）→ 子节点 add_artifact + mark_complete
//	→ 父节点 mark_complete → SQLite 索引落库 → 向量库异步入索引 → SaveState 可序列化。
func TestEndToEndAddArtifactAndAcceptance(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smoke.sqlite")

	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"verify add_artifact end-to-end"})
	root.SetStatus(tasknode.Running)

	engine := &scriptedEngine{scripts: []string{
		// Turn 1: root 创建一个带 acceptance 的 Leaf 子节点
		`{"actions":[{
			"action_type":"create_node",
			"node":{
				"id":"leaf1","name":"Probe","type":"Leaf","information":"work",
				"acceptance_criteria":[
					{"description":"artifact recorded","required":true,"check_type":"manual"}
				]
			}
		}]}`,
		// Turn 2: 子节点 add_artifact + mark_complete (含 acceptance_results)
		`{"actions":[
			{"action_type":"add_artifact",
			 "artifact_name":"probe_evidence",
			 "artifact_type":"evidence",
			 "scope":"node",
			 "tags":["probe","e2e"],
			 "granularity":"case-level",
			 "importance":"high",
			 "summary":"e2e probe captured 3 invariants",
			 "content":"line A\nline B\nline C\n"
			},
			{"action_type":"mark_complete",
			 "goal":"probe and record","summary":"probe done",
			 "key_facts":["captured 3 lines"],
			 "artifact_refs":[],
			 "acceptance_results":[
				{"criterion_id":"leaf1_ac_1","passed":true,
				 "notes":"manual check satisfied"}
			 ]}
		]}`,
		// Turn 3: 回到 root 完成
		`{"actions":[{"action_type":"mark_complete","summary":"all good","key_facts":["e2e green"]}]}`,
	}}

	rt := NewRuntime(engine, root, dbPath)

	if err := rt.Execute(root.Information[0]); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// === 1. 节点状态 ===
	if !root.WetherFinished {
		t.Errorf("root should be finished, status=%v finished=%v", root.Status, root.WetherFinished)
	}
	if len(root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(root.Children))
	}
	leaf := root.Children[0]
	if !leaf.SingleFinished {
		t.Errorf("leaf should have SingleFinished=true (mark_complete called)")
	}
	if len(leaf.AcceptanceCriteria) != 1 || leaf.AcceptanceCriteria[0].ID != "leaf1_ac_1" {
		t.Errorf("expected single criterion with id=leaf1_ac_1, got %+v", leaf.AcceptanceCriteria)
	}
	if len(leaf.AcceptanceResults) != 1 || !leaf.AcceptanceResults[0].Passed {
		t.Errorf("expected one passed acceptance_result, got %+v", leaf.AcceptanceResults)
	}

	// === 2. Artifact store ===
	arts := rt.GetArtifacts().ListAll()
	var probe *struct{ ID, Name string }
	for _, a := range arts {
		if a.Name == "probe_evidence" {
			probe = &struct{ ID, Name string }{a.ID, a.Name}
			break
		}
	}
	if probe == nil {
		t.Fatalf("expected probe_evidence artifact in store, got %d artifacts", len(arts))
	}

	// === 3. SQLite 索引 ===
	briefs, err := rt.memStore.QueryArtifactsByNode(leaf.ID)
	if err != nil {
		t.Fatalf("QueryArtifactsByNode: %v", err)
	}
	if len(briefs) == 0 {
		t.Errorf("expected artifact in SQLite for leaf %s", leaf.ID)
	}
	hits, err := rt.memStore.SearchArtifactsFTS("probe", 5)
	if err != nil {
		t.Fatalf("FTS: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("expected FTS to find probe artifact")
	}
	results, err := rt.memStore.QueryAcceptanceResults(leaf.ID)
	if err != nil {
		t.Fatalf("acceptance results: %v", err)
	}
	if len(results) == 0 || !results[0].Passed {
		t.Errorf("expected passed acceptance_result in SQLite, got %+v", results)
	}

	// === 4. 向量库（等异步入索引完成）===
	rt.WaitForBackgroundIndexing()
	if rt.vecStore != nil {
		// 至少 probe_evidence 已被 embed
		if rt.vecStore.Count() == 0 {
			t.Errorf("vector store should have indexed at least one artifact")
		}
	}

	// === 5. SaveState 可序列化 ===
	type SaveState struct {
		Root      *tasknode.TaskNode `json:"root"`
		Artifacts interface{}        `json:"artifacts,omitempty"`
	}
	state := SaveState{Root: root, Artifacts: rt.GetArtifacts()}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal save state: %v", err)
	}
	if !strings.Contains(string(data), "probe_evidence") {
		t.Errorf("save state should contain probe_evidence name")
	}
}

// TestEndToEndMarkCompleteRejectedWithoutAcceptance 验证 acceptance 拒绝路径：
// Required 标准未提供 acceptance_result 时，mark_complete 应该被拒。
func TestEndToEndMarkCompleteRejectedWithoutAcceptance(t *testing.T) {
	root := tasknode.NewTaskNode("root", "Root", tasknode.Normal, []string{"goal"})
	root.SetStatus(tasknode.Running)

	engine := &scriptedEngine{scripts: []string{
		`{"actions":[{
			"action_type":"create_node",
			"node":{"id":"l1","name":"L","type":"Leaf","information":"work",
				"acceptance_criteria":[
					{"description":"required check","required":true,"check_type":"manual"}
				]}
		}]}`,
		// 故意 mark_complete 不带 acceptance_results
		`{"actions":[{"action_type":"mark_complete","summary":"trying"}]}`,
		// 这次提供 acceptance_results，应该通过
		`{"actions":[{
			"action_type":"mark_complete","summary":"now properly",
			"acceptance_results":[
				{"criterion_id":"l1_ac_1","passed":true,"notes":"satisfied"}
			]
		}]}`,
		// 父节点 mark_complete
		`{"actions":[{"action_type":"mark_complete","summary":"done"}]}`,
	}}

	rt := NewRuntime(engine, root)
	if err := rt.Execute(root.Information[0]); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	leaf := root.Children[0]
	if !leaf.SingleFinished {
		t.Errorf("leaf should eventually be finished after second attempt")
	}
	if len(leaf.AcceptanceResults) != 1 {
		t.Errorf("expected exactly one accepted result, got %d", len(leaf.AcceptanceResults))
	}
}
