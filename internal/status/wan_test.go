package status

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
	"github.com/rshun/merlin-mcp/internal/syslog"
)

const wanSample = "wan0_state_t=2\nwan0_sbstate_t=0\nwan0_auxstate_t=0\nwan0_proto=dhcp\nwan0_ipaddr=198.51.100.7\nwan0_gateway=198.51.100.1\nwan0_dns=198.51.100.53 198.51.100.54\n"

func TestParseWAN(t *testing.T) {
	w := ParseWAN(wanSample)
	if w.State != "connected" || w.StateCode != "2" || w.Proto != "dhcp" || w.IP != "198.51.100.7" || w.Gateway != "198.51.100.1" {
		t.Fatalf("w = %+v", w)
	}
	if len(w.DNS) != 2 || w.DNS[1] != "198.51.100.54" {
		t.Fatalf("dns = %v", w.DNS)
	}
	if ParseWAN("wan0_state_t=9\n").State != "unknown" {
		t.Fatal("未知状态码应为 unknown")
	}
	if d := ParseWAN("").DNS; d == nil || len(d) != 0 {
		t.Fatal("没有 DNS 时应返回空数组而不是 nil")
	}
}

func TestWANEvents(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	lines := syslog.Parse("Oct  3 10:00:00 WAN(0) Connection: WAN was restored.\n"+
		"Oct  3 10:01:00 dnsmasq[1]: query\n"+
		"Oct  3 10:02:00 wan: finish adding multi routes\n"+
		"Oct  3 10:03:00 udhcpc: bound to 198.51.100.7\n"+
		"Oct  3 10:04:00 kernel: swan is not wan\n", now)
	ev := WANEvents(lines, 2)
	// 匹配到 4 条（WAN(0)、wan:、udhcpc、末尾的单词 wan），保留最后 2 条
	if len(ev) != 2 || !strings.Contains(ev[0], "udhcpc") || !strings.Contains(ev[1], "kernel: swan") {
		t.Fatalf("events = %v", ev)
	}
	if e := WANEvents(nil, 20); e == nil || len(e) != 0 {
		t.Fatal("没有事件时应返回空数组")
	}
}

func TestFetchWAN(t *testing.T) {
	f := runnertest.New().
		On("wan_status", runnertest.Stdout(wanSample)).
		On("syslog_fetch", runnertest.Stdout("@@MERLINMCP:path@@\n/jffs/syslog.log\n@@MERLINMCP:date@@\n1791000000 +0800\n@@MERLINMCP:current@@\nOct  3 10:00:00 WAN(0) Connection: WAN was restored.\n"))
	w, err := FetchWAN(context.Background(), f, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "connected" || len(w.Events) != 1 || w.EventsError != "" {
		t.Fatalf("w = %+v", w)
	}
}

func TestFetchWANToleratesSyslogFailure(t *testing.T) {
	f := runnertest.New().
		On("wan_status", runnertest.Stdout(wanSample)).
		On("syslog_fetch", runnertest.Fail(errors.New("boom")))
	w, err := FetchWAN(context.Background(), f, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if w.EventsError == "" || len(w.Events) != 0 {
		t.Fatalf("w = %+v", w)
	}
}
