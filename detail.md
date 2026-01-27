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