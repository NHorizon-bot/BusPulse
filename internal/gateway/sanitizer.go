// Package gateway 提供工具层输出进入 LLM 上下文前的脱敏与摘要压缩。
// 这是一个横切安全关注点：所有工具输出必须经过此层，
// 不信任各工具自己做截断，由 ToolDispatcher 统一调用。
package gateway

import (
	"regexp"
)

// 手机号：11 位纯数字，前置非数字或起始（避免误匹配 ID 中的数字段）
var phoneRe = regexp.MustCompile(`(?:^|[^\d])1[3-9]\d{9}(?:[^\d]|$)`)

// 18 位身份证号（末位可为 X）
var idCardRe = regexp.MustCompile(`\d{17}[\dXx]`)

// 精确经纬度（小数点后 6 位及以上视为精确坐标，网格化处理）
// 格式匹配：116.391234 或 -74.006123
var preciseLngLatRe = regexp.MustCompile(`-?\d{1,3}\.\d{6,}`)

// Sanitizer 脱敏器，无状态，线程安全。
type Sanitizer struct {
	maxPayloadBytes int
}

// New 创建 Sanitizer 实例。
//   - maxPayloadBytes：截断阈值（字节），超出部分丢弃，建议 1024
func New(maxPayloadBytes int) *Sanitizer {
	if maxPayloadBytes <= 0 {
		maxPayloadBytes = 1024
	}
	return &Sanitizer{maxPayloadBytes: maxPayloadBytes}
}

// Sanitize 对工具原始输出执行两步处理：
//  1. PII 字段擦除：手机号 → [PHONE]，身份证 → [ID_CARD]，精确 GPS → [GEO_GRID]
//  2. 字节截断：超过 maxPayloadBytes 时截断并追加 "...(truncated)"
//
// toolName 仅用于日志标注，不影响处理逻辑。
// 若 payload 为 nil，返回 nil（调用方按工具不可用处理）。
func (s *Sanitizer) Sanitize(toolName string, payload []byte, maxBytes int) []byte {
	if payload == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = s.maxPayloadBytes
	}

	// Step 1：PII 擦除（在截断前做，避免 PII 恰好出现在截断边界附近）
	cleaned := erasePII(payload)

	// Step 2：字节截断（防止原始大 JSON 撑爆 LLM 上下文）
	return truncate(cleaned, maxBytes)
}

// SanitizeFn 返回符合 ToolDispatcher 注入签名的函数。
// 使用场景：NewDispatcher(..., s.SanitizeFn(), ...)
func (s *Sanitizer) SanitizeFn() func(toolName string, payload []byte, maxBytes int) []byte {
	return s.Sanitize
}

// erasePII 按顺序擦除三类 PII，返回新分配的字节切片。
func erasePII(data []byte) []byte {
	result := data

	// 手机号替换（保留匹配到的前后非数字边界字符，只替换核心 11 位）
	result = phoneRe.ReplaceAllFunc(result, func(match []byte) []byte {
		s := string(match)
		// 保留首尾边界字符，仅替换中间 11 位手机号
		prefix, suffix := "", ""
		if len(s) > 0 && (s[0] < '0' || s[0] > '9') {
			prefix = string(s[0])
			s = s[1:]
		}
		if len(s) > 0 && (s[len(s)-1] < '0' || s[len(s)-1] > '9') {
			suffix = string(s[len(s)-1])
			s = s[:len(s)-1]
		}
		_ = s // 手机号本体已在正则范围内，直接整体替换
		return []byte(prefix + "[PHONE]" + suffix)
	})

	// 身份证整体替换
	result = idCardRe.ReplaceAll(result, []byte("[ID_CARD]"))

	// 精确 GPS 坐标替换（小数点后 6 位及以上）
	result = preciseLngLatRe.ReplaceAll(result, []byte("[GEO_GRID]"))

	return result
}

// truncate 若 data 超过 maxBytes，截断并追加省略标记。
// 截断在字节边界，不保证 UTF-8 完整性（JSON 内容为 ASCII 居多，可接受）。
func truncate(data []byte, maxBytes int) []byte {
	if len(data) <= maxBytes {
		return data
	}
	suffix := []byte("...(truncated)")
	cutAt := maxBytes - len(suffix)
	if cutAt < 0 {
		cutAt = 0
	}
	result := make([]byte, cutAt+len(suffix))
	copy(result, data[:cutAt])
	copy(result[cutAt:], suffix)
	return result
}
