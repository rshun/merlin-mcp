package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rshun/merlin-mcp/internal/syslog"
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

func TestParseSystemRealSample(t *testing.T) {
	s, err := ParseSystem(readReal(t, "system_status.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "RT-AX86U" || s.Firmware != "3.0.0.4.388.12_2" {
		t.Errorf("model/firmware = %q %q", s.Model, s.Firmware)
	}
	if s.UptimeSeconds != 197640 || s.MemTotalKB != 933964 || s.MemAvailableKB != 560112 {
		t.Errorf("s = %+v", s)
	}
	if s.TemperatureC == nil || *s.TemperatureC != 71.9 {
		t.Errorf("temp = %v", s.TemperatureC)
	}
}

func TestParseWANRealSample(t *testing.T) {
	w := ParseWAN(readReal(t, "wan_nvram.txt"))
	if w.State != "connected" || w.Proto != "pppoe" {
		t.Fatalf("w = %+v", w)
	}
}

func TestParseConntrackRealSample(t *testing.T) {
	c, err := ParseConntrack(readReal(t, "conntrack.txt"))
	if err != nil || c.Count != 333 || c.Max != 300000 || c.Warning != "" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}

// 真实 syslog 中没有 WAN 事件（只有 DHCP、Wi-Fi、dropbear），不能误报。
func TestWANEventsRealSyslogHasNoFalsePositives(t *testing.T) {
	now := time.Unix(1791013761, 0).In(time.FixedZone("router", 8*3600))
	if ev := WANEvents(syslog.Parse(readReal(t, "syslog.txt"), now), 20); len(ev) != 0 {
		t.Fatalf("误报的 WAN 事件: %v", ev)
	}
}
