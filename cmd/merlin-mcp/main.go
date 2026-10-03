// merlin-mcp 是管理 Asuswrt-Merlin 路由器的 MCP Server。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/audit"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/diagnose"
	"github.com/rshun/merlin-mcp/internal/dnsmasq"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
	"github.com/rshun/merlin-mcp/internal/sshx"
	"github.com/rshun/merlin-mcp/internal/state"
	"github.com/rshun/merlin-mcp/internal/syslog"
	"github.com/rshun/merlin-mcp/internal/tools"
)

var version = "dev"

const usage = `用法:
  merlin-mcp serve   --config <配置文件>   启动 MCP 服务
  merlin-mcp check   --config <配置文件>   只读检查配置和路由器连接
  merlin-mcp version                       输出版本号`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		return serve(args[1:], stderr)
	case "check":
		return check(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// configPath 解析 --config；缺失时返回 false。
func configPath(name string, args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	p := fs.String("config", "", "配置文件路径")
	if err := fs.Parse(args); err != nil || *p == "" {
		fmt.Fprintln(stderr, usage)
		return "", false
	}
	return *p, true
}

type app struct {
	ssh  *sshx.Client
	deps tools.Deps
}

func build(cfg *config.Config) (*app, error) {
	store, err := state.Open(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	client, err := sshx.New(sshx.Options{
		Addr:        net.JoinHostPort(cfg.Router.Host, strconv.Itoa(cfg.Router.Port)),
		User:        cfg.Router.User,
		KeyFile:     cfg.Router.KeyFile,
		KnownHosts:  cfg.Router.KnownHosts,
		Timeout:     cfg.CommandTimeout,
		DialTimeout: 10 * time.Second,
		MaxSessions: 4,
		KeepAlive:   30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	quota := reboot.NewQuota(store, cfg.Location, cfg.Reboot.MaxPerDay, time.Now)
	return &app{ssh: client, deps: tools.Deps{
		Cfg:      cfg,
		Runner:   client,
		Quota:    quota,
		Rebooter: reboot.NewRebooter(quota, client),
		Dnsmasq: dnsmasq.New(client, dnsmasq.Options{
			AddPath:      cfg.Paths.DnsmasqAdd,
			BackupDir:    cfg.Paths.BackupDir,
			HealthDomain: cfg.Dnsmasq.HealthCheckDomain,
			Keep:         cfg.Dnsmasq.BackupKeep,
			Store:        store,
		}),
		Audit: audit.New(cfg.AuditLog),
	}}, nil
}

func serve(args []string, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	slog.SetDefault(logger)
	p, ok := configPath("serve", args, stderr)
	if !ok {
		return 2
	}
	cfg, err := config.Load(p)
	if err != nil {
		logger.Error("配置无效", "err", err)
		return 1
	}
	a, err := build(cfg)
	if err != nil {
		logger.Error("初始化失败", "err", err)
		return 1
	}
	defer a.ssh.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "merlin-mcp", Version: version}, nil)
	tools.Register(server, a.deps)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","version":%q}`, version)
	})
	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("merlin-mcp 已启动", "listen", cfg.Listen, "version", version, "allow_mutations", cfg.AllowMutations)

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "err", err)
			return 1
		}
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return 0
}

// check 依次执行只读检查。SSH 失败时立即退出；其余项目失败只给出警告。
func check(args []string, stdout, stderr io.Writer) int {
	p, ok := configPath("check", args, stderr)
	if !ok {
		return 2
	}
	cfg, err := config.Load(p)
	if err != nil {
		fmt.Fprintln(stderr, "✗ 配置:", err)
		return 1
	}
	fmt.Fprintln(stdout, "✓ 配置文件校验通过")
	a, err := build(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "✗ 初始化:", err)
		return 1
	}
	defer a.ssh.Close()
	fmt.Fprintln(stdout, "✓ 私钥可读取，known_hosts 中有路由器的记录")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	step := func(name, cmd string, judge func(runner.Result) (string, bool)) (bool, error) {
		res, err := a.ssh.Run(ctx, cmd, nil)
		if err != nil {
			fmt.Fprintf(stdout, "✗ %s: %v\n", name, err)
			return false, err
		}
		msg, good := judge(res)
		mark := "✓"
		if !good {
			mark = "!"
		}
		fmt.Fprintf(stdout, "%s %s: %s\n", mark, name, msg)
		return good, nil
	}
	trim := func(b []byte) string { return strings.TrimSpace(string(b)) }

	if _, err := step("SSH 连接与认证", runner.Op("check_echo", "echo ok"), func(r runner.Result) (string, bool) {
		return trim(r.Stdout), r.ExitCode == 0
	}); err != nil {
		return 1
	}
	_, _ = step("syslog 路径", runner.Op("check_syslog", syslog.DetectScript(cfg.Paths.Syslog)+`; echo "$p"`), func(r runner.Result) (string, bool) {
		if r.ExitCode != 0 {
			return trim(r.Stderr), false
		}
		return trim(r.Stdout), true
	})
	addFile := shell.Quote(cfg.Paths.DnsmasqAdd)
	_, _ = step("dnsmasq.conf.add", runner.Op("check_addfile", fmt.Sprintf("if [ -f %s ]; then echo 存在; else echo 不存在（第一次 add 时会自动创建）; fi", addFile)), func(r runner.Result) (string, bool) {
		return trim(r.Stdout), true
	})
	_, _ = step("dnsmasq --test 支持", runner.Op("check_dnsmasq_test", "dnsmasq --help 2>&1 | grep -c -- '--test' || true"), func(r runner.Result) (string, bool) {
		if n := trim(r.Stdout); n != "" && n != "0" {
			return "支持", true
		}
		return "不支持，dnsmasq_apply 将跳过语法校验", false
	})
	_, _ = step("健康检查域名", runner.Op("check_health", fmt.Sprintf("nslookup %s 127.0.0.1 2>&1", shell.Quote(cfg.Dnsmasq.HealthCheckDomain))), func(r runner.Result) (string, bool) {
		if ips := diagnose.ParseNslookup(string(r.Stdout)); r.ExitCode == 0 && len(ips) > 0 {
			return cfg.Dnsmasq.HealthCheckDomain + " → " + strings.Join(ips, ", "), true
		}
		return "无法解析 " + cfg.Dnsmasq.HealthCheckDomain + "，请在配置中更换 dnsmasq.health_check_domain", false
	})
	_, _ = step("路由器时间", runner.Op("check_date", "date '+%s %z'"), func(r runner.Result) (string, bool) {
		return trim(r.Stdout), r.ExitCode == 0
	})
	return 0
}
