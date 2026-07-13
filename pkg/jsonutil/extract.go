// Package jsonutil 提供 LLM 输出的 JSON 解析防御链，与业务无关，可被任何 Go LLM 项目复用。
package jsonutil

import (
	"fmt"
	"regexp"
	"strings"
)

// markdownCodeBlockRe 匹配 ```json ... ``` 或 ``` ... ``` 包裹的内容
var markdownCodeBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.+?)\\s*```")

// ExtractJSON 三层 JSON 解析防御链：
//
//  1. 剥离 Markdown code block 包裹（最常见的 LLM 格式问题）
//  2. 定位并提取第一个完整 JSON 对象（处理截断 JSON 和前后多余文字）
//  3. 返回提取到的原始 JSON 字节，由调用方负责 Unmarshal 和字段校验
//
// 若三层均无法提取到完整 JSON，返回描述性 error，调用方应触发降级逻辑，
// 而非继续重试 LLM（避免将格式重试预算浪费在解析层）。
func ExtractJSON(raw string) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("llm response is empty")
	}

	// 第一层：剥离 Markdown code block
	if m := markdownCodeBlockRe.FindStringSubmatch(raw); len(m) > 1 {
		raw = strings.TrimSpace(m[1])
	}

	// 第二层：提取第一个完整 JSON 对象（深度计数器，处理嵌套）
	jsonBytes, err := extractFirstObject(raw)
	if err != nil {
		return nil, fmt.Errorf("json object extraction failed: %w", err)
	}

	return jsonBytes, nil
}

// extractFirstObject 从字符串中找到第一个平衡的 { } JSON 对象。
// 能处理截断 JSON（在找到起点后 } 数不够时报错），不会 panic。
func extractFirstObject(s string) ([]byte, error) {
	depth := 0
	start := -1
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if inString {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				// 遇到 } 但没有对应的 {，说明原始文本格式混乱
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				return []byte(s[start : i+1]), nil
			}
		}
	}

	if start == -1 {
		return nil, fmt.Errorf("no JSON object '{' found in response")
	}
	// start >= 0 但 depth > 0：找到开头但 JSON 被截断
	return nil, fmt.Errorf("JSON object starting at offset %d is truncated (depth=%d)", start, depth)
}
