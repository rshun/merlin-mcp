package dnsmasq

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

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
		if f.running && f.healthOK {
			return out("Server: 127.0.0.1\nAddress 1: 127.0.0.1\n\nName: router.asus.com\nAddress 1: 192.0.2.1\n"), nil
		}
		return runner.Result{ExitCode: 1, Stdout: []byte("nslookup: can't resolve 'router.asus.com'")}, nil
	})
	f.On("dnsmasq_effective", func(string, []byte) (runner.Result, error) { return out(f.effective), nil })
	return f
}

func newManager(t *testing.T, f *fakeRouter) (*Manager, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(f, Options{
		AddPath:      "/jffs/configs/dnsmasq.conf.add",
		BackupDir:    "/jffs/merlin-mcp/backups",
		HealthDomain: "router.asus.com",
		Keep:         3,
		Store:        store,
		Sleep:        func(time.Duration) {},
	})
	return m, store
}
