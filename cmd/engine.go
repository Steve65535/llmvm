package main

import (
	"fmt"

	"github.com/Steve65535/llmvm/pkg/llm"
)

// initEngine 选择 LLM 引擎。
//
// DEEPSEEK_API_KEY 不存在时退化到 StubEngine（确定性、无网络），方便本地测试 / CI。
func initEngine() llm.Engine {
	apiEngine, err := llm.NewLLMEngine()
	if err != nil {
		fmt.Println("⚠️  Warning: LLM API not available, using StubEngine for testing")
		return &llm.StubEngine{}
	}
	fmt.Println("✅ LLM Engine initialized successfully")
	return apiEngine
}
