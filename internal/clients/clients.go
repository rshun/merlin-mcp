// Package clients 合并 DHCP 租约、ARP 表、静态 IP 分配和无线关联列表，生成客户端列表。
package clients

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rshun/merlin-mcp/internal/routercmd"
	"github.com/rshun/merlin-mcp/internal/runner"
	"github.com/rshun/merlin-mcp/internal/shell"
)

// Lease 是 dnsmasq.leases 的一行。Expiry 为 0 表示永不过期。
type Lease struct {
	Expiry   int64
	MAC      string
	IP       string
	Hostname string
}

// ARPEntry 是 /proc/net/arp 的一行。
type ARPEntry struct {
	IP     string
	MAC    string
	Device string
}

// Static 是一条静态 IP 分配。
type Static struct {
	IP       string
	Hostname string
}

// Inputs 是 Merge 的全部输入。MAC 一律为大写。
type Inputs struct {
	Now      time.Time
	Leases   []Lease
	ARP      []ARPEntry
	Static   map[string]Static
	Wireless map[string]string // MAC → 频段（2.4G/5G/6G/unknown）
}

// Client 是 clients_list 返回的一台设备。
type Client struct {
	MAC               string `json:"mac"`
	IP                string `json:"ip,omitempty"`
	Hostname          string `json:"hostname,omitempty"`
	Connection        string `json:"connection"` // wired / 2.4G / 5G / 6G / unknown
	Static            bool   `json:"static"`
	LeaseRemainingSec *int64 `json:"lease_remaining_sec,omitempty"`
	LeaseInfinite     bool   `json:"lease_infinite,omitempty"`
	InARP             bool   `json:"in_arp"`
}

