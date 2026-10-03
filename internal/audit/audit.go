// Package audit 以 JSONL 格式记录修改类工具的调用。
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Entry 是一条审计记录。
type Entry struct {
	TS         time.Time      `json:"ts"`
	Tool       string         `json:"tool"`
	Args       map[string]any `json:"args,omitempty"`
	Outcome    string         `json:"outcome"` // ok / rejected / error
	ErrorCode  string         `json:"error_code,omitempty"`
	DurationMS int64          `json:"duration_ms"`
	Summary    string         `json:"summary,omitempty"`
}

// Logger 追加写入审计日志文件，并发安全。
type Logger struct {
	mu   sync.Mutex
	path string
}

// New 创建写入 path 的 Logger；目录在第一次写入时创建。
func New(path string) *Logger { return &Logger{path: path} }

// Write 脱敏后追加一行 JSON。
func (l *Logger) Write(e Entry) error {
	e.Args = Redact(e.Args)
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// 不使用单独的 key 作为匹配词，避免误伤 keyword 这类参数。
var sensitiveRe = regexp.MustCompile(`(?i)(passwd|password|token|secret|private_key|api_key)`)

// Redact 返回 args 的副本，名称敏感的字段替换为 ****。
func Redact(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if sensitiveRe.MatchString(k) {
			out[k] = "****"
		} else {
			out[k] = v
		}
	}
	return out
}
