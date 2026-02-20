Overview
反思机制是 LLMVM 中负责⾃我修正的核⼼组件。它不是外挂的独⽴模块，⽽是通过 Loop 节点
的语义⾃然表达⸺
“条件不满⾜就继续”本身就是反思的结构。
Flag Type 含义
wethertraveled boolean 该节点是否已被遍历执⾏
wetherfinished boolean 该节点（或⼦树）是否已完成
wethersinglefinish boolean 单个叶节点是否完成（仅叶节点使⽤）
节点类型与执⾏模型
叶节点 (Leaf Node)
采⽤简单的 Agentic Loop 执⾏
通过 wethersinglefinish 标记⾃身完成状态
是任务执⾏的最⼩原⼦单元
粒度设计原则：⼤⼩适配 LLM 最佳上下⽂窗⼝
Loop 节点
承载反思语义的核⼼节点
循环退出条件：所有叶节点 wetherfinished 的 AND 操作为 true
反思触发、⼦树归零、重新执⾏均在此节点管理
分⽀节点
普通控制流节点
⽀持 DFS 执⾏顺序
完成判断逻辑
循环退出条件 = AND(所有叶节点.wetherfinished)
任意⼀个叶节点未完成 → 整体 wetherfinished = false → Loop 节点继续循环。
叶节点之间完全并⾏独⽴，通过全树变量扫描（⽽⾮直接通信）实现状态共享，天然避免依赖死
锁。
反思触发条件
if (node.wethertraveled == false) AND (node.variables != empty):
→ 触发反思
语义解读：节点被重置（wethertraveled=false ）但保留了历史变量，说明这是⼀次有历史记
录的重新执⾏，需要反思上⼀次哪⾥做错了。
主要是loop节点执行反思 因为loop节点本身就是循环 
反思判定条件 
if (node.wethertraveled == false) AND (node.variables != empty):
反思执⾏流程
1. 触发条件满⾜
2. 全树扫描 → 识别有⽤的历史节点和变量
3. 加载选中变量⾄当前执⾏上下⽂（瞬时态 RAM）
4. LLM 基于历史变量进⾏反思分析
5. 输出：直接修改 AST 节点
6. 重新执⾏修改后的节点/⼦树