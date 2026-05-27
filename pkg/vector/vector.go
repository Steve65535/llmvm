// Package vector 提供节点 / artifact 的向量索引能力。
//
// 设计目标（按文档 2）：
//   - 完全本地：默认嵌入器为纯 Go 实现，无外部依赖
//   - 可插拔：Embedder 是接口，可替换为 Ollama / OpenAI / 任何返回 []float32 的实现
//   - 与 SQLite 互补：SQLite 是事实账本，vector 是语义入口；vector 无法访问时系统照常运行
//
// 默认 embedder 用 char-trigram + feature hashing：把文本切成 3-gram 字符序列，
// 哈希到固定维度的向量空间，最后归一化。这不是高质量语义嵌入，但满足两个关键属性：
// (1) 同义/近义文本得分明显高于不相关文本；(2) 完全确定性，便于测试。
//
// 真正的语义嵌入可通过 LLMVM_EMBEDDER=ollama 切换（要求本地 ollama 服务）。
package vector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/philippgille/chromem-go"
)

// Embedder 把文本嵌入为定长向量。
//
// 实现必须保证幂等：相同输入 + 相同实例返回相同向量（测试 / rerank 复现性需要）。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dim() int
	Name() string
}

// HashingEmbedder: char-trigram + feature hashing，纯 Go 零依赖。
//
// 用 dim=384 与 all-MiniLM-L6-v2 对齐，方便后续替换。
type HashingEmbedder struct {
	dim int
}

func NewHashingEmbedder(dim int) *HashingEmbedder {
	if dim <= 0 {
		dim = 384
	}
	return &HashingEmbedder{dim: dim}
}

func (e *HashingEmbedder) Dim() int     { return e.dim }
func (e *HashingEmbedder) Name() string { return "hashing-trigram" }

func (e *HashingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, e.dim)
	if text == "" {
		return vec, nil
	}
	lower := strings.ToLower(text)
	tokens := tokenize(lower)
	for _, tok := range tokens {
		// 词级特征
		bumpHash(vec, "w:"+tok, +1)
		// 字符 trigram
		runes := []rune(tok)
		if len(runes) < 3 {
			bumpHash(vec, "w:"+tok, +1)
			continue
		}
		for i := 0; i+3 <= len(runes); i++ {
			bumpHash(vec, "g:"+string(runes[i:i+3]), +1)
		}
	}
	// L2 归一化（cosine 相似度需要）
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec, nil
	}
	scale := float32(1.0 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec, nil
}

func bumpHash(vec []float32, key string, delta float32) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	idx := int(h.Sum64() % uint64(len(vec)))
	// 用次低位作符号位，保持稀疏正负分布
	if (h.Sum64()>>1)&1 == 1 {
		vec[idx] += delta
	} else {
		vec[idx] -= delta
	}
}

func tokenize(s string) []string {
	var tokens []string
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		default:
			if b.Len() > 0 {
				tokens = append(tokens, b.String())
				b.Reset()
			}
		}
	}
	if b.Len() > 0 {
		tokens = append(tokens, b.String())
	}
	return tokens
}

// OllamaEmbedder: 调用本地 ollama /api/embeddings。
//
// 需要 ollama 服务在 LLMVM_OLLAMA_URL（默认 http://localhost:11434）运行，
// 且模型 LLMVM_EMBEDDER_MODEL（默认 nomic-embed-text）已 pull。
type OllamaEmbedder struct {
	url   string
	model string
	dim   int
	cli   *http.Client
}

func NewOllamaEmbedder() *OllamaEmbedder {
	url := os.Getenv("LLMVM_OLLAMA_URL")
	if url == "" {
		url = "http://localhost:11434"
	}
	model := os.Getenv("LLMVM_EMBEDDER_MODEL")
	if model == "" {
		model = "nomic-embed-text"
	}
	return &OllamaEmbedder{
		url:   url,
		model: model,
		dim:   768, // nomic-embed-text 默认；首次 Embed 会校正
		cli:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *OllamaEmbedder) Dim() int     { return e.dim }
func (e *OllamaEmbedder) Name() string { return "ollama:" + e.model }

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]string{"model": e.model, "prompt": text})
	req, err := http.NewRequestWithContext(ctx, "POST", e.url+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Embedding []float32 `json:"embedding"`
		Error     string    `json:"error,omitempty"`
	}
	dec := json.NewDecoder(bufio.NewReader(resp.Body))
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama embed decode: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama embed error: %s", out.Error)
	}
	if len(out.Embedding) > 0 {
		e.dim = len(out.Embedding)
	}
	return out.Embedding, nil
}

