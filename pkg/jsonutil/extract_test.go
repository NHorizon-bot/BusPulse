package jsonutil

import (
	"testing"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string // 期望提取到的 JSON 包含该子串
		wantErr bool
	}{
		{
			name:    "纯 JSON",
			input:   `{"issue_level":"P1","root_cause":"ALGO"}`,
			wantKey: `"issue_level"`,
		},
		{
			name:    "Markdown json 包裹",
			input:   "```json\n{\"issue_level\":\"P2\"}\n```",
			wantKey: `"issue_level"`,
		},
		{
			name:    "Markdown 无语言标记包裹",
			input:   "```\n{\"issue_level\":\"P3\"}\n```",
			wantKey: `"issue_level"`,
		},
		{
			name:    "JSON 前后有自然语言",
			input:   "我认为根因是围栏问题。{\"issue_level\":\"P1\"}，以上是我的分析。",
			wantKey: `"issue_level"`,
		},
		{
			name:    "嵌套 JSON",
			input:   `{"level":"P1","actions":[{"type":"JIRA","name":"创建工单"}]}`,
			wantKey: `"actions"`,
		},
		{
			name:    "截断 JSON",
			input:   `{"issue_level":"P1","root_cause":"ALGO_ETA_TIM`,
			wantErr: true,
		},
		{
			name:    "空字符串",
			input:   "",
			wantErr: true,
		},
		{
			name:    "纯自然语言无 JSON",
			input:   "我认为这是一个算法问题，无需进一步排查。",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ExtractJSON(%q) expected error, got nil (result: %s)", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ExtractJSON(%q) unexpected error: %v", tt.input, err)
				return
			}
			if tt.wantKey != "" {
				gotStr := string(got)
				if len(gotStr) == 0 {
					t.Errorf("ExtractJSON(%q) returned empty bytes", tt.input)
					return
				}
				found := false
				for i := 0; i <= len(gotStr)-len(tt.wantKey); i++ {
					if gotStr[i:i+len(tt.wantKey)] == tt.wantKey {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ExtractJSON(%q) = %s, want to contain %q", tt.input, gotStr, tt.wantKey)
				}
			}
		})
	}
}
