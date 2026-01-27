package llm

import (
	"encoding/json"
	"fmt"
)

// StubEngine 用于测试
type StubEngine struct{}

func (s *StubEngine) Call(prompt string) (*Output, error) {
	// 返回一个符合格式的测试响应
	// 根据 prompt 内容，返回创建节点的响应
	response := Response{
		Actions: []Action{
			{
				ActionType: "create_node",
				Node: NodeDTO{
					ID:          "child_loop_1",
					Name:        "Loop Node",
					Type:        "Loop",
					Information: "这个节点需要循环处理",
				},
			},
			{
				ActionType: "create_node",
				Node: NodeDTO{
					ID:          "child_normal_1",
					Name:        "Normal Node",
					Type:        "Normal",
					Information: "这个节点是普通推进节点",
				},
			},
			{
				ActionType: "create_node",
				Node: NodeDTO{
					ID:          "child_leaf_1",
					Name:        "Leaf Node",
					Type:        "Leaf",
					Information: "这个节点是叶子节点，将被执行",
				},
			},
		},
	}

	jsonData, err := json.MarshalIndent(response, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal stub response: %w", err)
	}

	return &Output{Response: string(jsonData)}, nil
}

func (s *StubEngine) CallAsync(prompt string) <-chan *Output {
	ch := make(chan *Output, 1)
	go func() {
		out, _ := s.Call(prompt)
		ch <- out
	}()
	return ch
}
