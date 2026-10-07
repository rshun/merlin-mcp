package tools_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/audit"
	"github.com/rshun/merlin-mcp/internal/config"
	"github.com/rshun/merlin-mcp/internal/dnsmasq"
	"github.com/rshun/merlin-mcp/internal/reboot"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/state"
	"github.com/rshun/merlin-mcp/internal/tools"
)

type env struct {
	deps      tools.Deps
	auditPath string
}

func newEnv(t *testing.T, r runner.Runner, allowMutations bool) env {
	t.Helper()
	dir := t.TempDir()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	cfg := &config.Config{
		AllowMutations: allowMutations,
		Location:       loc,
		Paths:          config.Paths{DnsmasqAdd: "/jffs/configs/dnsmasq.conf.add", BackupDir: "/jffs/merlin-mcp/backups", Syslog: "auto"},
	}
	now := func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, loc) }
	quota := reboot.NewQuota(store, loc, 1, now)
	auditPath := filepath.Join(dir, "audit.jsonl")
	return env{
		auditPath: auditPath,
		deps: tools.Deps{
			Cfg:      cfg,
			Runner:   r,
			Quota:    quota,
			Rebooter: reboot.NewRebooter(quota, r),
			Dnsmasq: dnsmasq.New(r, dnsmasq.Options{
				AddPath: cfg.Paths.DnsmasqAdd, BackupDir: cfg.Paths.BackupDir, HealthDomain: "router.asus.com",
				Keep: 20, Store: store, Sleep: func(time.Duration) {},
			}),
			Audit: audit.New(auditPath),
		},
	}
}

func connect(t *testing.T, d tools.Deps) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "merlin-mcp", Version: "test"}, nil)
	tools.Register(server, d)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s 没有返回内容", name)
	}
	return res, res.Content[0].(*mcp.TextContent).Text
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		m[tl.Name] = tl
	}
	return m
}

var readOnlyNames = []string{"syslog_read", "kernel_log_read", "system_status", "wan_status", "clients_list", "conntrack_status", "network_diagnose", "dnsmasq_addfile_read", "dnsmasq_effective_config"}
var mutationNames = []string{"dnsmasq_addfile_add", "dnsmasq_addfile_remove", "dnsmasq_apply", "router_reboot"}

func TestRegistersAllToolsWhenMutationsAllowed(t *testing.T) {
	got := listTools(t, connect(t, newEnv(t, runnertest.New(), true).deps))
	if len(got) != 13 {
		t.Fatalf("工具数量 = %d", len(got))
	}
	for _, n := range append(append([]string{}, readOnlyNames...), mutationNames...) {
		if got[n] == nil {
			t.Errorf("缺少工具 %s", n)
		}
	}
	for _, n := range readOnlyNames {
		if !got[n].Annotations.ReadOnlyHint {
			t.Errorf("%s 应标记 readOnlyHint", n)
		}
	}
	if dh := got["router_reboot"].Annotations.DestructiveHint; dh == nil || !*dh {
		t.Error("router_reboot 应标记 destructiveHint")
	}
}

func TestReadOnlyModeRegistersNineTools(t *testing.T) {
	got := listTools(t, connect(t, newEnv(t, runnertest.New(), false).deps))
	if len(got) != 9 {
		t.Fatalf("工具数量 = %d", len(got))
	}
	for _, n := range mutationNames {
		if got[n] != nil {
			t.Errorf("只读模式不应注册 %s", n)
		}
	}
}

func TestRebootRequiresConfirm(t *testing.T) {
	f := runnertest.New()
	e := newEnv(t, f, true)
	res, text := call(t, connect(t, e.deps), "router_reboot", map[string]any{})
	if !res.IsError || !strings.Contains(text, string(apperr.ConfirmRequired)) {
		t.Fatalf("res=%v text=%s", res.IsError, text)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("没有 confirm 时不应执行任何命令")
	}
	data, _ := os.ReadFile(e.auditPath)
	if !strings.Contains(string(data), `"outcome":"rejected"`) {
		t.Fatalf("应记录被拒绝的调用: %s", data)
	}
}

