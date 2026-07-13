// Package audit 提供诊断全链路审计日志的持久化能力。
// MVP 阶段写入 JSONL 文件，每行一条 AuditRecord。
// Logger 线程安全，多个 goroutine 可并发写入。
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/buspulse/diagnose-agent/internal/domain"
)

// ToolInvocation 单个工具调用的审计记录
type ToolInvocation struct {
	Tool       string `json:"tool"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"` // "SUCCESS" | "TIMEOUT" | "ERROR"
}

// HumanAction 人工复核动作
type HumanAction string

const (
	HumanActionPending  HumanAction = "PENDING"
	HumanActionConfirmed HumanAction = "CONFIRMED"
	HumanActionRejected HumanAction = "REJECTED"
)

// Record 一次完整诊断的审计记录
type Record struct {
	AuditID          string           `json:"audit_id"`
	OperatorID       string           `json:"operator_id"`
	TriggerSource    string           `json:"trigger_source"`
	OrderID          string           `json:"order_id"`
	TimestampUnix    int64            `json:"timestamp"`
	ToolsInvoked     []ToolInvocation `json:"agent_tools_invoked"`
	DiagDurationMs   int64            `json:"diag_duration_ms"`
	// LLMRawDigest LLM 原始响应的摘要（前 200 字），不存完整内容
	LLMRawDigest     string           `json:"llm_raw_digest,omitempty"`
	FinalReport      *domain.DiagnosticReport `json:"final_diagnostic_report"`
	HumanAction      HumanAction      `json:"human_action"`
	CorrectionNote   string           `json:"human_correction_note,omitempty"`
	Err              string           `json:"error,omitempty"`
}

// Logger 审计日志写入器，线程安全。
type Logger struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// NewFileLogger 创建写入 JSONL 文件的审计 Logger。
// filePath 不存在时自动创建（含父目录）。
func NewFileLogger(filePath string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return nil, fmt.Errorf("audit mkdir %q: %w", filepath.Dir(filePath), err)
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("audit open %q: %w", filePath, err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false) // 保留中文字符可读性
	return &Logger{file: f, enc: enc}, nil
}

// Write 将一条审计记录追加写入 JSONL 文件，线程安全。
func (l *Logger) Write(r Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enc.Encode(r)
}

// Close 关闭底层文件，应在服务退出时调用。
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// NewAuditID 生成审计记录唯一 ID。
// 格式：aud_<unix秒>_<orderID前8位>
func NewAuditID(orderID string) string {
	prefix := orderID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	return fmt.Sprintf("aud_%d_%s", time.Now().Unix(), prefix)
}

// Digest 截取字符串前 n 个字节作为摘要，超出则追加 "..."
func Digest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
