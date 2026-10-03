package dnsmasq

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/state"
)

// fakeRouter 在内存中模拟路由器上与 dnsmasq 相关的文件和进程。
// 内容包含 "bad-option" 时，重启后 dnsmasq 进程不会运行。
type fakeRouter struct {
	*runnertest.Fake
	exists         bool
	content        string
	backups        map[string]string
	seq            int
	pid            int
	running        bool
	testExit       int
	testOutput     string
	healthOK       bool
	restartIgnored bool // service 命令返回成功，但 dnsmasq 实际没有重启
	effective      string
	pidSeq         []string // 非空时 pidof 依次返回这些值，用完后回到按 pid/running 计算
	healthSeq      []bool   // 非空时健康检查依次返回这些结果（模拟残留的旧进程仍在应答）
}

var nameInCmd = regexp.MustCompile(`dnsmasq\.conf\.add\.\d{8}-\d{6}(-\d+)?`)

func out(s string) runner.Result { return runner.Result{Stdout: []byte(s)} }

func newFakeRouter() *fakeRouter {
	f := &fakeRouter{Fake: runnertest.New(), backups: map[string]string{}, pid: 100, running: true, healthOK: true}
	f.On("addfile_read", func(string, []byte) (runner.Result, error) {
		if !f.exists {
			return out("MISSING\n"), nil
		}
		return out("EXISTS\n" + f.content), nil
	})
	f.On("addfile_write", func(_ string, stdin []byte) (runner.Result, error) {
		f.exists, f.content = true, string(stdin)
		return out(""), nil
	})
	f.On("addfile_backup", func(_ string, stdin []byte) (runner.Result, error) {
		f.seq++
		name := fmt.Sprintf("dnsmasq.conf.add.20261003-1200%02d", f.seq)
		f.backups[name] = string(stdin)
		return out("/jffs/merlin-mcp/backups/" + name + "\n"), nil
	})
	f.On("backup_read", func(cmd string, _ []byte) (runner.Result, error) {
		c, ok := f.backups[nameInCmd.FindString(cmd)]
		if !ok {
			return runner.Result{ExitCode: 1, Stderr: []byte("No such file")}, nil
		}
		return out(c), nil
	})
	f.On("backup_list", func(string, []byte) (runner.Result, error) {
		var names []string
		for n := range f.backups {
			names = append(names, n)
		}
		sort.Strings(names)
		return out(strings.Join(names, "\n") + "\n"), nil
	})
	f.On("backup_prune", func(cmd string, _ []byte) (runner.Result, error) {
		for _, n := range nameInCmd.FindAllString(cmd, -1) {
			delete(f.backups, n)
		}
		return out(""), nil
	})
	f.On("dnsmasq_test", func(string, []byte) (runner.Result, error) {
		return runner.Result{ExitCode: f.testExit, Stdout: []byte(f.testOutput)}, nil
	})
	f.On("pidof_dnsmasq", func(string, []byte) (runner.Result, error) {
		if len(f.pidSeq) > 0 {
			v := f.pidSeq[0]
			f.pidSeq = f.pidSeq[1:]
			return out(v + "\n"), nil
		}
		if !f.running {
			return out(""), nil
		}
		return out(fmt.Sprintf("%d\n", f.pid)), nil
	})
	f.On("restart_dnsmasq", func(string, []byte) (runner.Result, error) {
		if !f.restartIgnored {
			f.pid++
			f.running = !strings.Contains(f.content, "bad-option")
		}
		return out(""), nil
	})
	f.On("dnsmasq_health", func(string, []byte) (runner.Result, error) {
		ok := f.running && f.healthOK
		if len(f.healthSeq) > 0 {
			ok, f.healthSeq = f.healthSeq[0], f.healthSeq[1:]
		}
		if ok {
			return out("Server: 127.0.0.1\nAddress 1: 127.0.0.1\n\nName: router.asus.com\nAddress 1: 192.0.2.1\n"), nil
		}
		return runner.Result{ExitCode: 1, Stdout: []byte("nslookup: can't resolve 'router.asus.com'")}, nil
	})
	f.On("dnsmasq_effective", func(string, []byte) (runner.Result, error) { return out(f.effective), nil })
	return f
}

func newManager(t *testing.T, f *fakeRouter) (*Manager, *state.Store) {
	t.Helper()
	return newManagerWithRunner(t, f)
}

// ctxRunner 模拟真实 SSH Runner 的行为：ctx 已结束时命令直接超时失败。
type ctxRunner struct{ inner runner.Runner }

func (c ctxRunner) Run(ctx context.Context, cmd string, stdin []byte) (runner.Result, error) {
	if ctx.Err() != nil {
		return runner.Result{}, apperr.New(apperr.Timeout, "命令执行超时", "")
	}
	return c.inner.Run(ctx, cmd, stdin)
}

func newManagerWithRunner(t *testing.T, r runner.Runner) (*Manager, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(r, Options{
		AddPath:      "/jffs/configs/dnsmasq.conf.add",
		BackupDir:    "/jffs/merlin-mcp/backups",
		HealthDomain: "router.asus.com",
		Keep:         3,
		Store:        store,
		Sleep:        func(time.Duration) {},
	})
	return m, store
}