func TestRebootQuotaEnforced(t *testing.T) {
	f := runnertest.New().On("reboot", runnertest.Stdout(""))
	cs := connect(t, newEnv(t, f, true).deps)
	if res, text := call(t, cs, "router_reboot", map[string]any{"confirm": true}); res.IsError {
		t.Fatalf("第一次应成功: %s", text)
	}
	res, text := call(t, cs, "router_reboot", map[string]any{"confirm": true})
	if !res.IsError || !strings.Contains(text, string(apperr.RebootQuotaExceeded)) {
		t.Fatalf("第二次应被拒绝: %s", text)
	}
	if n := len(f.CallsFor("reboot")); n != 1 {
		t.Fatalf("reboot 执行了 %d 次", n)
	}
}

func TestReadOnlyToolRetriesOnDisconnect(t *testing.T) {
	n := 0
	f := runnertest.New().On("conntrack", func(string, []byte) (runner.Result, error) {
		n++
		if n == 1 {
			return runner.Result{}, apperr.New(apperr.SSHUnreachable, "断开", "")
		}
		return runner.Result{Stdout: []byte("@@MERLINMCP:count@@\n100\n@@MERLINMCP:max@@\n1000\n")}, nil
	})
	res, text := call(t, connect(t, newEnv(t, f, true).deps), "conntrack_status", map[string]any{})
	if res.IsError || !strings.Contains(text, `"count": 100`) || n != 2 {
		t.Fatalf("isError=%v n=%d text=%s", res.IsError, n, text)
	}
}

func TestMutationDoesNotRetryOnDisconnect(t *testing.T) {
	n := 0
	f := runnertest.New().On("addfile_read", func(string, []byte) (runner.Result, error) {
		n++
		return runner.Result{}, apperr.New(apperr.SSHUnreachable, "断开", "")
	})
	res, text := call(t, connect(t, newEnv(t, f, true).deps), "dnsmasq_addfile_add", map[string]any{"lines": []any{"address=/a.example/0.0.0.0"}})
	if !res.IsError || !strings.Contains(text, string(apperr.SSHUnreachable)) || n != 1 {
		t.Fatalf("isError=%v n=%d text=%s", res.IsError, n, text)
	}
}

func TestSyslogRejectsBadArguments(t *testing.T) {
	cs := connect(t, newEnv(t, runnertest.New(), true).deps)
	for _, args := range []map[string]any{{"since": "1w"}, {"lines": 5000}, {"process": "a b"}} {
		res, text := call(t, cs, "syslog_read", args)
		if !res.IsError || !strings.Contains(text, string(apperr.InvalidArgument)) {
			t.Errorf("args=%v 应返回 INVALID_ARGUMENT: %s", args, text)
		}
	}
}

func TestDryRunIsNotAudited(t *testing.T) {
	f := runnertest.New().On("addfile_read", runnertest.Stdout("MISSING\n"))
	e := newEnv(t, f, true)
	res, text := call(t, connect(t, e.deps), "dnsmasq_addfile_add", map[string]any{"lines": []any{"x=1"}, "dry_run": true})
	if res.IsError || !strings.Contains(text, "+x=1") {
		t.Fatalf("text = %s", text)
	}
	if _, err := os.Stat(e.auditPath); !os.IsNotExist(err) {
		t.Fatal("dry_run 不应写审计日志")
	}
}

func TestSystemStatusIncludesRebootQuota(t *testing.T) {
	out := "@@MERLINMCP:uptime@@\n100.0 0\n@@MERLINMCP:loadavg@@\n0 0 0 1/1 1\n@@MERLINMCP:meminfo@@\nMemTotal: 100 kB\nMemAvailable: 50 kB\n" +
		"@@MERLINMCP:stat1@@\ncpu 1 0 1 8 0 0 0\n@@MERLINMCP:stat2@@\ncpu 2 0 2 16 0 0 0\n@@MERLINMCP:temp@@\n@@MERLINMCP:nvram@@\nproductid=RT-TEST\n"
	f := runnertest.New().On("system_status", runnertest.Stdout(out))
	res, text := call(t, connect(t, newEnv(t, f, true).deps), "system_status", map[string]any{})
	if res.IsError || !strings.Contains(text, `"reboot_allowed_today": true`) || !strings.Contains(text, "RT-TEST") {
		t.Fatalf("text = %s", text)
	}
}

// day 返回上海时区"今天"偏移 offset 天后的 0 点。
func day(offset int) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day()+offset, 0, 0, 0, 0, loc)
}

func syslogLine(d time.Time, hour int, msg string) string {
	return d.Add(time.Duration(hour)*time.Hour).Format("Jan _2 15:04:05") + " kernel: " + msg + "\n"
}