// SelectEmbedder 按 LLMVM_EMBEDDER 选择实现。默认 hashing。
func SelectEmbedder() Embedder {
	switch strings.ToLower(os.Getenv("LLMVM_EMBEDDER")) {
	case "ollama":
		return NewOllamaEmbedder()
	default:
		return NewHashingEmbedder(384)
	}
}

// Store 是 chromem-go 之上的薄包装：对应 LLMVM 的"vector index of artifacts"。
//
// 持久化路径默认在 .chromem/llmvm.gob，可通过 LLMVM_VECTOR_DIR 覆盖。
// 一个 LLMVM 进程对应一个 collection（"artifacts"）。
type Store struct {
	db       *chromem.DB
	col      *chromem.Collection
	embedder Embedder
}

const collectionName = "artifacts"

// New 创建或加载 vector store。
// 失败时返回错误；调用方可选择降级（continueWithoutVector）。
func New(persistDir string) (*Store, error) {
	if persistDir == "" {
		persistDir = ".chromem"
	}
	emb := SelectEmbedder()
	embedFn := func(ctx context.Context, text string) ([]float32, error) {
		return emb.Embed(ctx, text)
	}
	db, err := chromem.NewPersistentDB(persistDir, false)
	if err != nil {
		return nil, fmt.Errorf("vector: open chromem at %s: %w", persistDir, err)
	}
	col, err := db.GetOrCreateCollection(collectionName, nil, embedFn)
	if err != nil {
		return nil, fmt.Errorf("vector: get_or_create collection: %w", err)
	}
	return &Store{db: db, col: col, embedder: emb}, nil
}

// EmbedderName 暴露当前嵌入器名（调试 / metrics 用）。
func (s *Store) EmbedderName() string { return s.embedder.Name() }

// Upsert 写入一条 artifact。如果同 ID 已存在，会被替换。
//
// metadata 用于 chromem 的 where filter。LLMVM 至少落 producer_node_id / scope / name / kind。
func (s *Store) Upsert(ctx context.Context, id, content string, metadata map[string]string) error {
	if s == nil {
		return nil
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	doc := chromem.Document{
		ID:       id,
		Content:  content,
		Metadata: metadata,
	}
	return s.col.AddDocuments(ctx, []chromem.Document{doc}, 1)
}

// Delete 按 ID 删除一条 artifact。
func (s *Store) Delete(ctx context.Context, id string) error {
	if s == nil {
		return nil
	}
	return s.col.Delete(ctx, nil, nil, id)
}

// QueryResult 单条召回结果。
type QueryResult struct {
	ID         string
	Content    string
	Score      float32 // chromem 返回的 similarity（越大越相关）
	Metadata   map[string]string
}

// Query 按文本召回 top-k 条。where 是 chromem 的元数据过滤（精确匹配）。
func (s *Store) Query(ctx context.Context, text string, k int, where map[string]string) ([]QueryResult, error) {
	if s == nil || s.col == nil {
		return nil, nil
	}
	if k <= 0 {
		k = 10
	}
	count := s.col.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}
	res, err := s.col.Query(ctx, text, k, where, nil)
	if err != nil {
		return nil, err
	}
	out := make([]QueryResult, 0, len(res))
	for _, r := range res {
		out = append(out, QueryResult{
			ID:       r.ID,
			Content:  r.Content,
			Score:    r.Similarity,
			Metadata: r.Metadata,
		})
	}
	return out, nil
}

// Count 当前 collection 中的 artifact 数。
func (s *Store) Count() int {
	if s == nil || s.col == nil {
		return 0
	}
	return s.col.Count()
}
