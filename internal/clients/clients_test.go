package clients

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

func TestParseLeasesSkipsMalformed(t *testing.T) {
	s := "1791003600 aa:bb:cc:00:00:01 192.0.2.10 phone 01:aa:bb:cc:00:00:01\n" +
		"0 AA:BB:CC:00:00:02 192.0.2.11 * *\n" +
		"garbage line\n" +
		"notanumber AA:BB:CC:00:00:03 192.0.2.12 x\n" +
		"1791003600 not-a-mac 192.0.2.13 y\n"
	got := ParseLeases(s)
	if len(got) != 2 {
		t.Fatalf("len = %d: %+v", len(got), got)
	}
	if got[0].MAC != "AA:BB:CC:00:00:01" || got[0].Hostname != "phone" || got[0].Expiry != 1791003600 {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Hostname != "" || got[1].Expiry != 0 {
		t.Errorf("主机名 * 应转为空: %+v", got[1])
	}
}

func TestParseARP(t *testing.T) {
	s := "IP address       HW type     Flags       HW address            Mask     Device\n" +
		"192.0.2.10       0x1         0x2         aa:bb:cc:00:00:01     *        br0\n" +
		"192.0.2.99       0x1         0x0         00:00:00:00:00:00     *        br0\n"
	got := ParseARP(s)
	if len(got) != 1 || got[0].MAC != "AA:BB:CC:00:00:01" || got[0].Device != "br0" {
		t.Fatalf("got = %+v", got)
	}
}

func TestParseStaticList(t *testing.T) {
	// 386 格式：<MAC>IP>>，主机名在 dhcp_hostnames；388 格式：<MAC>IP>DNS>主机名
	m := ParseStaticList("<AA:BB:CC:00:00:01>192.0.2.10>><aa:bb:cc:00:00:02>192.0.2.11>>nas", "<AA:BB:CC:00:00:01>phone<AA:BB:CC:00:00:09>ghost")
	if m["AA:BB:CC:00:00:01"] != (Static{IP: "192.0.2.10", Hostname: "phone"}) {
		t.Errorf("01 = %+v", m["AA:BB:CC:00:00:01"])
	}
	if m["AA:BB:CC:00:00:02"] != (Static{IP: "192.0.2.11", Hostname: "nas"}) {
		t.Errorf("02 = %+v", m["AA:BB:CC:00:00:02"])
	}
	if _, ok := m["AA:BB:CC:00:00:09"]; ok {
		t.Error("只在 dhcp_hostnames 中出现的 MAC 不算静态分配")
	}
}

func TestParseAssoc(t *testing.T) {
	got := ParseAssoc("assoclist AA:BB:CC:00:00:01\nassoclist aa:bb:cc:00:00:02\nwl: error\n")
	if len(got) != 2 || got[1] != "AA:BB:CC:00:00:02" {
		t.Fatalf("got = %v", got)
	}
}

func TestMerge(t *testing.T) {
	now := time.Unix(1791000000, 0)
	in := Inputs{
		Now: now,
		Leases: []Lease{
			{Expiry: 1791000600, MAC: "AA:BB:CC:00:00:01", IP: "192.0.2.10", Hostname: "phone"},
			{Expiry: 0, MAC: "AA:BB:CC:00:00:03", IP: "192.0.2.30", Hostname: "printer"},
		},
		ARP: []ARPEntry{
			{IP: "192.0.2.10", MAC: "AA:BB:CC:00:00:01", Device: "br0"},
			{IP: "192.0.2.20", MAC: "AA:BB:CC:00:00:02", Device: "br0"},
		},
		Static:   map[string]Static{"AA:BB:CC:00:00:04": {IP: "192.0.2.5", Hostname: "offline-nas"}, "AA:BB:CC:00:00:02": {IP: "192.0.2.20"}},
		Wireless: map[string]string{"AA:BB:CC:00:00:01": "5G"},
	}
	got := Merge(in)
	if len(got) != 4 {
		t.Fatalf("len = %d", len(got))
	}
	// 按 IP 排序：.5 .10 .20 .30
	order := []string{"AA:BB:CC:00:00:04", "AA:BB:CC:00:00:01", "AA:BB:CC:00:00:02", "AA:BB:CC:00:00:03"}
	for i, mac := range order {
		if got[i].MAC != mac {
			t.Fatalf("第 %d 个应为 %s，得到 %s", i, mac, got[i].MAC)
		}
	}
	phone := got[1]
	if phone.Connection != "5G" || phone.Hostname != "phone" || phone.LeaseRemainingSec == nil || *phone.LeaseRemainingSec != 600 || !phone.InARP {
		t.Errorf("phone = %+v", phone)
	}
	if got[2].Connection != "wired" || !got[2].Static {
		t.Errorf("有线静态设备 = %+v", got[2])
	}
	if got[3].Connection != "unknown" || !got[3].LeaseInfinite || got[3].LeaseRemainingSec != nil {
		t.Errorf("无限租约设备 = %+v", got[3])
	}
	if got[0].Connection != "unknown" || got[0].Hostname != "offline-nas" || !got[0].Static {
		t.Errorf("离线静态设备 = %+v", got[0])
	}
}

const baseOutput = "@@MERLINMCP:date@@\n1791000000 +0800\n" +
	"@@MERLINMCP:nvram@@\ndhcp_staticlist=\ndhcp_hostnames=\nwl0_ifname=eth6\nwl0_nband=2\nwl1_ifname=eth7\nwl1_nband=1\nwl2_ifname=\nwl2_nband=\nwl3_ifname=bad;name\nwl3_nband=4\n" +
	"@@MERLINMCP:leases@@\n1791000600 AA:BB:CC:00:00:01 192.0.2.10 phone *\n" +
	"@@MERLINMCP:arp@@\nIP address HW type Flags HW address Mask Device\n192.0.2.10 0x1 0x2 AA:BB:CC:00:00:01 * br0\n192.0.2.20 0x1 0x2 AA:BB:CC:00:00:02 * br0\n"

func TestFetchMergesWirelessBands(t *testing.T) {
	f := runnertest.New().
		On("clients_base", runnertest.Stdout(baseOutput)).
		On("clients_assoc", runnertest.Stdout("@@MERLINMCP:eth6@@\n@@MERLINMCP:eth7@@\nassoclist AA:BB:CC:00:00:01\n"))
	got, err := Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Connection != "5G" || got[1].Connection != "wired" {
		t.Fatalf("got = %+v", got)
	}
	cmd := f.CallsFor("clients_assoc")[0].Cmd
	if !strings.Contains(cmd, "wl -i 'eth6' assoclist") || strings.Contains(cmd, "bad;name") {
		t.Fatalf("非法接口名不应进入命令: %s", cmd)
	}
}

func TestFetchDegradesWhenWlFails(t *testing.T) {
	f := runnertest.New().
		On("clients_base", runnertest.Stdout(baseOutput)).
		On("clients_assoc", runnertest.Exit(127, "", "wl: not found"))
	got, err := Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Connection != "wired" || got[1].Connection != "wired" {
		t.Fatalf("wl 失败时在 ARP 中的设备应为 wired: %+v", got)
	}
}
