package dnsmasq

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
)

func TestApplySuccessUpdatesKnownGood(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"address=/a.example/0.0.0.0"}, false); err != nil {
		t.Fatal(err)
	}
	res, err := m.Apply(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldPID == res.NewPID || res.HealthCheck != "ok" || res.ValidationSkipped != "" {
		t.Fatalf("res = %+v", res)
	}
	st := store.Get().Dnsmasq
	if st.KnownGoodBackup != res.KnownGoodBackup || f.backups[res.KnownGoodBackup] != f.content || st.LastMCPSHA256 != SHA256(f.content) {
		t.Fatalf("state=%+v res=%+v", st, res)
	}
}

func TestApplyValidationFailureDoesNotRestart(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.testExit, f.testOutput = 1, "dnsmasq: bad option at line 1 of /jffs/configs/dnsmasq.conf.add"
	m, _ := newManager(t, f)
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqValidationFailed || !strings.Contains(e.Details["output"].(string), "bad option") {
		t.Fatalf("err = %+v", e)
	}
	if len(f.CallsFor("restart_dnsmasq")) != 0 {
		t.Fatal("校验失败时不应重启 dnsmasq")
	}
}

func TestApplySkipsValidationWhenUnsupported(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.testExit, f.testOutput = 1, "dnsmasq: unrecognized option '--test'"
	m, _ := newManager(t, f)
	res, err := m.Apply(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ValidationSkipped == "" {
		t.Fatal("不支持 --test 时应跳过校验并注明")
	}
}

func TestApplyRollsBackWhenDnsmasqDies(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"bad-option=1"}, false); err != nil {
		t.Fatal(err)
	}
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRolledBack || e.Details["cause"] == nil {
		t.Fatalf("err = %+v", e)
	}
	if f.content != original || !f.running {
		t.Fatalf("应回滚到原始内容并恢复运行: content=%q running=%v", f.content, f.running)
	}
	if store.Get().Dnsmasq.LastMCPSHA256 != SHA256(original) {
		t.Fatal("回滚后 last_mcp_sha256 应为 known good 的 sha256")
	}
}

func TestApplyReportsRollbackFailure(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.healthOK = false
	m, _ := newManager(t, f)
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRollbackFailed || e.Details["backup_path"] == nil || e.Details["rollback_error"] == nil {
		t.Fatalf("err = %+v", e)
	}
}

func TestApplyDetectsRestartThatDidNotHappen(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	f.restartIgnored = true
	m, _ := newManager(t, f)
	_, err := m.Apply(context.Background(), false)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRollbackFailed || !strings.Contains(e.Details["cause"].(string), "没有以新进程重新启动") {
		t.Fatalf("err = %+v", e)
	}
}

// Asus 上 dnsmasq 通常有两个进程。重启时只退出了一个，剩下的旧进程不能被当作新进程。
func TestApplyDoesNotTreatSurvivingOldPIDAsNew(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"bad-option=1"}, false); err != nil {
		t.Fatal(err)
	}
	kgBefore := store.Get().Dnsmasq.KnownGoodBackup
	f.pidSeq = []string{"101 100", "100"} // 重启前两个进程；第一次轮询时只剩旧的 100
	f.healthSeq = []bool{true}            // 旧进程 100 仍然能应答解析
	_, err := m.Apply(context.Background(), false)
	if apperr.CodeOf(err) != apperr.DnsmasqRolledBack {
		t.Fatalf("残留的旧进程不应被当成新进程，err = %v", err)
	}
	if store.Get().Dnsmasq.KnownGoodBackup != kgBefore || f.content != original {
		t.Fatalf("坏配置不能成为 known good: kg=%s content=%q", store.Get().Dnsmasq.KnownGoodBackup, f.content)
	}
}

// 调用方取消（或 60 秒预算耗尽）发生在重启 dnsmasq 之后时，回滚仍必须完成。
func TestApplyRollsBackEvenIfContextCancelledAfterRestart(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManagerWithRunner(t, ctxRunner{f})
	if _, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := false
	f.On("restart_dnsmasq", func(string, []byte) (runner.Result, error) {
		f.pid++
		f.running = !strings.Contains(f.content, "bad-option")
		if !cancelled {
			cancelled = true
			cancel()
		}
		return out(""), nil
	})
	_, err := m.Apply(ctx, false)
	if apperr.CodeOf(err) != apperr.DnsmasqRolledBack {
		t.Fatalf("取消后应仍能完成回滚，err = %v", err)
	}
	if f.content != original {
		t.Fatalf("应恢复 known good: %q", f.content)
	}
}

// 回滚会覆盖当前内容，被覆盖的内容（包括人工修改）必须先备份。
func TestRollbackBacksUpReplacedContent(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, false); err != nil {
		t.Fatal(err)
	}
	f.content += "manual=1\n" // 人工修改，只存在于 .add 文件中
	f.healthSeq = []bool{false}
	_, err := m.Apply(context.Background(), true)
	e := apperr.From(err)
	if e.Code != apperr.DnsmasqRolledBack {
		t.Fatalf("err = %+v", e)
	}
	saved, _ := e.Details["failed_content_backup"].(string)
	if saved == "" {
		t.Fatalf("应在 details.failed_content_backup 中给出被替换内容的备份: %+v", e.Details)
	}
	found := false
	for _, c := range f.backups {
		if strings.Contains(c, "manual=1") {
			found = true
		}
	}
	if !found {
		t.Fatal("人工修改的内容应存在于某个备份中")
	}
}

func TestApplyRefusesExternalChangeUnlessAccepted(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, false); err != nil {
		t.Fatal(err)
	}
	f.content += "manual=1\n"
	_, err := m.Apply(context.Background(), false)
	if apperr.CodeOf(err) != apperr.AddFileChangedExternally {
		t.Fatalf("err = %v", err)
	}
	if len(f.CallsFor("restart_dnsmasq")) != 0 {
		t.Fatal("拒绝时不应重启")
	}
	if _, err := m.Apply(context.Background(), true); err != nil {
		t.Fatalf("accept_external_changes=true 应继续: %v", err)
	}
}

func TestApplyWithoutPriorEditCreatesBaseline(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	if _, err := m.Apply(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if store.Get().Dnsmasq.KnownGoodBackup == "" {
		t.Fatal("应记录 known good")
	}
}
