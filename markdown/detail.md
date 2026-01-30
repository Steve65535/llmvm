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
DEEPSEEK_API_KEY=sk-6cb6f64b1f83461cb7630968ca8bbeba go run cmd/main.go "创建一个three.js驱动的3d应用 空间贪吃蛇
"
```

## 紧急停机 (Emergency Shutdown)
如果需要立即停止所有递归任务：
1. **终端层面**：直接使用 `Ctrl + C` 强制终止进程。
2. **逻辑层面**：AI 可以返回 `{"action_type": "shutdown", "result": "原因"}` 动作，系统会立即抛出 `EMERGENCY_SHUTDOWN` 异常并停止遍历。
3. **交互层面**：在提示输入时输入 `exit` 即可安全退出。

具体的执行逻辑：

1. 深度优先搜索
2. 他有一个loop stack 一旦碰到一个loop节点 就给loopstack中push一个数字 比如1 然后在每个节点中 使用一个wetherfinished变量去记载是否完整的完成了（注意 wetherfinished和wethertraveled是不同的含义 wethertraveled只代表cursor经过了这个tasknode 而wetherfinished主要用于记载是否完美的完成了 也就是判断是否需要pop出stack 然后上一级的wetherfinished应该是底层的wetherfinished的and操作） 只有最底层的leaf是原生决定wetherfinished操作 上层全都是底层的and 然后往上移动遇到loop节点 如果子节点全部finished 就pop栈 同时跳出这个loop子节点 

我的思想其实有好几个 一个是ast  把所有的任务和计算抽象成了一个“函数” 追求了图灵完备架构 保证了无限任务执行 而并非之前的llm图灵机 另一个是stateless slice 解决了上下文爆炸的问题 然后是节点级别注意力机制

go build ./pkg/llm/...