func writeArchive(t *testing.T, dir string, d time.Time, content string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(content))
	zw.Close()
	if err := os.WriteFile(filepath.Join(dir, "merlin-syslog-"+d.Format("2006-01-02")+".log.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// routerSyslog 返回 syslog_fetch 的假输出：轮转文件里是昨天的日志，当前文件里是今天的日志。
func routerSyslog() runnertest.Handler {
	out := fmt.Sprintf("@@MERLINMCP:path@@\n/jffs/syslog.log\n@@MERLINMCP:date@@\n%d +0800\n", time.Now().Unix()) +
		"@@MERLINMCP:rotated@@\n" + syslogLine(day(-1), 23, "router-yesterday") +
		"@@MERLINMCP:current@@\n" + syslogLine(day(0), 0, "router-today")
	return runnertest.Stdout(out)
}

func TestSyslogHistoryDateReadsLocalArchive(t *testing.T) {
	f := runnertest.New() // 没有注册任何操作：一旦走 SSH 就会报错
	e := newEnv(t, f, false)
	dir := t.TempDir()
	e.deps.Cfg.Paths.SyslogArchive = dir
	d := day(-2)
	writeArchive(t, dir, d, syslogLine(d, 1, "keep me")+syslogLine(d, 2, "other"))
	res, text := call(t, connect(t, e.deps), "syslog_read", map[string]any{"date": d.Format("2006-01-02"), "keyword": "keep"})
	if res.IsError || !strings.Contains(text, "keep me") || strings.Contains(text, "other") || !strings.Contains(text, `"source": "archive"`) {
		t.Fatalf("isError=%v text=%s", res.IsError, text)
	}
	if n := len(f.Calls()); n != 0 {
		t.Fatalf("读取历史归档不应连接路由器，调用了 %d 次", n)
	}
}

func TestSyslogYesterdayFallsBackToRouterBeforeArchiveArrives(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", routerSyslog())
	e := newEnv(t, f, false)
	e.deps.Cfg.Paths.SyslogArchive = t.TempDir() // 昨天的归档还没下载
	res, text := call(t, connect(t, e.deps), "syslog_read", map[string]any{"date": day(-1).Format("2006-01-02")})
	if res.IsError || !strings.Contains(text, "router-yesterday") || strings.Contains(text, "router-today") ||
		!strings.Contains(text, `"source": "router"`) || !strings.Contains(text, `"note"`) {
		t.Fatalf("isError=%v text=%s", res.IsError, text)
	}
	if !strings.Contains(f.CallsFor("syslog_fetch")[0].Cmd, `"$p-1"`) {
		t.Fatal("回退到路由器时应同时读取轮转日志")
	}
}

func TestSyslogTodayReadsRouter(t *testing.T) {
	f := runnertest.New().On("syslog_fetch", routerSyslog())
	e := newEnv(t, f, false)
	e.deps.Cfg.Paths.SyslogArchive = t.TempDir()
	res, text := call(t, connect(t, e.deps), "syslog_read", map[string]any{"date": day(0).Format("2006-01-02")})
	if res.IsError || !strings.Contains(text, "router-today") || strings.Contains(text, "router-yesterday") || !strings.Contains(text, `"source": "router"`) {
		t.Fatalf("isError=%v text=%s", res.IsError, text)
	}
}

func TestSyslogRejectsBadDates(t *testing.T) {
	e := newEnv(t, runnertest.New(), false)
	e.deps.Cfg.Paths.SyslogArchive = t.TempDir()
	cs := connect(t, e.deps)
	for _, args := range []map[string]any{
		{"date": "2026-1-5"},
		{"date": "../../etc/passwd"},
		{"date": day(1).Format("2006-01-02")},
		{"date": day(0).Format("2006-01-02"), "since": "1h"},
		{"date": day(-3).Format("2006-01-02")}, // 本地没有这一天的归档
	} {
		res, text := call(t, cs, "syslog_read", args)
		if !res.IsError || !strings.Contains(text, string(apperr.InvalidArgument)) {
			t.Errorf("args=%v 应返回 INVALID_ARGUMENT: %s", args, text)
		}
	}
}

func TestSyslogHistoryWithoutArchiveConfigured(t *testing.T) {
	cs := connect(t, newEnv(t, runnertest.New(), false).deps)
	res, text := call(t, cs, "syslog_read", map[string]any{"date": day(-3).Format("2006-01-02")})
	if !res.IsError || !strings.Contains(text, "paths.syslog_archive") {
		t.Fatalf("isError=%v text=%s", res.IsError, text)
	}
}
