package reboot

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/state"
)

var shanghai, _ = time.LoadLocation("Asia/Shanghai")

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newQuota(t *testing.T, start time.Time) (*Quota, *state.Store, *clock) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: start}
	return NewQuota(store, shanghai, 1, c.now), store, c
}

func TestQuotaAllowsFirstAndRejectsSecondSameDay(t *testing.T) {
	q, store, c := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	if !q.Status().AllowedToday {
		t.Fatal("一开始应允许重启")
	}
	if _, err := q.Reserve(); err != nil {
		t.Fatal(err)
	}
	if len(store.Get().Reboot.History) != 1 {
		t.Fatal("Reserve 应落盘")
	}
	c.t = time.Date(2026, 10, 3, 23, 59, 0, 0, shanghai)
	_, err := q.Reserve()
	e := apperr.From(err)
	if e.Code != apperr.RebootQuotaExceeded || e.Details["last_reboot_at"] != "2026-10-03T09:00:00+08:00" {
		t.Fatalf("err = %+v", e)
	}
	s := q.Status()
	if s.AllowedToday || s.UsedToday != 1 || s.LastMCPRebootAt == nil {
		t.Fatalf("status = %+v", s)
	}
}

func TestQuotaResetsAtMidnightInConfiguredTimezone(t *testing.T) {
	q, _, c := newQuota(t, time.Date(2026, 10, 3, 23, 30, 0, 0, shanghai))
	if _, err := q.Reserve(); err != nil {
		t.Fatal(err)
	}
	c.t = time.Date(2026, 10, 4, 0, 10, 0, 0, shanghai)
	if _, err := q.Reserve(); err != nil {
		t.Fatalf("上海时间第二天应允许: %v", err)
	}
}

func TestQuotaUsesConfiguredTimezoneNotUTC(t *testing.T) {
	// 两次都是上海时间 10 月 4 日，但分属 UTC 的 10 月 3 日和 10 月 4 日
	q, _, c := newQuota(t, time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC))
	if _, err := q.Reserve(); err != nil {
		t.Fatal(err)
	}
	c.t = time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	if _, err := q.Reserve(); apperr.CodeOf(err) != apperr.RebootQuotaExceeded {
		t.Fatalf("按上海时间是同一天，应拒绝: %v", err)
	}
}

func TestHistoryIsTrimmed(t *testing.T) {
	q, store, c := newQuota(t, time.Date(2026, 1, 1, 12, 0, 0, 0, shanghai))
	for i := 0; i < 40; i++ {
		c.t = c.t.AddDate(0, 0, 1)
		if _, err := q.Reserve(); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(store.Get().Reboot.History); n != 30 {
		t.Fatalf("history = %d，应只保留 30 条", n)
	}
}

type resettableFake struct {
	*runnertest.Fake
	resets int
}

func (r *resettableFake) Reset() { r.resets++ }

func TestRebooterRecordsBeforeRunningAndResets(t *testing.T) {
	q, store, _ := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	f := &resettableFake{Fake: runnertest.New()}
	recordedBeforeRun := false
	f.On("reboot", func(string, []byte) (runner.Result, error) {
		recordedBeforeRun = len(store.Get().Reboot.History) == 1
		return runner.Result{}, nil
	})
	res, err := NewRebooter(q, f).Reboot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !recordedBeforeRun {
		t.Fatal("执行 reboot 命令前必须已经记录")
	}
	if f.resets != 1 || res.RequestedAt != "2026-10-03T09:00:00+08:00" {
		t.Fatalf("resets=%d res=%+v", f.resets, res)
	}
}

func TestRebooterCountsQuotaEvenIfCommandFails(t *testing.T) {
	q, _, _ := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	f := runnertest.New().On("reboot", runnertest.Fail(apperr.New(apperr.SSHUnreachable, "断开", "")))
	_, err := NewRebooter(q, f).Reboot(context.Background())
	e := apperr.From(err)
	if e.Code != apperr.SSHUnreachable || e.Details["quota_consumed"] != true {
		t.Fatalf("err = %+v", e)
	}
	if q.Status().AllowedToday {
		t.Fatal("命令失败也应计入额度")
	}
}

func TestRebooterDoesNotRunWhenQuotaExceeded(t *testing.T) {
	q, _, _ := newQuota(t, time.Date(2026, 10, 3, 9, 0, 0, 0, shanghai))
	f := runnertest.New().On("reboot", runnertest.Stdout(""))
	b := NewRebooter(q, f)
	if _, err := b.Reboot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Reboot(context.Background()); apperr.CodeOf(err) != apperr.RebootQuotaExceeded {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.CallsFor("reboot")); n != 1 {
		t.Fatalf("reboot 命令执行了 %d 次", n)
	}
}
