// Package syslog 解析和过滤 Merlin 的 syslog 与 dmesg 输出。
package syslog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxOutputBytes 是单次返回给 AI 的日志上限。
const MaxOutputBytes = 64 * 1024

// Line 是解析后的一行 syslog。
type Line struct {
	Time    time.Time
	HasTime bool   // 本行或之前的行带有可解析的时间戳
	Process string // 进程名（不含 [pid]），无法识别时为空
	Raw     string // 原始文本
}

const bsdLayout = "Jan _2 15:04:05"

// Parse 解析 syslog 文本。now 是路由器当前时间（带路由器时区），用于推断年份。
// 无法解析时间戳的行沿用前一行的时间。
func Parse(raw string, now time.Time) []Line {
	var out []Line
	var last time.Time
	have := false
	for _, s := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if s == "" {
			continue
		}
		l := Line{Raw: s}
		if t, rest, ok := parseTime(s, now); ok {
			last, have = t, true
			l.Process = processOf(rest)
		}
		l.Time, l.HasTime = last, have
		out = append(out, l)
	}
	return out
}

func parseTime(s string, now time.Time) (time.Time, string, bool) {
	if len(s) >= 15 {
		if t, err := time.ParseInLocation(bsdLayout, s[:15], now.Location()); err == nil {
			t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
			// 时间戳没有年份：晚于"现在"（允许 1 分钟误差）的说明是去年的日志
			if t.After(now.Add(time.Minute)) {
				t = t.AddDate(-1, 0, 0)
			}
			return t, strings.TrimSpace(s[15:]), true
		}
	}
	if i := strings.IndexByte(s, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339, s[:i]); err == nil {
			return t, strings.TrimSpace(s[i+1:]), true
		}
	}
	return time.Time{}, "", false
}

// processOf 从时间戳之后的内容中取进程名：第一个或第二个（前面有主机名时）以冒号结尾的词。
func processOf(rest string) string {
	f := strings.Fields(rest)
	for i := 0; i < len(f) && i < 2; i++ {
		if strings.HasSuffix(f[i], ":") {
			name := strings.TrimSuffix(f[i], ":")
			if j := strings.IndexByte(name, '['); j >= 0 {
				name = name[:j]
			}
			return name
		}
	}
	return ""
}

// Filter 是 syslog_read 的过滤条件，零值表示不过滤。
type Filter struct {
	Since   time.Duration
	Process string
	Keyword string
	Lines   int
}

// Apply 按 时间 → 进程 → 关键字 的顺序过滤，最后保留最新的 Lines 行。
func Apply(lines []Line, f Filter, now time.Time) []Line {
	cut := now.Add(-f.Since)
	kw := strings.ToLower(f.Keyword)
	var out []Line
	for _, l := range lines {
		if f.Since > 0 && (!l.HasTime || l.Time.Before(cut)) {
			continue
		}
		if f.Process != "" && l.Process != f.Process {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(l.Raw), kw) {
			continue
		}
		out = append(out, l)
	}
	if f.Lines > 0 && len(out) > f.Lines {
		out = out[len(out)-f.Lines:]
	}
	return out
}

var sinceRe = regexp.MustCompile(`^([1-9][0-9]{0,4})([mhd])$`)

// ParseSince 解析 30m、2h、1d 这类相对时间，最大 7d；空字符串返回 0。
func ParseSince(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	m := sinceRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("since 格式错误 %q，应为 <数字><m|h|d>", s)
	}
	n, _ := strconv.Atoi(m[1])
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	d := time.Duration(n) * unit
	if d > 7*24*time.Hour {
		return 0, fmt.Errorf("since 最大为 7d，当前为 %q", s)
	}
	return d, nil
}

// Raws 取出原始文本。
func Raws(lines []Line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Raw
	}
	return out
}

// FilterText 按关键字（不区分大小写）过滤纯文本行，n > 0 时保留最后 n 行。
func FilterText(lines []string, keyword string, n int) []string {
	kw := strings.ToLower(keyword)
	var out []string
	for _, l := range lines {
		if kw == "" || strings.Contains(strings.ToLower(l), kw) {
			out = append(out, l)
		}
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Render 拼接日志行。超过 maxBytes 时从前面丢弃整行、保留最新的内容；
// 最新的一行本身就超长时只保留它的末尾。结果总是合法的 UTF-8。
func Render(lines []string, maxBytes int) (string, bool) {
	// 先替换非法 UTF-8 再计算长度：每个非法字节会变成 3 字节的替换字符
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = strings.ToValidUTF8(l, "�")
	}
	lines = clean
	total, start := 0, len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		n := len(lines[i]) + 1
		if total+n > maxBytes {
			break
		}
		total += n
		start = i
	}
	if start == len(lines) && len(lines) > 0 {
		last := lines[len(lines)-1]
		tail := last[len(last)-(maxBytes-1):]
		return strings.ToValidUTF8(tail, "") + "\n", true
	}
	text := strings.Join(lines[start:], "\n")
	if text != "" {
		text += "\n"
	}
	return text, start > 0
}
