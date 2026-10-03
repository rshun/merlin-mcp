package clients

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/routercmd"
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

func TestParseStaticListRealSample(t *testing.T) {
	m := ParseStaticList(strings.TrimSpace(readReal(t, "dhcp_staticlist.txt")), "")
	if len(m) != 26 {
		t.Fatalf("静态分配条目数 = %d，期望 26", len(m))
	}
	named := 0
	for _, st := range m {
		if st.IP == "" {
			t.Errorf("IP 为空: %+v", st)
		}
		if st.Hostname == "laptop-1" {
			named++
		}
	}
	if named != 1 {
		t.Fatalf("388 格式的主机名应解析出 1 个，得到 %d", named)
	}
}

func TestParseAssocRealSample(t *testing.T) {
	if got := ParseAssoc(readReal(t, "assoclist.txt")); len(got) != 1 {
		t.Fatalf("got = %v", got)
	}
}

// Asus 的 dnsmasq 启用了 HAVE_BROKEN_RTC：租约文件第一列是剩余秒数，不是到期时间戳。
func TestLeasesRealSampleUseRemainingSeconds(t *testing.T) {
	now, err := routercmd.ParseDate(readReal(t, "date.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := Merge(Inputs{Now: now, Leases: ParseLeases(readReal(t, "dnsmasq.leases"))})
	if len(got) != 5 {
		t.Fatalf("len = %d", len(got))
	}
	for _, c := range got {
		if c.LeaseRemainingSec == nil || *c.LeaseRemainingSec <= 0 || *c.LeaseRemainingSec > 86400 {
			t.Errorf("%s 的剩余租期应在 (0, 86400] 之间，得到 %v", c.MAC, c.LeaseRemainingSec)
		}
	}
}

// ARP 表里 WAN 口（eth0）上的上游设备不是局域网客户端。
func TestARPRealSampleExcludesWANSide(t *testing.T) {
	got := Merge(Inputs{ARP: ParseARP(readReal(t, "arp.txt"))})
	if len(got) == 0 {
		t.Fatal("应解析出局域网设备")
	}
	for _, c := range got {
		if strings.HasPrefix(c.IP, "198.51.100.") {
			t.Errorf("WAN 侧设备不应出现在客户端列表中: %+v", c)
		}
	}
}

// 访客网络（wl0.1、wl1.1 等虚拟接口）上的设备也要查询关联列表。
func TestFetchQueriesGuestInterfaces(t *testing.T) {
	base := "@@MERLINMCP:date@@\n1791013761 +0800\n" +
		"@@MERLINMCP:nvram@@\ndhcp_staticlist=\ndhcp_hostnames=\nwl0_ifname=eth6\nwl0_nband=2\nwl0_vifs=wl0.1\nwl1_ifname=eth7\nwl1_nband=1\nwl1_vifs=wl1.1\n" +
		"@@MERLINMCP:leases@@\n" +
		"@@MERLINMCP:arp@@\nIP address HW type Flags HW address Mask Device\n198.18.102.54 0x1 0x2 AA:BB:CC:00:00:31 * br2\n"
	f := runnertest.New().
		On("clients_base", runnertest.Stdout(base)).
		On("clients_assoc", runnertest.Stdout("@@MERLINMCP:eth6@@\n@@MERLINMCP:wl0.1@@\n@@MERLINMCP:eth7@@\n@@MERLINMCP:wl1.1@@\nassoclist AA:BB:CC:00:00:31\n"))
	got, err := Fetch(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.CallsFor("clients_assoc")[0].Cmd, "wl -i 'wl1.1' assoclist") {
		t.Fatalf("应查询访客接口: %s", f.CallsFor("clients_assoc")[0].Cmd)
	}
	if len(got) != 1 || got[0].Connection != "5G" || !got[0].Guest {
		t.Fatalf("访客设备应为 5G 且标记 guest: %+v", got)
	}
}
