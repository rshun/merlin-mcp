package syslog

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var cst = time.FixedZone("router", 8*3600)

func TestParseBusyboxFormat(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	raw := "Oct  3 11:58:01 dnsmasq-dhcp[1234]: DHCPACK(br0) 192.0.2.10 AA:BB:CC:00:00:01 phone\n" +
		"Oct  3 11:59:00 kernel: eth6: link up\n" +
		"Oct  3 11:59:30 RT-AX86U-1234 rc_service: httpd 1234:notify_rc restart_dnsmasq\n"
	lines := Parse(raw, now)
	if len(lines) != 3 {
		t.Fatalf("len = %d", len(lines))
	}
	if lines[0].Process != "dnsmasq-dhcp" || !lines[0].HasTime || !lines[0].Time.Equal(time.Date(2026, 10, 3, 11, 58, 1, 0, cst)) {
		t.Errorf("line0 = %+v", lines[0])
	}
	if lines[1].Process != "kernel" {
		t.Errorf("line1 process = %q", lines[1].Process)
	}
	if lines[2].Process != "rc_service" {
		t.Errorf("带主机名的行 process = %q", lines[2].Process)
	}
}

func TestParseISOFormat(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	lines := Parse("2026-10-03T11:00:00+08:00 RT-AX86U dnsmasq[99]: started\n", now)
	if len(lines) != 1 || lines[0].Process != "dnsmasq" || lines[0].Time.Hour() != 11 {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestParseInfersPreviousYear(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 10, 0, 0, cst)
	lines := Parse("Dec 31 23:59:00 kernel: old\nJan  1 00:05:00 kernel: new\n", now)
	if lines[0].Time.Year() != 2025 || lines[1].Time.Year() != 2026 {
		t.Fatalf("years = %d %d", lines[0].Time.Year(), lines[1].Time.Year())
	}
}

func TestParseWithUnsyncedClock(t *testing.T) {
	now := time.Unix(300, 0).In(cst) // 1970-01-01，路由器还没同步 NTP
	lines := Parse("Jan  1 08:01:00 kernel: boot\nOct  3 11:00:00 kernel: future\n", now)
	if len(lines) != 2 || !lines[0].HasTime || !lines[1].HasTime {
		t.Fatalf("lines = %+v", lines)
	}
	if lines[1].Time.After(now) {
		t.Fatal("推断出的时间不应晚于路由器当前时间")
	}
}

func TestParseLineWithoutTimestampInheritsPrevious(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	lines := Parse("orphan before any time\nOct  3 11:00:00 kernel: a\ncontinuation line\n", now)
	if lines[0].HasTime {
		t.Error("第一行之前没有时间，HasTime 应为 false")
	}
	if !lines[2].HasTime || !lines[2].Time.Equal(lines[1].Time) {
		t.Errorf("续行应继承前一行时间: %+v", lines[2])
	}
}

func TestApplyFilters(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	raw := "Oct  3 10:00:00 dnsmasq[1]: old\n" +
		"Oct  3 11:30:00 dnsmasq[1]: Query Example.com\n" +
		"Oct  3 11:40:00 dnsmasq-dhcp[2]: DHCPACK example\n" +
		"Oct  3 11:50:00 kernel: EXAMPLE kernel\n"
	lines := Parse(raw, now)

	got := Apply(lines, Filter{Since: time.Hour}, now)
	if len(got) != 3 {
		t.Errorf("since=1h 应得到 3 行，得到 %d", len(got))
	}
	got = Apply(lines, Filter{Process: "dnsmasq"}, now)
	if len(got) != 2 {
		t.Errorf("process=dnsmasq 应精确匹配 2 行，得到 %d", len(got))
	}
	got = Apply(lines, Filter{Keyword: "example"}, now)
	if len(got) != 3 {
		t.Errorf("keyword 不区分大小写应得到 3 行，得到 %d", len(got))
	}
	got = Apply(lines, Filter{Lines: 2}, now)
	if len(got) != 2 || !strings.Contains(got[1].Raw, "kernel") {
		t.Errorf("lines=2 应保留最新的 2 行: %+v", got)
	}
}

func TestApplySinceDropsLinesWithoutTime(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, cst)
	lines := Parse("no time at all\n", now)
	if got := Apply(lines, Filter{Since: time.Hour}, now); len(got) != 0 {
		t.Fatalf("没有时间的行在 since 过滤下应被排除: %+v", got)
	}
}

func TestParseSince(t *testing.T) {
	ok := map[string]time.Duration{"": 0, "30m": 30 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour}
	for in, want := range ok {
		got, err := ParseSince(in)
		if err != nil || got != want {
			t.Errorf("ParseSince(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"0m", "8d", "5x", "1w", "-1h", "1.5h", "h"} {
		if _, err := ParseSince(in); err == nil {
			t.Errorf("ParseSince(%q) 应失败", in)
		}
	}
}

func TestRenderKeepsNewestLines(t *testing.T) {
	lines := []string{"aaaa", "bbbb", "cccc"}
	text, truncated := Render(lines, 10)
	if text != "bbbb\ncccc\n" || !truncated {
		t.Fatalf("text=%q truncated=%v", text, truncated)
	}
	text, truncated = Render(lines, 100)
	if text != "aaaa\nbbbb\ncccc\n" || truncated {
		t.Fatalf("text=%q truncated=%v", text, truncated)
	}
	if text, truncated := Render(nil, 10); text != "" || truncated {
		t.Fatalf("空输入: %q %v", text, truncated)
	}
}

func TestRenderKeepsValidUTF8(t *testing.T) {
	huge := strings.Repeat("中", 30000) // 90000 字节的单行
	text, truncated := Render([]string{huge}, MaxOutputBytes)
	if !truncated || !utf8.ValidString(text) || len(text) > MaxOutputBytes {
		t.Fatalf("truncated=%v valid=%v len=%d", truncated, utf8.ValidString(text), len(text))
	}
	text, _ = Render([]string{"abc\xff\xfedef"}, MaxOutputBytes)
	if !utf8.ValidString(text) {
		t.Fatal("非法 UTF-8 应被替换")
	}
}

func TestFilterText(t *testing.T) {
	lines := []string{"[1.0] eth6: link up", "[2.0] Out of memory: Kill process", "[3.0] ETH6 down"}
	got := FilterText(lines, "eth6", 0)
	if len(got) != 2 {
		t.Errorf("got %v", got)
	}
	got = FilterText(lines, "", 1)
	if len(got) != 1 || got[0] != "[3.0] ETH6 down" {
		t.Errorf("got %v", got)
	}
}
