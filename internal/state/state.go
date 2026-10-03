// Package state 管理 Debian 侧的状态文件（重启记录、dnsmasq known good 指针）。
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State 是 state.json 的完整内容。
type State struct {
	Reboot  RebootState  `json:"reboot"`
	Dnsmasq DnsmasqState `json:"dnsmasq"`
}

// RebootState 记录由 MCP 发起的重启时间（只保留最近若干条）。
type RebootState struct {
	History []time.Time `json:"history"`
}

// DnsmasqState 记录回滚目标和 MCP 最后一次写入后的文件指纹。
type DnsmasqState struct {
	KnownGoodBackup string `json:"known_good_backup"`
	LastMCPSHA256   string `json:"last_mcp_sha256"`
}

func (st State) clone() State {
	c := st
	c.Reboot.History = append([]time.Time(nil), st.Reboot.History...)
	return c
}

// Store 在进程内用互斥锁保护状态，每次修改都原子写入磁盘。
type Store struct {
	mu   sync.Mutex
	path string
	cur  State
}

// Open 打开 dir/state.json；文件不存在时返回空状态，文件损坏时返回错误而不是重置。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建状态目录失败: %w", err)
	}
	s := &Store{path: filepath.Join(dir, "state.json")}
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}
	if err := json.Unmarshal(data, &s.cur); err != nil {
		return nil, fmt.Errorf("状态文件 %s 已损坏，请人工检查后修复或移走（不会自动重置，以免清零重启额度）: %w", s.path, err)
	}
	return s, nil
}

// Get 返回当前状态的深拷贝。
func (s *Store) Get() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.clone()
}

// Update 在锁内修改状态并落盘。fn 返回错误时原样返回该错误，内存和磁盘都不变。
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur.clone()
	if err := fn(&next); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}
	if err := writeAtomic(s.path, data); err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}
	s.cur = next
	return nil
}

// writeAtomic 先写临时文件并 fsync，再 rename 覆盖目标文件。
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
