// Package routercmd 提供组装路由器命令和解析其输出的公共工具。
package routercmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const markerPrefix = "@@MERLINMCP:"

// Marker 返回输出一个分段标记的命令。多条命令的输出用标记分隔，一次 SSH 调用即可全部取回。
func Marker(name string) string { return "echo '" + markerPrefix + name + "@@'" }

// Sections 按 Marker 输出的标记把 out 切分成各段；第一个标记之前的内容被忽略。
func Sections(out string) map[string]string {
	res := map[string]string{}
	var cur string
	var b strings.Builder
	have := false
	for _, line := range strings.SplitAfter(out, "\n") {
		t := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(t, markerPrefix) && strings.HasSuffix(t, "@@") && len(t) > len(markerPrefix)+2 {
			if have {
				res[cur] = b.String()
			}
			cur = strings.TrimSuffix(strings.TrimPrefix(t, markerPrefix), "@@")
			b.Reset()
			have = true
			continue
		}
		if have {
			b.WriteString(line)
		}
	}
	if have {
		res[cur] = b.String()
	}
	return res
}

var (
	nvramKeyRe  = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	nvramDenyRe = regexp.MustCompile(`(?i)(passwd|password|psk|secret|key)`)
)

// NvramScript 生成读取一组 nvram 键的脚本，输出为 key=value 行。
// 名称含 passwd/password/psk/secret/key 的键一律拒绝，防止把密码类字段交给 AI。
func NvramScript(keys ...string) (string, error) {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if !nvramKeyRe.MatchString(k) || nvramDenyRe.MatchString(k) {
			return "", fmt.Errorf("不允许读取的 nvram 键: %q", k)
		}
		parts = append(parts, fmt.Sprintf(`printf '%%s=%%s\n' %s "$(nvram get %s)"`, k, k))
	}
	return strings.Join(parts, "; "), nil
}

// MustNvramScript 与 NvramScript 相同，键非法时 panic。只用于代码中写死的键列表。
func MustNvramScript(keys ...string) string {
	s, err := NvramScript(keys...)
	if err != nil {
		panic(err)
	}
	return s
}

// ParseKV 解析 key=value 行，忽略没有等号的行。
func ParseKV(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if ok && k != "" {
			m[k] = v
		}
	}
	return m
}

// DateCmd 输出路由器当前的 Unix 时间和时区偏移，例如 "1791000000 +0800"。
const DateCmd = "date '+%s %z'"

// ParseDate 解析 DateCmd 的输出，返回带路由器时区偏移的时间。
func ParseDate(s string) (time.Time, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return time.Time{}, fmt.Errorf("无法解析路由器时间 %q", s)
	}
	sec, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("无法解析路由器时间 %q: %w", s, err)
	}
	z := f[1]
	if len(z) != 5 || (z[0] != '+' && z[0] != '-') {
		return time.Time{}, fmt.Errorf("无法解析路由器时区 %q", z)
	}
	hh, err1 := strconv.Atoi(z[1:3])
	mm, err2 := strconv.Atoi(z[3:5])
	if err1 != nil || err2 != nil {
		return time.Time{}, fmt.Errorf("无法解析路由器时区 %q", z)
	}
	off := hh*3600 + mm*60
	if z[0] == '-' {
		off = -off
	}
	return time.Unix(sec, 0).In(time.FixedZone("router", off)), nil
}
