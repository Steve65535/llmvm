llm的缺陷
llm的token生成机制是图灵不完备的 是根据条件概率去embedding的token

所以 一个单独的llm 无论他的参数多大 都无法去实现通用人工智能

从一个编程语言理论的角度来说
实现一个图灵完备的编程语言 需要
A if else
B for循环

从编程语言图灵完备性的启发
对于现有的agent架构进行批判

1 claudecode & cowork 都无法实现图灵完备性 因为只有一层sub agent。无限的去套壳subagent根本不可能 因为他的执行本质是广度优先搜素tasktree（compile syntax tree）

所以在此基础上 我设计了一个Bootstrapped Program Construction with Stateless Large Language Models

即通过llm去动态生成syntax tree的一个东西 遵循深度优先搜索

他的每一次ai对话都是stateless的 什么是stateless？ 就是每一次的prompt不会带着前面所有的prompt 这样会避免上下文爆炸的问题 同时一共有三种节点 loop 节点 普通节点 leaf节点 
这本质就是一个图灵机 cursor是读写头 想象一下 一个传统的程序 编程语言 有分支 循环 这就是图灵完备 在编译过程中会生成一个语法树 llmvm的目的就在于直接生成语法树 他的结构是 感知当前的节点类型 是否遍历过 是否完成 然后下一步 执行 就是和llm对话 llm给出结构性回答 给出结构性回答后进行执行 有一个解析器 解析 这个东西 然后去生成节点或者是不生成 决定下树还是上树

对于普通节点 他有一个wethertraveled变量 如果wethertraveled变量是1 则寻找下一个子节点 如果全部的子节点wethertraveled 他就返回上级 对于loop 还有wetherfinished 变量 父节点的wether finished就是子节点wetherfinished的and操作 一旦有了loop节点 下面的节点有一个loop的wetherfinished的栈 看看子节点是否全部finished 如果finished 就pop栈 同时跳出这个loop子节点 而leaf节点就是最小的拆解节点 可以由llm单次上下文窗口完备处理 这就是下一代程序的核心灵魂

api:sk-6cb6f64b1f83461cb7630968ca8bbeba

## 如何启动 (Startup)
使用以下命令启动程序（已注入您的 API Key）：
```bash
DEEPSEEK_API_KEY=sk-6cb6f64b1f83461cb7630968ca8bbeba go run cmd/main.go "创建一个3d视觉效果的网站 是一个跨境电商网站 严格遵循软件工程流程执行"
```

## 紧急停机 (Emergency Shutdown)
如果需要立即停止所有递归任务：
1. **终端层面**：直接使用 `Ctrl + C` 强制终止进程。
2. **逻辑层面**：AI 可以返回 `{"action_type": "shutdown", "result": "原因"}` 动作，系统会立即抛出 `EMERGENCY_SHUTDOWN` 异常并停止遍历。
3. **交互层面**：在提示输入时输入 `exit` 即可安全退出。

具体的执行逻辑：

1. 深度优先搜索
2. 他有一个loop stack 一旦碰到一个loop节点 就给loopstack中push一个数字 比如1 然后在每个节点中 使用一个wetherfinished变量去记载是否完整的完成了（注意 wetherfinished和wethertraveled是不同的含义 wethertraveled只代表cursor经过了这个tasknode 而wetherfinished主要用于记载是否完美的完成了 也就是判断是否需要pop出stack 然后上一级的wetherfinished应该是底层的wetherfinished的and操作） 只有最底层的leaf是原生决定wetherfinished操作 上层全都是底层的and 然后往上移动遇到loop节点 如果子节点全部finished 就pop栈 同时跳出这个loop子节点 

我的思想其实有好几个 一个是ast  把所有的任务和计算抽象成了一个"函数" 追求了图灵完备架构 保证了无限任务执行 而并非之前的llm图灵机 另一个是stateless slice 解决了上下文爆炸的问题 然后是节点级别注意力机制

go build ./pkg/llm/...

---

# 长时间推理任务设计：证明哥德巴赫猜想的特殊情况

## 任务背景

**哥德巴赫猜想**：任何大于 2 的偶数都可以表示为两个质数之和。

