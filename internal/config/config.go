// Package config 负责从 YAML 文件加载服务配置，支持本地覆盖文件。
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 全量配置结构体，与 configs/config.yaml 字段一一对应。
type Config struct {
	Server         ServerConfig         `yaml:"server"`
	ToolDispatcher ToolDispatcherConfig `yaml:"tool_dispatcher"`
	LLM            LLMConfig            `yaml:"llm"`
	RuleEngine     RuleEngineConfig     `yaml:"rule_engine"`
	Audit          AuditConfig          `yaml:"audit"`
	Sanitizer      SanitizerConfig      `yaml:"sanitizer"`
}

type ServerConfig struct {
	Port                int `yaml:"port"`
	DiagnoseTimeoutMs   int `yaml:"diagnose_timeout_ms"`
}

type ToolDispatcherConfig struct {
	FanOutTimeoutMs int                    `yaml:"fan_out_timeout_ms"`
	ToolTimeouts    map[string]int         `yaml:"tool_timeouts"`
}

type LLMConfig struct {
	// Provider 模型供应商：deepseek | qwen（默认 deepseek）
	Provider      string  `yaml:"provider"`
	Endpoint      string  `yaml:"endpoint"`
	APIKey        string  `yaml:"api_key"`
	Model         string  `yaml:"model"`
	Temperature   float64 `yaml:"temperature"`
	MaxTokens     int     `yaml:"max_tokens"`
	MaxIterations int     `yaml:"max_iterations"`
}

type RuleEngineConfig struct {
	GPSDeviationThresholdM    float64 `yaml:"gps_deviation_threshold_m"`
	GPSDeviationDurationS     int     `yaml:"gps_deviation_duration_s"`
	EtaProtectionThresholdMin float64 `yaml:"eta_protection_threshold_min"`
	HeadingConflictDeg        float64 `yaml:"heading_conflict_threshold_deg"`
	CongestionIndexThreshold  float64 `yaml:"congestion_index_threshold"`
}

type AuditConfig struct {
	Backend  string `yaml:"backend"`   // "file" | "mysql"
	FilePath string `yaml:"file_path"`
}

type SanitizerConfig struct {
	MaxPayloadBytes int `yaml:"max_payload_bytes"`
}

// DiagnoseTimeout 返回全局诊断超时 Duration。
func (c *Config) DiagnoseTimeout() time.Duration {
	ms := c.Server.DiagnoseTimeoutMs
	if ms <= 0 {
		ms = 3500
	}
	return time.Duration(ms) * time.Millisecond
}

// FanOutTimeout 返回工具扇出层超时 Duration。
func (c *Config) FanOutTimeout() time.Duration {
	ms := c.ToolDispatcher.FanOutTimeoutMs
	if ms <= 0 {
		ms = 1500
	}
	return time.Duration(ms) * time.Millisecond
}

// ToolTimeout 按工具名返回其独立超时预算，找不到时返回 fallback。
func (c *Config) ToolTimeout(toolName string, fallback time.Duration) time.Duration {
	if ms, ok := c.ToolDispatcher.ToolTimeouts[toolName]; ok && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

// Load 从 baseFile 加载配置，再用 overrideFile（若存在）合并覆盖。
// overrideFile 不存在时静默跳过（不报错），适用于 config.local.yaml 可选覆盖场景。
func Load(baseFile, overrideFile string) (*Config, error) {
	cfg := &Config{}

	if err := loadYAML(baseFile, cfg); err != nil {
		return nil, fmt.Errorf("load base config %q: %w", baseFile, err)
	}

	// 本地覆盖文件可选，不存在则跳过
	if _, err := os.Stat(overrideFile); err == nil {
		if err := loadYAML(overrideFile, cfg); err != nil {
			return nil, fmt.Errorf("load override config %q: %w", overrideFile, err)
		}
	}

	applyDefaults(cfg)
	return cfg, nil
}

// loadYAML 将 YAML 文件解码合并到 dst。
// 使用 yaml.v3 的合并语义：仅覆盖文件中出现的字段，其余保持不变。
func loadYAML(path string, dst *Config) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return yaml.NewDecoder(f).Decode(dst)
}

// applyDefaults 补填零值字段的合理默认值。
func applyDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.DiagnoseTimeoutMs == 0 {
		cfg.Server.DiagnoseTimeoutMs = 3500
	}
	if cfg.ToolDispatcher.FanOutTimeoutMs == 0 {
		cfg.ToolDispatcher.FanOutTimeoutMs = 1500
	}
	if cfg.LLM.MaxIterations == 0 {
		cfg.LLM.MaxIterations = 4
	}
	if cfg.LLM.Temperature == 0 {
		cfg.LLM.Temperature = 0.1
	}
	if cfg.Sanitizer.MaxPayloadBytes == 0 {
		cfg.Sanitizer.MaxPayloadBytes = 1024
	}
	if cfg.Audit.Backend == "" {
		cfg.Audit.Backend = "file"
	}
	if cfg.Audit.FilePath == "" {
		cfg.Audit.FilePath = "./logs/audit.jsonl"
	}
	if cfg.RuleEngine.GPSDeviationThresholdM == 0 {
		cfg.RuleEngine.GPSDeviationThresholdM = 500
	}
	if cfg.RuleEngine.GPSDeviationDurationS == 0 {
		cfg.RuleEngine.GPSDeviationDurationS = 180
	}
	if cfg.RuleEngine.EtaProtectionThresholdMin == 0 {
		cfg.RuleEngine.EtaProtectionThresholdMin = 15
	}
	if cfg.RuleEngine.HeadingConflictDeg == 0 {
		cfg.RuleEngine.HeadingConflictDeg = 90
	}
	if cfg.RuleEngine.CongestionIndexThreshold == 0 {
		cfg.RuleEngine.CongestionIndexThreshold = 3.5
	}
}
