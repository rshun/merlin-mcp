package dnsmasq

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
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
