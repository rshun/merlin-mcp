package tools_test

import (
	"context"
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
