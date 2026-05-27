package runtime

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Steve65535/llmvm/pkg/artifact"
	"github.com/Steve65535/llmvm/pkg/memory"
	"github.com/Steve65535/llmvm/pkg/tasknode"
)

// IndexArtifactAsync 后台 embedding + 写 vector store。
//
// 调用方持有 art 的快照（避免后台读 LRU 已淘汰的内容）。
// 在 mark_complete 完成 handoff 后，runtime 把节点产出的 artifact 批量异步入索引。
// 当前 artifact 已在 SQLite + FTS 同步落库；vector 索引是异步补充。
func (r *Runtime) IndexArtifactAsync(art *artifact.Artifact, content string) {
	if r.vecStore == nil || art == nil {
		return
	}
	r.embedWG.Add(1)
	go func(id, body string, meta map[string]string) {
		defer r.embedWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := r.vecStore.Upsert(ctx, id, body, meta); err != nil {
			fmt.Printf("  ⚠️  vector upsert %s failed: %v\n", id, err)
		}
	}(art.ID, content, artifactVectorMeta(art))
}

// artifactVectorMeta 提取 artifact 元数据用于 chromem where 过滤。
func artifactVectorMeta(art *artifact.Artifact) map[string]string {
	meta := map[string]string{
		"producer": art.CreatedBy,
		"kind":     art.Type,
		"scope":    string(art.Scope),
	}
	if art.Name != "" {
		meta["name"] = art.Name
	}
	if art.Granularity != "" {
		meta["granularity"] = art.Granularity
	}
	if art.Importance != "" {
		meta["importance"] = art.Importance
	}
	return meta
}

// IndexNodeArtifactsAsync 在 mark_complete 触发 handoff 后调用：
// 把当前节点产出的所有 artifact 批量异步 embed + 入向量库。
//
// 工作池上限 2，避免一次性把外部 embedding 服务（ollama）打满。
// SQLite + FTS 已同步写入，这里只补 vector。
func (r *Runtime) IndexNodeArtifactsAsync(node *tasknode.TaskNode) {
	if r.vecStore == nil || node == nil {
		return
	}
	arts := r.artifacts.ListByNode(node.ID)
	if len(arts) == 0 {
		return
	}
	const concurrency = 2
	sem := make(chan struct{}, concurrency)
	for _, art := range arts {
		if art.Evicted {
			continue
		}
		body := art.Content
		if body == "" && art.SpillPath != "" {
			if data, err := os.ReadFile(art.SpillPath); err == nil {
				body = string(data)
			}
		}
		if body == "" {
			body = art.Summary
		}
		r.embedWG.Add(1)
		sem <- struct{}{}
		go func(id, content string, meta map[string]string) {
			defer r.embedWG.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := r.vecStore.Upsert(ctx, id, content, meta); err != nil {
				fmt.Printf("  ⚠️  vector upsert %s failed: %v\n", id, err)
			}
		}(art.ID, body, artifactVectorMeta(art))
	}
}

// WaitForBackgroundIndexing 等待所有后台 embed 完成（测试 / shutdown 用）。
func (r *Runtime) WaitForBackgroundIndexing() {
	r.embedWG.Wait()
}

// syncNodeToMemory 将节点状态 + 已完成节点的 handoff 同步到 SQLite index。
//
// AST 仍是控制流权威源；SQLite 是检索镜像，必须每次 mutation 后同步以保证 query_memory 一致。
func (r *Runtime) syncNodeToMemory(node *tasknode.TaskNode) {
	if r.memStore == nil {
		return
	}
	parentID := ""
	depth := 0
	n := node
	for n.Parent != nil {
		depth++
		n = n.Parent
	}
	if node.Parent != nil {
		parentID = node.Parent.ID
	}
	info := ""
	if len(node.Information) > 0 {
		info = node.Information[0]
	}
	_ = r.memStore.UpsertNode(
		node.ID, parentID, node.Name,
		nodeTypeStr(node.Type), nodeStatusStr(node.Status),
		info, depth, node.Index,
	)
	if node.WetherFinished || node.SingleFinished {
		confidence := node.Confidence
		if confidence == "" && node.WetherFinished {
			confidence = "auto_generated"
		}
		_ = r.memStore.UpsertHandoff(
			node.ID, node.Goal, node.Summary,
			node.KeyFacts, node.Decisions, node.Assumptions,
			node.ArtifactRefs, node.Outputs, node.OpenQuestions,
			node.Handoff, confidence,
		)
	}
}

// RebuildIndexFromAST 从当前 AST + artifact store 完整重建 SQLite 索引。
//
// 在 --load 恢复状态后调用，防止 .sqlite 丢失或过期导致 query_memory 查不到数据。
func (r *Runtime) RebuildIndexFromAST() error {
	if r.memStore == nil {
		return nil
	}
	root := r.cursor.GetRoot()
	if root == nil {
		return nil
	}

	var nodes []memory.RebuildNode
	root.Traverse(func(n *tasknode.TaskNode) {
		parentID := ""
		depth := 0
		cur := n
		for cur.Parent != nil {
			depth++
			cur = cur.Parent
		}
		if n.Parent != nil {
			parentID = n.Parent.ID
		}
		info := ""
		if len(n.Information) > 0 {
			info = n.Information[0]
		}
		hasHandoff := n.WetherFinished || n.SingleFinished
		confidence := n.Confidence
		if confidence == "" && hasHandoff {
			confidence = "auto_generated"
		}
		nodes = append(nodes, memory.RebuildNode{
			ID:             n.ID,
			ParentID:       parentID,
			Name:           n.Name,
			Type:           nodeTypeStr(n.Type),
			Status:         nodeStatusStr(n.Status),
			Information:    info,
			Depth:          depth,
			TraversalIndex: n.Index,
			Goal:           n.Goal,
			Summary:        n.Summary,
			KeyFacts:       n.KeyFacts,
			Decisions:      n.Decisions,
			Assumptions:    n.Assumptions,
			ArtifactRefs:   n.ArtifactRefs,
			Outputs:        n.Outputs,
			OpenQuestions:  n.OpenQuestions,
			Handoff:        n.Handoff,
			Confidence:     confidence,
			HasHandoff:     hasHandoff,
		})
	})

	var arts []memory.RebuildArtifact
	for _, a := range r.artifacts.ListAll() {
		arts = append(arts, memory.RebuildArtifact{
			ID:             a.ID,
			ProducerNodeID: a.CreatedBy,
			Kind:           a.Type,
			Title:          a.Source,
			Summary:        a.Summary,
			ContentRef:     a.SpillPath,
			Pinned:         a.Pinned,
			Name:           a.Name,
			Scope:          string(a.Scope),
			Tags:           a.Tags,
			Granularity:    a.Granularity,
			Importance:     a.Importance,
			Version:        a.Version,
			SupersededBy:   a.SupersededBy,
			Supersedes:     a.Supersedes,
			TokenCount:     a.TokenCount,
		})
	}

	return r.memStore.Rebuild(memory.RebuildInput{Nodes: nodes, Artifacts: arts})
}
