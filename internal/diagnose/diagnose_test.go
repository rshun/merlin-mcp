package diagnose

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

const pingOut = `PING example.com (192.0.2.80): 56 data bytes
64 bytes from 192.0.2.80: seq=0 ttl=55 time=12.345 ms

--- example.com ping statistics ---
3 packets transmitted, 2 packets received, 33% packet loss
round-trip min/avg/max = 10.000/12.500/15.000 ms`

func TestRunRejectsInvalidInput(t *testing.T) {
	f := runnertest.New()
	cases := []Request{
		{Action: "ping", Target: "-c 100 example.com"},
		{Action: "ping", Target: "$(reboot)"},
		{Action: "ping", Target: "example.com", Count: 6},
		{Action: "nslookup", Target: "example.com", Server: "dns.example"},
		{Action: "traceroute", Target: "example.com"},
	}
	for _, c := range cases {
		_, err := Run(context.Background(), f, c)
		if apperr.CodeOf(err) != apperr.InvalidArgument {
			t.Errorf("%+v 应返回 INVALID_ARGUMENT，得到 %v", c, err)
		}
	}
	if len(f.Calls()) != 0 {
		t.Fatal("参数非法时不应执行任何命令")
	}
}

func TestRunPing(t *testing.T) {
	f := runnertest.New().On("ping", runnertest.Stdout(pingOut))
	res, err := Run(context.Background(), f, Request{Action: "ping", Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if *res.PacketLossPercent != 33 || *res.AvgRTTMs != 12.5 {
		t.Fatalf("res = %+v", res)
	}
	if cmd := f.CallsFor("ping")[0].Cmd; !strings.Contains(cmd, "ping -c 3 -W 2 'example.com'") {
		t.Fatalf("cmd = %s", cmd)
	}
}

func TestRunPingTotalLossIsNotError(t *testing.T) {
	f := runnertest.New().On("ping", runnertest.Exit(1, "3 packets transmitted, 0 packets received, 100% packet loss\n", ""))
	res, err := Run(context.Background(), f, Request{Action: "ping", Target: "192.0.2.1", Count: 3})
	if err != nil || res.ExitCode != 1 || *res.PacketLossPercent != 100 || res.AvgRTTMs != nil {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunNslookupDefaultsToLocalServer(t *testing.T) {
	out := "Server:    127.0.0.1\nAddress 1: 127.0.0.1 localhost\n\nName:      example.com\nAddress 1: 192.0.2.80\nAddress 2: 2001:db8::80\n"
	f := runnertest.New().On("nslookup", runnertest.Stdout(out))
	res, err := Run(context.Background(), f, Request{Action: "nslookup", Target: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resolved) != 2 || res.Resolved[1] != "2001:db8::80" {
		t.Fatalf("resolved = %v", res.Resolved)
	}
	if cmd := f.CallsFor("nslookup")[0].Cmd; !strings.Contains(cmd, "nslookup 'example.com' '127.0.0.1'") {
		t.Fatalf("cmd = %s", cmd)
	}
}

func TestParseNslookupNewBusyboxFormat(t *testing.T) {
	out := "Server:\t\t127.0.0.1\nAddress:\t127.0.0.1:53\n\nName:\trouter.asus.com\nAddress: 192.0.2.1\n"
	if got := ParseNslookup(out); len(got) != 1 || got[0] != "192.0.2.1" {
		t.Fatalf("got = %v", got)
	}
	if got := ParseNslookup("** server can't find nope.example: NXDOMAIN\n"); len(got) != 0 {
		t.Fatalf("解析失败时应为空: %v", got)
	}
}
