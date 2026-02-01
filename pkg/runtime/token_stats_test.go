package runtime

import (
	"testing"
)

// TestTokenEstimation 测试 Token 估算功能
func TestTokenEstimation(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{
			name:     "Pure English",
			text:     "Hello, this is a test message with about twenty words in it for testing purposes.",
			expected: 20, // ~80 chars / 4 = 20 tokens
		},
		{
			name:     "Pure Chinese",
			text:     "这是一个测试消息，包含大约二十个汉字用于测试目的。",
			expected: 28, // 中文字符数 * 2/3，实际约 28 tokens
		},
		{
			name:     "Mixed",
			text:     "Hello 你好 World 世界",
			expected: 6, // ~12 English chars / 4 + 4 Chinese chars * 2/3 = 3 + 2 = 5-6
		},
		{
			name:     "Empty",
			text:     "",
			expected: 0,
		},
		{
			name:     "Long English",
			text:     "This is a much longer text that contains many more words and should result in a higher token count. We are testing the token estimation function to see if it can handle longer inputs correctly.",
			expected: 44, // ~176 chars / 4 = 44 tokens
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := estimateTokenCount(tt.text)

			// 允许 ±20% 的误差
			tolerance := int(float64(tt.expected) * 0.2)
			if tolerance < 1 {
				tolerance = 1
			}

			if result < tt.expected-tolerance || result > tt.expected+tolerance {
				t.Errorf("estimateTokenCount(%q) = %d, want %d (±%d)", tt.text, result, tt.expected, tolerance)
			}
		})
	}
}

// TestTokenEstimationLarge 测试大文本的 Token 估算
func TestTokenEstimationLarge(t *testing.T) {
	// 生成一个大约 10000 字符的文本
	largeText := ""
	for i := 0; i < 1000; i++ {
		largeText += "This is a test sentence. "
	}

	result := estimateTokenCount(largeText)

	// 大约 25000 字符 / 4 = 6250 tokens
	expected := 6250
	tolerance := 1000 // ±1000 tokens

	if result < expected-tolerance || result > expected+tolerance {
		t.Errorf("estimateTokenCount(large text) = %d, want %d (±%d)", result, expected, tolerance)
	}
}

// TestTokenEstimationCodeSnippet 测试代码片段的 Token 估算
func TestTokenEstimationCodeSnippet(t *testing.T) {
	codeSnippet := `func main() {
	fmt.Println("Hello, World!")
	for i := 0; i < 10; i++ {
		fmt.Printf("Count: %d\n", i)
	}
}`

	result := estimateTokenCount(codeSnippet)

	// 代码通常比普通文本有更多 token
	// 大约 100+ 字符，估计 25-40 tokens
	if result < 20 || result > 50 {
		t.Errorf("estimateTokenCount(code snippet) = %d, expected between 20-50", result)
	}
}
