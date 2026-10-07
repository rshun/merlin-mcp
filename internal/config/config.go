// Package config 加载并校验 merlin-mcp 的 YAML 配置文件。
package config

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	_ "time/tzdata" // 内置时区数据，不依赖系统是否安装 tzdata

	"gopkg.in/yaml.v3"

	"github.com/rshun/merlin-mcp/internal/shell"
)

// Config 是完整的配置。CommandTimeout 和 Location 由校验步骤根据原始字段计算得出。
type Config struct {
	Listen         string  `yaml:"listen"`
	Router         Router  `yaml:"router"`
	AllowMutations bool    `yaml:"allow_mutations"`
	Paths          Paths   `yaml:"paths"`
	Dnsmasq        Dnsmasq `yaml:"dnsmasq"`
	Reboot         Reboot  `yaml:"reboot"`
	StateDir       string  `yaml:"state_dir"`
	AuditLog       string  `yaml:"audit_log"`

	CommandTimeout time.Duration  `yaml:"-"`
	Location       *time.Location `yaml:"-"`
}

type Router struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	User           string `yaml:"user"`
	KeyFile        string `yaml:"key_file"`
	KnownHosts     string `yaml:"known_hosts"`
	CommandTimeout string `yaml:"command_timeout"`
}

type Paths struct {
	DnsmasqAdd string `yaml:"dnsmasq_add"`
	BackupDir  string `yaml:"backup_dir"`
	Syslog     string `yaml:"syslog"`
	// SyslogArchive 是本机上按天存放的历史日志目录（可选），syslog_read 读取历史日期时使用。
	SyslogArchive string `yaml:"syslog_archive"`
}

type Dnsmasq struct {
	HealthCheckDomain string `yaml:"health_check_domain"`
	BackupKeep        int    `yaml:"backup_keep"`
}

type Reboot struct {
	Timezone  string `yaml:"timezone"`
	MaxPerDay int    `yaml:"max_per_day"`
}

func defaults() Config {
	return Config{
		Listen:  "127.0.0.1:8765",
		Router:  Router{Port: 22, CommandTimeout: "15s"},
		Paths:   Paths{DnsmasqAdd: "/jffs/configs/dnsmasq.conf.add", BackupDir: "/jffs/merlin-mcp/backups", Syslog: "auto"},
		Dnsmasq: Dnsmasq{HealthCheckDomain: "router.asus.com", BackupKeep: 20},
		Reboot:  Reboot{MaxPerDay: 1},
	}
}

// Load 读取并校验配置文件。未知字段视为错误，避免拼写错误被静默忽略。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var (
	routerPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	userRe       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

func isAbs(p string) bool { return p != "" && (strings.HasPrefix(p, "/") || filepath.IsAbs(p)) }

func validRouterPath(p string) bool { return routerPathRe.MatchString(p) && !strings.Contains(p, "..") }

func (c *Config) validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if host, _, err := net.SplitHostPort(c.Listen); err != nil {
		add("listen 格式错误: %v", err)
	} else if ip, err := netip.ParseAddr(host); err != nil || !ip.IsLoopback() {
		add("listen 必须是回环地址（127.0.0.0/8 或 ::1），当前为 %q", c.Listen)
	}

	if !shell.ValidHost(c.Router.Host) {
		add("router.host 无效: %q", c.Router.Host)
	}
	if c.Router.Port < 1 || c.Router.Port > 65535 {
		add("router.port 无效: %d", c.Router.Port)
	}
	if !userRe.MatchString(c.Router.User) {
		add("router.user 无效: %q", c.Router.User)
	}
	checkFile := func(name, p string, private bool) {
		if !isAbs(p) {
			add("%s 必须是绝对路径，当前为 %q", name, p)
			return
		}
		info, err := os.Stat(p)
		if err != nil {
			add("%s 无法访问: %v", name, err)
			return
		}
		if private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			add("%s 权限过宽（%o），请执行 chmod 600 %s", name, info.Mode().Perm(), p)
		}
	}
	checkFile("router.key_file", c.Router.KeyFile, true)
	checkFile("router.known_hosts", c.Router.KnownHosts, false)
	if d, err := time.ParseDuration(c.Router.CommandTimeout); err != nil || d <= 0 {
		add("router.command_timeout 无效: %q", c.Router.CommandTimeout)
	} else {
		c.CommandTimeout = d
	}

	if !validRouterPath(c.Paths.DnsmasqAdd) {
		add("paths.dnsmasq_add 必须是只含字母、数字和 ._/- 的绝对路径: %q", c.Paths.DnsmasqAdd)
	}
	if !validRouterPath(c.Paths.BackupDir) {
		add("paths.backup_dir 必须是只含字母、数字和 ._/- 的绝对路径: %q", c.Paths.BackupDir)
	}
	if c.Paths.Syslog != "auto" && !validRouterPath(c.Paths.Syslog) {
		add("paths.syslog 必须是 auto 或绝对路径: %q", c.Paths.Syslog)
	}
	if p := c.Paths.SyslogArchive; p != "" {
		if !isAbs(p) {
			add("paths.syslog_archive 必须是绝对路径: %q", p)
		} else if info, err := os.Stat(p); err != nil {
			add("paths.syslog_archive 无法访问: %v", err)
		} else if !info.IsDir() {
			add("paths.syslog_archive 不是目录: %q", p)
		}
	}

	if !shell.ValidDomain(c.Dnsmasq.HealthCheckDomain) {
		add("dnsmasq.health_check_domain 不是合法域名: %q", c.Dnsmasq.HealthCheckDomain)
	}
	if c.Dnsmasq.BackupKeep < 1 || c.Dnsmasq.BackupKeep > 1000 {
		add("dnsmasq.backup_keep 必须在 1-1000 之间: %d", c.Dnsmasq.BackupKeep)
	}

	if c.Reboot.MaxPerDay < 1 {
		add("reboot.max_per_day 必须 >= 1: %d", c.Reboot.MaxPerDay)
	}
	if c.Reboot.Timezone == "" {
		add("reboot.timezone 必填，例如 Asia/Shanghai")
	} else if loc, err := time.LoadLocation(c.Reboot.Timezone); err != nil {
		add("reboot.timezone 无法加载 %q: %v", c.Reboot.Timezone, err)
	} else {
		c.Location = loc
	}

	if !isAbs(c.StateDir) {
		add("state_dir 必须是绝对路径: %q", c.StateDir)
	}
	if !isAbs(c.AuditLog) {
		add("audit_log 必须是绝对路径: %q", c.AuditLog)
	}

	if len(errs) > 0 {
		return fmt.Errorf("配置校验失败:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
