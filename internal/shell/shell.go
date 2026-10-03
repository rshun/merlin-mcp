// Package shell 提供拼接远程命令时使用的转义和参数校验。
// 规则：用户传入的值必须先通过这里的校验，再经 Quote 转义后才能拼进命令。
package shell

import (
	"net/netip"
	"regexp"
	"strings"
)

// Quote 把 s 转成 POSIX shell 单引号字符串；内部的单引号会先闭合引号、转义后再重新打开引号。
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var (
	labelRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	processRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	ifnameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)
)

// ValidDomain 判断 s 是否为合法域名（RFC 1123，最长 253 字符，允许末尾的点）。
func ValidDomain(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !labelRe.MatchString(label) {
			return false
		}
	}
	return true
}

// ValidIP 判断 s 是否为不带 zone 的 IPv4 或 IPv6 地址。
func ValidIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Zone() == ""
}

// ValidHost 判断 s 是否为合法域名或 IP。
func ValidHost(s string) bool { return ValidIP(s) || ValidDomain(s) }

// ValidProcessName 判断 s 是否为合法的进程名（用于 syslog 过滤）。
func ValidProcessName(s string) bool { return processRe.MatchString(s) }

// ValidIfName 判断 s 是否为合法的网络接口名（Linux 接口名最长 15 字符）。
func ValidIfName(s string) bool { return ifnameRe.MatchString(s) }
