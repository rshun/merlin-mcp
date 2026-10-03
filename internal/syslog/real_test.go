package syslog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

// 真实样本来自 RT-AX86U / Merlin 388.12_2，已脱敏。
func readReal(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real", name))
	if err != nil {
		t.Fatalf("读取真实样本 %s 失败: %v", name, err)
	}
	return string(data)
}

func TestParseRealSyslog(t *testing.T) {
	now := time.Unix(1791013761, 0).In(time.FixedZone("router", 8*3600))
	lines := Parse(readReal(t, "syslog.txt"), now)
	if len(lines) != 30 {
		t.Fatalf("len = %d", len(lines))
	}
	procs := map[string]bool{}
	for _, l := range lines {
		if !l.HasTime || l.Time.After(now) || l.Time.Year() != now.Year() {
			t.Errorf("时间解析不正确: %+v", l)
		}
		procs[l.Process] = true
	}
	for _, p := range []string{"dnsmasq-dhcp", "wlceventd", "hostapd", "dropbear"} {
		if !procs[p] {
			t.Errorf("应识别出进程 %s，得到 %v", p, procs)
		}
	}
	if got := Apply(lines, Filter{Process: "hostapd"}, now); len(got) != 7 {
		t.Errorf("hostapd 行数 = %d，期望 7", len(got))
	}
}

// 真实 dmesg 带 ANSI 颜色转义，返回给 AI 前应去掉。
func TestFetchKernelStripsANSIRealSample(t *testing.T) {
	f := runnertest.New().On("dmesg", runnertest.Stdout(readReal(t, "dmesg.txt")))
	lines, err := FetchKernel(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 20 {
		t.Fatalf("len = %d", len(lines))
	}
	for _, l := range lines {
		if strings.Contains(l, "\x1b") {
			t.Fatalf("仍含 ANSI 转义: %q", l)
		}
	}
	if !strings.HasPrefix(lines[0], "FCACHEfc_vblog_list_add ERROR") {
		t.Fatalf("去掉转义后的内容不正确: %q", lines[0])
	}
}
