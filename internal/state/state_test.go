package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpenEmptyDirGivesZeroState(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "sub"))
	if err != nil {
		t.Fatal(err)
	}
	st := s.Get()
	if len(st.Reboot.History) != 0 || st.Dnsmasq.KnownGoodBackup != "" {
		t.Fatalf("state = %+v", st)
	}
}

func TestUpdatePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	err := s.Update(func(st *State) error {
		st.Reboot.History = append(st.Reboot.History, at)
		st.Dnsmasq.KnownGoodBackup = "dnsmasq.conf.add.20261003-120000"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := s2.Get()
	if len(st.Reboot.History) != 1 || !st.Reboot.History[0].Equal(at) || st.Dnsmasq.KnownGoodBackup != "dnsmasq.conf.add.20261003-120000" {
		t.Fatalf("reopened state = %+v", st)
	}
}

func TestUpdateErrorKeepsState(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	boom := errors.New("boom")
	err := s.Update(func(st *State) error {
		st.Dnsmasq.LastMCPSHA256 = "changed"
		return boom
	})
	if err != boom {
		t.Fatalf("err = %v", err)
	}
	if s.Get().Dnsmasq.LastMCPSHA256 != "" {
		t.Fatal("fn 返回错误时内存状态不应改变")
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("fn 返回错误时不应写文件")
	}
}

func TestOpenCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "已损坏") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetReturnsDeepCopy(t *testing.T) {
	s, _ := Open(t.TempDir())
	_ = s.Update(func(st *State) error {
		st.Reboot.History = []time.Time{time.Unix(1, 0)}
		return nil
	})
	got := s.Get()
	got.Reboot.History[0] = time.Unix(2, 0)
	if s.Get().Reboot.History[0].Unix() != 1 {
		t.Fatal("修改 Get 的返回值不应影响内部状态")
	}
}

func TestStateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 权限位")
	}
	dir := t.TempDir()
	s, _ := Open(dir)
	_ = s.Update(func(*State) error { return nil })
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o", info.Mode().Perm())
	}
}