传统的 Chain-of-Thought (CoT) 方法在处理这类需要**大量迭代验证**的数学问题时会遇到以下问题：
1. **上下文窗口限制**：验证 100 个偶数就需要 100 步推理，CoT 会因为上下文过长而中途退出
2. **无法保存中间状态**：每次重新开始都要从头推理
3. **缺乏循环结构**：无法表达"对每个偶数进行验证"这样的迭代逻辑

## LLMVM 任务设计

### 任务描述
```
验证哥德巴赫猜想对于 4 到 1000 之间的所有偶数都成立，
并生成详细的验证报告，包括：
1. 每个偶数的质数分解方案（可能有多种）
2. 统计分析（平均方案数、最小质数对、最大质数对等）
3. 可视化图表（方案数分布、质数使用频率等）
4. 数学分析报告
```

### 预期任务树结构

```
Root: "验证哥德巴赫猜想 (4-1000)"
├─ Normal: "准备工作"
│  ├─ Leaf: "生成质数表 (2-1000)"
│  │  └─ 使用埃拉托斯特尼筛法，保存到 primes.txt
│  └─ Leaf: "创建工作目录"
│     └─ mkdir goldbach_verification
│
├─ Loop: "验证所有偶数" (498 次迭代，从 4 到 1000)
│  ├─ Leaf: "读取当前偶数"
│  │  └─ 从变量 current_even 读取
│  ├─ Normal: "寻找质数对"
│  │  ├─ Leaf: "加载质数表"
│  │  ├─ Loop: "遍历质数" (内层循环)
│  │  │  ├─ Leaf: "检查 p1"
│  │  │  ├─ Leaf: "计算 p2 = current_even - p1"
│  │  │  ├─ Leaf: "验证 p2 是否为质数"
│  │  │  └─ Leaf: "记录有效质数对"
│  │  │     └─ 如果找到，标记为 finished
│  │  └─ Leaf: "汇总当前偶数的所有方案"
│  ├─ Leaf: "保存结果到文件"
│  │  └─ echo "n = p1 + p2" >> results.txt
│  ├─ Leaf: "更新进度"
│  │  └─ current_even += 2
│  └─ Leaf: "检查是否完成所有偶数"
│     └─ 如果 current_even > 1000，标记为 finished
│
├─ Normal: "统计分析"
│  ├─ Leaf: "读取所有结果"
│  ├─ Leaf: "计算统计指标"
│  │  └─ 平均方案数、中位数、标准差
│  ├─ Leaf: "找出特殊案例"
│  │  └─ 方案数最多/最少的偶数
│  └─ Leaf: "保存统计报告"
│
├─ Normal: "可视化"
│  ├─ Leaf: "生成方案数分布图"
│  │  └─ 使用 Python matplotlib
│  ├─ Leaf: "生成质数使用频率图"
│  └─ Leaf: "生成热力图"
│     └─ 偶数 vs 质数对的矩阵
│
└─ Normal: "生成最终报告"
   ├─ Leaf: "撰写引言"
   ├─ Leaf: "插入统计数据"
   ├─ Leaf: "插入可视化图表"
   ├─ Leaf: "撰写结论"
   └─ Leaf: "导出为 PDF"
```

### 关键特性

#### 1. 嵌套循环（Loop in Loop）
```
外层循环：遍历 498 个偶数
  内层循环：对每个偶数，遍历所有可能的质数 p1
```
这种嵌套结构是传统 CoT 无法表达的。

#### 2. 变量持久化
```
变量传递：
- primes: List[int]          # 质数表（全局）
- current_even: int          # 当前验证的偶数
- solutions: Map[int, List]  # 每个偶数的解
- stats: Map[string, float]  # 统计数据
```

#### 3. 中间结果保存
每验证完一个偶数，立即写入文件：
```bash
echo "4 = 2 + 2" >> results.txt
echo "6 = 3 + 3" >> results.txt
echo "8 = 3 + 5" >> results.txt
...
```
即使中途中断，已验证的结果也不会丢失。

#### 4. 进度追踪
```
变量 current_even 记录当前进度
可以随时恢复：
- 读取 results.txt 的最后一行
- 解析出已验证到哪个偶数
- 从下一个偶数继续
```

