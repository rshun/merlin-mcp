package dnsmasq

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

const original = "dhcp-mac=set:openwrt,AA:BB:CC:00:00:01\n"

func TestReadMissingFile(t *testing.T) {
	f := newFakeRouter()
	m, _ := newManager(t, f)
	v, err := m.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Exists || len(v.Lines) != 0 || v.SHA256 != SHA256("") {
		t.Fatalf("v = %+v", v)
	}
}

func TestReadNumbersLines(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original+"address=/a.example/0.0.0.0\n"
	m, _ := newManager(t, f)
	v, _ := m.Read(context.Background())
	if !v.Exists || len(v.Lines) != 2 || v.Lines[1].N != 2 || v.Lines[1].Text != "address=/a.example/0.0.0.0" {
		t.Fatalf("v = %+v", v)
	}
}

func TestAddWritesBackupAndSetsBaseline(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	res, err := m.Edit(context.Background(), OpAdd, []string{"address=/a.example/0.0.0.0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.content != original+"address=/a.example/0.0.0.0\n" {
		t.Fatalf("content = %q", f.content)
	}
	if !res.Changed || !res.PendingApply || res.Backup == "" || f.backups[res.Backup] != original {
		t.Fatalf("res = %+v", res)
	}
	st := store.Get().Dnsmasq
	if st.KnownGoodBackup != res.Backup || st.LastMCPSHA256 != SHA256(f.content) || res.SHA256 != st.LastMCPSHA256 {
		t.Fatalf("state = %+v", st)
	}
	if !strings.Contains(res.Diff, "+address=/a.example/0.0.0.0") {
		t.Fatalf("diff = %s", res.Diff)
	}
}

func TestAddExistingLineDoesNotWrite(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	res, err := m.Edit(context.Background(), OpAdd, []string{strings.TrimSpace(original)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || len(res.Skipped) != 1 || len(f.CallsFor("addfile_write")) != 0 || len(f.CallsFor("addfile_backup")) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestDryRunDoesNotWrite(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f)
	res, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || !res.Changed || res.PendingApply || !strings.Contains(res.Diff, "+x=1") {
		t.Fatalf("res = %+v", res)
	}
	if f.content != original || len(f.CallsFor("addfile_write")) != 0 || store.Get().Dnsmasq.KnownGoodBackup != "" {
		t.Fatal("dry_run 不应写文件、备份或修改状态")
	}
}

func TestRemoveReportsNotFound(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original+"x=1\n"
	m, _ := newManager(t, f)
	res, err := m.Edit(context.Background(), OpRemove, []string{"x=1", "y=2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if f.content != original || len(res.Removed) != 1 || len(res.NotFound) != 1 || res.NotFound[0] != "y=2" {
		t.Fatalf("content=%q res=%+v", f.content, res)
	}
}

func TestExternalChangeDetectedOnEdit(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	if _, err := m.Edit(context.Background(), OpAdd, []string{"x=1"}, false); err != nil {
		t.Fatal(err)
	}
	f.content += "manual=1\n" // 有人绕过 MCP 修改了文件
	res, err := m.Edit(context.Background(), OpAdd, []string{"y=2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ExternalChangeDetected {
		t.Fatal("应检测到外部修改")
	}
}

func TestContentTravelsOnlyViaStdin(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, _ := newManager(t, f)
	evil := "address=/$(reboot)/`id`/'x'"
	if _, err := m.Edit(context.Background(), OpAdd, []string{evil}, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls() {
		if strings.Contains(c.Cmd, "reboot") || strings.Contains(c.Cmd, "`id`") {
			t.Fatalf("配置内容出现在命令行中: %s", c.Cmd)
		}
	}
	w := f.CallsFor("addfile_write")
	if len(w) != 1 || !strings.Contains(string(w[0].Stdin), evil) {
		t.Fatalf("写入内容应通过 stdin 传输: %+v", w)
	}
}

func TestAddRejectsDangerousOptionButRemoveAllowsIt(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original+"dhcp-script=/old\n"
	m, _ := newManager(t, f)
	_, err := m.Edit(context.Background(), OpAdd, []string{"dhcp-script=/sbin/reboot"}, false)
	if apperr.CodeOf(err) != apperr.InvalidArgument || len(f.Calls()) != 0 {
		t.Fatalf("添加危险选项应在执行任何命令前被拒绝: err=%v calls=%d", err, len(f.Calls()))
	}
	if _, err := m.Edit(context.Background(), OpRemove, []string{"dhcp-script=/old"}, false); err != nil {
		t.Fatalf("删除危险选项应被允许: %v", err)
	}
}

func TestEditPrunesOldBackupsButKeepsKnownGood(t *testing.T) {
	f := newFakeRouter()
	f.exists, f.content = true, original
	m, store := newManager(t, f) // Keep = 3
	for i := 0; i < 6; i++ {
		if _, err := m.Edit(context.Background(), OpAdd, []string{strings.Repeat("x", i+1) + "=1"}, false); err != nil {
			t.Fatal(err)
		}
	}
	kg := store.Get().Dnsmasq.KnownGoodBackup
	if _, ok := f.backups[kg]; !ok {
		t.Fatal("known good 备份不能被清理")
	}
	if len(f.backups) != 4 { // 3 份最新的 + 1 份 known good
		t.Fatalf("backups = %d: %v", len(f.backups), f.backups)
	}
}

func TestPruneList(t *testing.T) {
	names := []string{
		"dnsmasq.conf.add.20261001-000000",
		"dnsmasq.conf.add.20261002-000000",
		"dnsmasq.conf.add.20261002-000000-2",
		"dnsmasq.conf.add.20261003-000000",
		"other-file",
	}
	got := PruneList(names, 2, "dnsmasq.conf.add.20261001-000000")
	if len(got) != 1 || got[0] != "dnsmasq.conf.add.20261002-000000" {
		t.Fatalf("got = %v", got)
	}
	if PruneList(names, 10, "") != nil {
		t.Fatal("数量未超过 keep 时不应清理")
	}
}

func TestEffectiveFiltersByKeyword(t *testing.T) {
	f := newFakeRouter()
	f.effective = "pid-file=/var/run/dnsmasq.pid\nDHCP-MAC=set:openwrt,x\nport=53\n"
	m, _ := newManager(t, f)
	got, err := m.Effective(context.Background(), "dhcp-mac")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].N != 2 {
		t.Fatalf("got = %+v", got)
	}
	all, _ := m.Effective(context.Background(), "")
	if len(all) != 3 {
		t.Fatalf("all = %+v", all)
	}
}
