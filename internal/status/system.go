// Package status 采集并解析路由器的系统、WAN 和连接跟踪状态。
package status

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// System 是 system_status 的返回内容（不含重启额度，由工具层补充）。
type System struct {
	Model          string     `json:"model"`
	Firmware       string     `json:"firmware"`
	UptimeSeconds  int64      `json:"uptime_seconds"`
	Load           [3]float64 `json:"load_average"`
	CPUPercent     float64    `json:"cpu_percent"`
	MemTotalKB     int64      `json:"mem_total_kb"`
	MemAvailableKB int64      `json:"mem_available_kb"`
	MemUsedPercent float64    `json:"mem_used_percent"`
	TemperatureC   *float64   `json:"temperature_c"`
}

var systemKeys = []string{"productid", "firmver", "buildno", "extendno"}

func systemCmd() string {
	return runner.Op("system_status", strings.Join([]string{
		routercmd.Marker("uptime"), "cat /proc/uptime",
		routercmd.Marker("loadavg"), "cat /proc/loadavg",
		routercmd.Marker("meminfo"), "cat /proc/meminfo",
		routercmd.Marker("stat1"), "head -n 1 /proc/stat",
		"sleep 1",
		routercmd.Marker("stat2"), "head -n 1 /proc/stat",
		routercmd.Marker("temp"), "(cat /sys/class/thermal/thermal_zone0/temp || cat /proc/dmu/temperature) 2>/dev/null",
		routercmd.Marker("nvram"), routercmd.MustNvramScript(systemKeys...),
	}, "; "))
}

// FetchSystem 一次 SSH 调用采集全部系统状态（其中 CPU 采样间隔 1 秒）。
func FetchSystem(ctx context.Context, r runner.Runner) (System, error) {
	out, err := runner.Output(ctx, r, systemCmd())
	if err != nil {
		return System{}, err
	}
	return ParseSystem(out)
}

// ParseSystem 解析 systemCmd 的输出。
func ParseSystem(out string) (System, error) {
	sec := routercmd.Sections(out)
	var s System

	up := strings.Fields(sec["uptime"])
	if len(up) == 0 {
		return s, parseErr("/proc/uptime")
	}
	f, err := strconv.ParseFloat(up[0], 64)
	if err != nil {
		return s, parseErr("/proc/uptime")
	}
	s.UptimeSeconds = int64(f)

	la := strings.Fields(sec["loadavg"])
	if len(la) < 3 {
		return s, parseErr("/proc/loadavg")
	}
	for i := 0; i < 3; i++ {
		s.Load[i], _ = strconv.ParseFloat(la[i], 64)
	}

	mem := parseMeminfo(sec["meminfo"])
	total := mem["MemTotal"]
	if total == 0 {
		return s, parseErr("/proc/meminfo")
	}
	avail, ok := mem["MemAvailable"]
	if !ok {
		avail = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	s.MemTotalKB, s.MemAvailableKB = total, avail
	s.MemUsedPercent = round1(100 * float64(total-avail) / float64(total))

	s.CPUPercent = cpuPercent(sec["stat1"], sec["stat2"])
	s.TemperatureC = parseTemp(sec["temp"])

	kv := routercmd.ParseKV(sec["nvram"])
	s.Model = kv["productid"]
	s.Firmware = firmware(kv)
	return s, nil
}

func parseErr(what string) error {
	return apperr.New(apperr.Internal, "无法解析路由器的 "+what+" 输出", "可能是固件输出格式不同，请把原始输出提供给开发者")
}

func parseMeminfo(s string) map[string]int64 {
	m := map[string]int64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
			m[strings.TrimSpace(k)] = n
		}
	}
	return m
}

// cpuTimes 解析 /proc/stat 的 cpu 行，idle 包含 iowait。
func cpuTimes(line string) (idle, total uint64, ok bool) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range f[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	return idle, total, true
}

func cpuPercent(a, b string) float64 {
	i1, t1, ok1 := cpuTimes(strings.TrimSpace(a))
	i2, t2, ok2 := cpuTimes(strings.TrimSpace(b))
	if !ok1 || !ok2 || t2 <= t1 || i2 < i1 {
		return 0
	}
	busy := float64((t2 - t1) - (i2 - i1))
	return round1(100 * busy / float64(t2-t1))
}

var numRe = regexp.MustCompile(`-?\d+(\.\d+)?`)

// parseTemp 支持 thermal_zone 的毫摄氏度和 /proc/dmu/temperature 的文字格式，读不到返回 nil。
func parseTemp(s string) *float64 {
	m := numRe.FindString(s)
	if m == "" {
		return nil
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return nil
	}
	if v > 1000 {
		v /= 1000
	}
	v = round1(v)
	return &v
}

func firmware(kv map[string]string) string {
	var parts []string
	for _, k := range []string{"firmver", "buildno"} {
		if kv[k] != "" {
			parts = append(parts, kv[k])
		}
	}
	fw := strings.Join(parts, ".")
	if e := kv["extendno"]; e != "" && fw != "" {
		fw += "_" + e
	}
	return fw
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