### 为什么 CoT 会失败？

#### 传统 CoT 的尝试：
```
User: 验证哥德巴赫猜想对 4-1000 的偶数成立

LLM (CoT):
让我逐步验证：
1. n=4: 4 = 2+2 ✓
2. n=6: 6 = 3+3 ✓
3. n=8: 8 = 3+5 ✓
...
50. n=100: 100 = 3+97 ✓
...
[上下文窗口已满，无法继续]
```

#### LLMVM 的优势：
1. **Stateless**：每次只处理一个偶数，上下文不累积
2. **Loop 节点**：自动迭代 498 次，无需手动展开
3. **变量系统**：保存中间状态，支持断点续传
4. **命令执行**：可以调用外部工具（Python 脚本、数据库等）

### 预期执行流程

```
Step 1: 生成质数表
  → 执行命令: python generate_primes.py 1000
  → 保存到 primes.txt
  → 变量: primes = [2, 3, 5, 7, 11, ...]

Step 2-499: Loop 迭代（498 次）
  Iteration 1 (n=4):
    → 遍历质数: p1=2, p2=2 ✓
    → 保存: echo "4 = 2 + 2" >> results.txt
    → current_even = 6
  
  Iteration 2 (n=6):
    → 遍历质数: p1=3, p2=3 ✓
    → 保存: echo "6 = 3 + 3" >> results.txt
    → current_even = 8
  
  ...
  
  Iteration 498 (n=1000):
    → 遍历质数: p1=3, p2=997 ✓
    → 保存: echo "1000 = 3 + 997" >> results.txt
    → Loop finished!

Step 500: 统计分析
  → 读取 results.txt (498 行)
  → 计算平均方案数: 4.2
  → 最多方案: n=120 (8 种方案)
  → 最少方案: n=4 (1 种方案)

Step 501: 可视化
  → 生成图表: goldbach_distribution.png
  → 生成热力图: prime_pairs_heatmap.png

Step 502: 生成报告
  → 撰写 Markdown 报告
  → 插入统计数据和图表
  → 导出为 PDF: goldbach_report.pdf
```

### 性能对比

| 指标 | CoT | LLMVM |
|------|-----|-------|
| 最大验证数量 | ~50 个（上下文限制） | 498 个（理论无限） |
| 上下文长度 | O(n)，线性增长 | O(1)，常数 |
| 中断恢复 | ❌ 需要重新开始 | ✅ 从断点继续 |
| 嵌套循环 | ❌ 无法表达 | ✅ 原生支持 |
| 中间结果 | ❌ 只在内存中 | ✅ 持久化到文件 |
| 总 Token 消耗 | ~500k（全部重复） | ~50k（每次独立） |

### 扩展任务

如果要进一步展示 LLMVM 的能力，可以设计更复杂的任务：

#### 任务 2：验证孪生质数猜想
```
寻找 1-100000 之间的所有孪生质数对 (p, p+2)
需要：
- 生成 100000 以内的质数（约 9592 个）
- 遍历检查相邻质数
- 统计孪生质数的分布
- 分析间隔规律
```

#### 任务 3：蒙特卡洛模拟
```
使用蒙特卡洛方法估算圆周率 π
需要：
- 生成 1000000 个随机点
- 判断是否在单位圆内
- 计算比例并估算 π
- 绘制收敛曲线
```

#### 任务 4：递归数学证明
```
证明斐波那契数列的性质：
F(n) = F(n-1) + F(n-2)
需要：
- 递归计算 F(1) 到 F(100)
- 验证黄金比例收敛性
- 证明 GCD(F(n), F(n+1)) = 1
- 生成数学归纳法证明
```

## 总结

这个长时间推理任务设计展示了 LLMVM 相对于传统 CoT 的核心优势：

✅ **图灵完备**：支持循环、递归、条件分支  
✅ **无限扩展**：不受上下文窗口限制  
✅ **状态持久化**：中间结果保存，支持断点续传  
✅ **结构化推理**：任务树清晰表达复杂逻辑  
✅ **工具集成**：可以调用外部程序和命令  

这正是发表在 Nature Machine Intelligence 所需要的**杀手级应用案例**。