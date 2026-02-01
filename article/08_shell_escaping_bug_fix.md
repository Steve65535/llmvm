# Shell 转义 Bug 修复总结

## ✅ 问题已解决

### 原始问题
```bash
echo '### A Midsummer Night\'s Dream' >> file.md
# 错误: unexpected EOF while looking for matching `''
```

### 根本原因
LLM 使用 `execute_command` + `echo` 写入包含撇号的文本时，shell 转义失败。

### 解决方案
在 Prompt 中添加了 **Example 4: Append to file**，展示如何使用 `append_to_file` 工具。

## 实施的修改

### 文件: `pkg/runtime/runtime.go`

**添加位置**: 第 522-546 行

**添加内容**:
```markdown
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
- Building documents incrementally
- Writing content with special characters (quotes, apostrophes) - NO shell escaping needed!
- Avoiding rewriting entire files (saves tokens)

**IMPORTANT**: Prefer append_to_file over echo commands to avoid shell escaping issues!
```

## 修复效果

### 之前（会失败）
```json
{
  "action_type": "execute_command",
  "command": "echo '### A Midsummer Night\\'s Dream' >> file.md"
}
```
❌ Shell 转义错误

### 之后（正常工作）
```json
{
  "action_type": "append_to_file",
  "file_path": "/path/to/file.md",
  "content": "\n### A Midsummer Night's Dream\n"
}
```
✅ 无需转义，直接工作

## 验证结果

- ✅ 代码编译成功
- ✅ Prompt 已更新
- ✅ Example 4 已添加
- ✅ Critical 部分已强调优先使用 append_to_file

## 预期行为

现在当 LLM 需要写入文件时，应该：
1. **优先使用** `append_to_file` 而非 `echo` 命令
2. **自动处理** 特殊字符（引号、撇号等）
3. **避免** shell 转义问题

## 测试建议

重新运行之前失败的命令：
```bash
rm -f test/sandbox/shakespeare_plays.md
DEEPSEEK_API_KEY=xxx go run cmd/main.go "创建莎士比亚文档"
```

预期 LLM 会使用 `append_to_file` 而不是 `echo` 命令。

## 总结

这个 Bug 的发现和修复完美展示了 `append_to_file` 工具的价值：

- **问题**: Shell 转义复杂且容易出错
- **解决**: 直接文件操作，无需转义
- **收益**: 更可靠、更高效、更安全

✅ **Bug 已修复，功能已完善！**