var macRe = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){5}$`)

func normMAC(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// ParseLeases 解析 dnsmasq.leases（格式：过期时间 MAC IP 主机名 客户端ID），跳过格式不对的行。
func ParseLeases(s string) []Lease {
	var out []Lease
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		exp, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		mac := normMAC(f[1])
		if !macRe.MatchString(mac) {
			continue
		}
		host := f[3]
		if host == "*" {
			host = ""
		}
		out = append(out, Lease{Expiry: exp, MAC: mac, IP: f[2], Hostname: host})
	}
	return out
}

// ParseARP 解析 /proc/net/arp，跳过表头和未完成解析（flags 0x0）的条目。
func ParseARP(s string) []ARPEntry {
	var out []ARPEntry
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || f[0] == "IP" {
			continue
		}
		mac := normMAC(f[3])
		if !macRe.MatchString(mac) || mac == "00:00:00:00:00:00" || f[2] == "0x0" {
			continue
		}
		out = append(out, ARPEntry{IP: f[0], MAC: mac, Device: f[5]})
	}
	return out
}

// ParseStaticList 解析 nvram dhcp_staticlist（<MAC>IP>DNS>主机名，不同固件版本字段数量不同）
// 和 dhcp_hostnames（<MAC>主机名，只用来给已有的静态分配补充主机名）。
func ParseStaticList(staticList, hostnames string) map[string]Static {
	m := map[string]Static{}
	for _, e := range strings.Split(staticList, "<") {
		parts := strings.Split(e, ">")
		mac := normMAC(parts[0])
		if len(parts) < 2 || !macRe.MatchString(mac) {
			continue
		}
		st := Static{IP: strings.TrimSpace(parts[1])}
		if len(parts) >= 4 {
			st.Hostname = strings.TrimSpace(parts[3])
		}
		m[mac] = st
	}
	for _, e := range strings.Split(hostnames, "<") {
		mac, name, ok := strings.Cut(e, ">")
		mac = normMAC(mac)
		st, exists := m[mac]
		if !ok || !exists || name == "" || st.Hostname != "" {
			continue
		}
		st.Hostname = strings.TrimSpace(name)
		m[mac] = st
	}
	return m
}

// ParseAssoc 解析 `wl -i <if> assoclist` 的输出（每行 "assoclist <MAC>"）。
func ParseAssoc(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if mac := normMAC(f[len(f)-1]); macRe.MatchString(mac) {
			out = append(out, mac)
		}
	}
	return out
}

func bandOf(nband string) string {
	switch strings.TrimSpace(nband) {
	case "2":
		return "2.4G"
	case "1":
		return "5G"
	case "4":
		return "6G"
	}
	return "unknown"
}

// Merge 以 MAC 为主键合并数据。
// IP 优先级：ARP > 租约 > 静态分配；主机名优先级：租约 > 静态分配。
func Merge(in Inputs) []Client {
	byMAC := map[string]*Client{}
	get := func(mac string) *Client {
		c, ok := byMAC[mac]
		if !ok {
			c = &Client{MAC: mac}
			byMAC[mac] = c
		}
		return c
	}
	for mac, st := range in.Static {
		c := get(mac)
		c.Static, c.IP, c.Hostname = true, st.IP, st.Hostname
	}
	for _, l := range in.Leases {
		c := get(l.MAC)
		c.IP = l.IP
		if l.Hostname != "" {
			c.Hostname = l.Hostname
		}
		if l.Expiry == 0 {
			c.LeaseInfinite = true
		} else {
			rem := l.Expiry - in.Now.Unix()
			if rem < 0 {
				rem = 0
			}
			c.LeaseRemainingSec = &rem
		}
	}
	for _, a := range in.ARP {
		c := get(a.MAC)
		c.IP, c.InARP = a.IP, true
	}
	for mac := range in.Wireless {
		get(mac)
	}

	out := make([]Client, 0, len(byMAC))
	for _, c := range byMAC {
		switch {
		case in.Wireless[c.MAC] != "":
			c.Connection = in.Wireless[c.MAC]
		case c.InARP:
			c.Connection = "wired"
		default:
			c.Connection = "unknown"
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// less 按 IP 排序，没有 IP 的排在最后，IP 相同时按 MAC。
func less(a, b Client) bool {
	ai, aerr := netip.ParseAddr(a.IP)
	bi, berr := netip.ParseAddr(b.IP)
	switch {
	case aerr == nil && berr == nil:
		if c := ai.Compare(bi); c != 0 {
			return c < 0
		}
	case aerr == nil:
		return true
	case berr == nil:
		return false
	}
	return a.MAC < b.MAC
}

var baseKeys = []string{
	"dhcp_staticlist", "dhcp_hostnames",
	"wl0_ifname", "wl0_nband", "wl1_ifname", "wl1_nband",
	"wl2_ifname", "wl2_nband", "wl3_ifname", "wl3_nband",
}

// Fetch 两次 SSH 调用：先取租约、ARP、nvram，再按无线接口查询关联列表。
// wl 命令失败时不报错，无线设备的连接方式降级为 wired/unknown。
func Fetch(ctx context.Context, r runner.Runner) ([]Client, error) {
	base := runner.Op("clients_base", strings.Join([]string{
		routercmd.Marker("date"), routercmd.DateCmd,
		routercmd.Marker("nvram"), routercmd.MustNvramScript(baseKeys...),
		routercmd.Marker("leases"), "cat /var/lib/misc/dnsmasq.leases 2>/dev/null",
		routercmd.Marker("arp"), "cat /proc/net/arp",
	}, "; "))
	out, err := runner.Output(ctx, r, base)
	if err != nil {
		return nil, err
	}
	sec := routercmd.Sections(out)
	now, err := routercmd.ParseDate(sec["date"])
	if err != nil {
		now = time.Now()
	}
	kv := routercmd.ParseKV(sec["nvram"])
	in := Inputs{
		Now:      now,
		Leases:   ParseLeases(sec["leases"]),
		ARP:      ParseARP(sec["arp"]),
		Static:   ParseStaticList(kv["dhcp_staticlist"], kv["dhcp_hostnames"]),
		Wireless: map[string]string{},
	}

	bands := map[string]string{}
	var parts []string
	for i := 0; i < 4; i++ {
		ifname := strings.TrimSpace(kv[fmt.Sprintf("wl%d_ifname", i)])
		if ifname == "" || !shell.ValidIfName(ifname) {
			continue
		}
		bands[ifname] = bandOf(kv[fmt.Sprintf("wl%d_nband", i)])
		parts = append(parts, routercmd.Marker(ifname), "wl -i "+shell.Quote(ifname)+" assoclist 2>/dev/null")
	}
	if len(parts) > 0 {
		res, err := r.Run(ctx, runner.Op("clients_assoc", strings.Join(parts, "; ")), nil)
		if err != nil {
			return nil, err
		}
		asec := routercmd.Sections(string(res.Stdout))
		for ifname, band := range bands {
			for _, mac := range ParseAssoc(asec[ifname]) {
				in.Wireless[mac] = band
			}
		}
	}
	return Merge(in), nil
}
