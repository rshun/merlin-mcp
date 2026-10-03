package status

import (
	"context"
	"strings"
	"testing"

	"github.com/rshun/merlin-mcp/internal/runner/runnertest"
)

const systemSample = `@@MERLINMCP:uptime@@
12345.67 40000.00
@@MERLINMCP:loadavg@@
0.52 0.40 0.35 2/150 1234
@@MERLINMCP:meminfo@@
MemTotal:         524288 kB
MemFree:          100000 kB
MemAvailable:     262144 kB
Buffers:           10000 kB
Cached:            50000 kB
@@MERLINMCP:stat1@@
cpu  1000 0 1000 8000 0 0 0 0 0 0
@@MERLINMCP:stat2@@
cpu  1100 0 1100 8800 0 0 0 0 0 0
@@MERLINMCP:temp@@
68123
@@MERLINMCP:nvram@@
productid=RT-AX86U
firmver=3.0.0.4
buildno=388.8
extendno=4
`

func TestParseSystem(t *testing.T) {
	s, err := ParseSystem(systemSample)
	if err != nil {
		t.Fatal(err)
	}
	if s.Model != "RT-AX86U" || s.Firmware != "3.0.0.4.388.8_4" {
		t.Errorf("model/firmware = %q %q", s.Model, s.Firmware)
	}
	if s.UptimeSeconds != 12345 || s.Load != [3]float64{0.52, 0.40, 0.35} {
		t.Errorf("uptime/load = %d %v", s.UptimeSeconds, s.Load)
	}
	if s.CPUPercent != 20 {
		t.Errorf("cpu = %v，期望 20", s.CPUPercent)
	}
	if s.MemTotalKB != 524288 || s.MemAvailableKB != 262144 || s.MemUsedPercent != 50 {
		t.Errorf("mem = %d %d %v", s.MemTotalKB, s.MemAvailableKB, s.MemUsedPercent)
	}
	if s.TemperatureC == nil || *s.TemperatureC != 68.1 {
		t.Errorf("temp = %v", s.TemperatureC)
	}
}

func TestParseSystemTemperatureVariants(t *testing.T) {
	out := strings.Replace(systemSample, "68123", "CPU temperature : 71°C", 1)
	s, _ := ParseSystem(out)
	if s.TemperatureC == nil || *s.TemperatureC != 71 {
		t.Errorf("dmu 格式温度 = %v", s.TemperatureC)
	}
	out = strings.Replace(systemSample, "68123\n", "", 1)
	s, _ = ParseSystem(out)
	if s.TemperatureC != nil {
		t.Errorf("没有温度时应为 nil，得到 %v", *s.TemperatureC)
	}
}

func TestParseSystemWithoutMemAvailable(t *testing.T) {
	out := strings.Replace(systemSample, "MemAvailable:     262144 kB\n", "", 1)
	s, err := ParseSystem(out)
	if err != nil {
		t.Fatal(err)
	}
	if s.MemAvailableKB != 160000 {
		t.Errorf("回退计算 MemFree+Buffers+Cached = %d，期望 160000", s.MemAvailableKB)
	}
}

func TestParseSystemMissingSectionFails(t *testing.T) {
	if _, err := ParseSystem("@@MERLINMCP:loadavg@@\n0 0 0\n"); err == nil {
		t.Fatal("缺少 uptime 应报错")
	}
}

func TestFetchSystemUsesWhitelistedNvramOnly(t *testing.T) {
	f := runnertest.New().On("system_status", runnertest.Stdout(systemSample))
	if _, err := FetchSystem(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	cmd := f.CallsFor("system_status")[0].Cmd
	if strings.Contains(cmd, "passwd") || !strings.Contains(cmd, "nvram get productid") {
		t.Fatalf("cmd = %s", cmd)
	}
}
