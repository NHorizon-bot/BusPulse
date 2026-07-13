package gateway

import (
	"bytes"
	"strings"
	"testing"
)

func TestErasePII(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		notWant []string // 这些内容不应出现在输出中
		want    []string // 这些内容应出现在输出中
	}{
		{
			name:    "手机号擦除",
			input:   `{"phone":"13812345678","id":"usr_001"}`,
			notWant: []string{"13812345678"},
			want:    []string{"[PHONE]", "usr_001"},
		},
		{
			name:    "身份证擦除",
			input:   `{"id_card":"110101199001011234","name":"张X"}`,
			notWant: []string{"110101199001011234"},
			want:    []string{"[ID_CARD]"},
		},
		{
			name:    "精确 GPS 擦除",
			input:   `{"lng":116.391234,"lat":39.906789}`,
			notWant: []string{"116.391234", "39.906789"},
			want:    []string{"[GEO_GRID]"},
		},
		{
			name:    "网格化 GPS 不擦除（小数点后3位）",
			input:   `{"grid_lng":116.407,"grid_lat":39.904}`,
			notWant: []string{"[GEO_GRID]"},
			want:    []string{"116.407", "39.904"},
		},
		{
			name:    "无 PII 内容不变",
			input:   `{"order_id":"ord_001","status":"DISPATCHED"}`,
			notWant: []string{"[PHONE]", "[ID_CARD]", "[GEO_GRID]"},
			want:    []string{"ord_001", "DISPATCHED"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := erasePII([]byte(tt.input))
			resultStr := string(result)
			for _, nw := range tt.notWant {
				if strings.Contains(resultStr, nw) {
					t.Errorf("output should NOT contain %q, got: %s", nw, resultStr)
				}
			}
			for _, w := range tt.want {
				if !strings.Contains(resultStr, w) {
					t.Errorf("output should contain %q, got: %s", w, resultStr)
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		wantLen  int // 期望输出长度（字节）
		wantSuffix string
	}{
		{
			name:     "不超过阈值不截断",
			input:    `{"key":"val"}`,
			maxBytes: 100,
			wantLen:  13,
		},
		{
			name:       "超过阈值截断并追加标记",
			input:      strings.Repeat("a", 2000),
			maxBytes:   1024,
			wantSuffix: "...(truncated)",
		},
		{
			name:     "恰好等于阈值不截断",
			input:    strings.Repeat("x", 1024),
			maxBytes: 1024,
			wantLen:  1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncate([]byte(tt.input), tt.maxBytes)
			if tt.wantLen > 0 && len(result) != tt.wantLen {
				t.Errorf("truncate length = %d, want %d", len(result), tt.wantLen)
			}
			if tt.wantSuffix != "" && !bytes.HasSuffix(result, []byte(tt.wantSuffix)) {
				t.Errorf("truncated output should end with %q, got: ...%s", tt.wantSuffix, string(result[max(0, len(result)-30):]))
			}
			if len(result) > tt.maxBytes {
				t.Errorf("result length %d exceeds maxBytes %d", len(result), tt.maxBytes)
			}
		})
	}
}

func TestSanitize_NilPayload(t *testing.T) {
	s := New(1024)
	if got := s.Sanitize("tool", nil, 1024); got != nil {
		t.Errorf("Sanitize(nil) should return nil, got %v", got)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
