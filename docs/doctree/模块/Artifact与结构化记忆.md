# Artifact 与结构化记忆

上级：[[../README|项目总览]]

## 职责

Artifact store 和 SQLite memory 是 LLMVM 的外置记忆系统。artifact 保存大内容和工具结果；memory 保存可查询索引。两者共同减少 prompt 中的大文本复制。

子页面：

- [[状态与记忆/ArtifactStore|Artifact Store]]
- [[状态与记忆/SQLiteMemory|SQLite Memory]]

## 关系

```text
Runtime action output
  -> artifact.Store.Add(...)
  -> artifact metadata / summary
  -> memory.Store.UpsertArtifact(...)
  -> artifact_fts index
  -> prompt 中只出现 artifact brief
  -> LLM 使用 read_artifact 或 query_memory 按需读取
```

AST 仍是执行权威；memory 可以从 AST + artifact store 重建。

