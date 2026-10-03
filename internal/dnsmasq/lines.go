// Package dnsmasq 管理 Merlin 的 dnsmasq.conf.add：按行增删、备份、生效与回滚。
package dnsmasq

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

// NormalizeInput 校验并规范化要添加/删除的行：去掉行尾空白，拒绝空行和含换行/NUL 的行，
// 输入中重复的行只保留一个。
func NormalizeInput(lines []string) ([]string, error) {
	if len(lines) == 0 || len(lines) > 100 {
		return nil, apperr.New(apperr.InvalidArgument, "lines 必须包含 1-100 行", "")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if strings.ContainsAny(l, "\n\r\x00") {
			return nil, apperr.New(apperr.InvalidArgument, fmt.Sprintf("第 %d 行包含换行符或 NUL 字符，每个元素只能是一行配置", i+1), "")
		}
		n := strings.TrimRight(l, " \t")
		if strings.TrimSpace(n) == "" {
			return nil, apperr.New(apperr.InvalidArgument, fmt.Sprintf("第 %d 行为空", i+1), "")
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// splitContent 把文件内容拆成行：统一 CRLF，去掉行尾空白，去掉末尾的空行（中间的空行保留）。
func splitContent(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// join 把行拼回文件内容，非空时以换行结尾。
func join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Add 追加文件中还没有的行。
func Add(content string, add []string) (next string, added, skipped []string) {
	cur := splitContent(content)
	have := map[string]bool{}
	for _, l := range cur {
		have[l] = true
	}
	for _, a := range add {
		if have[a] {
			skipped = append(skipped, a)
			continue
		}
		have[a] = true
		cur = append(cur, a)
		added = append(added, a)
	}
	return join(cur), added, skipped
}

// Remove 删除所有与 rm 中某一项完全相同的行。
func Remove(content string, rm []string) (next string, removed, notFound []string) {
	want := map[string]bool{}
	for _, r := range rm {
		want[r] = true
	}
	found := map[string]bool{}
	var kept []string
	for _, l := range splitContent(content) {
		if want[l] {
			found[l] = true
			continue
		}
		kept = append(kept, l)
	}
	for _, r := range rm {
		if found[r] {
			removed = append(removed, r)
		} else {
			notFound = append(notFound, r)
		}
	}
	return join(kept), removed, notFound
}

// SHA256 返回内容的小写十六进制 sha256。
func SHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Diff 基于最长公共子序列生成逐行差异，行首为 " "（不变）、"-"（删除）、"+"（新增）。
func Diff(oldContent, newContent string) string {
	a, b := splitContent(oldContent), splitContent(newContent)
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var sb strings.Builder
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			sb.WriteString(" " + a[i] + "\n")
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			sb.WriteString("-" + a[i] + "\n")
			i++
		default:
			sb.WriteString("+" + b[j] + "\n")
			j++
		}
	}
	for ; i < n; i++ {
		sb.WriteString("-" + a[i] + "\n")
	}
	for ; j < m; j++ {
		sb.WriteString("+" + b[j] + "\n")
	}
	return sb.String()
}
