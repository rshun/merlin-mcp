// Package sshx 实现基于 SSH 的 runner.Runner：长连接复用、host key 校验、超时与并发控制。
package sshx

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/rshun/merlin-mcp/internal/apperr"
	"github.com/rshun/merlin-mcp/internal/runner"
)

// Options 是 SSH 客户端的配置。
type Options struct {
	Addr        string        // host:port
	User        string        // 路由器用户名
	KeyFile     string        // 私钥路径（不支持带口令的私钥）
	KnownHosts  string        // known_hosts 路径
	Timeout     time.Duration // 调用方没有设置 deadline 时使用的默认命令超时
	DialTimeout time.Duration // 建立 TCP 连接和握手的超时，默认 10s
	MaxSessions int           // 同时打开的 session 上限，默认 4
	KeepAlive   time.Duration // keepalive 间隔，0 表示不发送
}

// Client 维护一条到路由器的 SSH 长连接，每次 Run 打开一个新的 session。
type Client struct {
	opts   Options
	config *ssh.ClientConfig
	sem    chan struct{}

	mu   sync.Mutex
	conn *ssh.Client
}

var _ runner.Runner = (*Client)(nil)

const hintCheckRouter = "检查路由器是否在线、SSH 是否开启，然后重试"

// New 读取私钥和 known_hosts 并完成校验，但不立即连接。
func New(opts Options) (*Client, error) {
	if opts.DialTimeout <= 0 {
		opts.DialTimeout = 10 * time.Second
	}
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = 4
	}
	keyData, err := os.ReadFile(opts.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("读取私钥失败: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		var pe *ssh.PassphraseMissingError
		if errors.As(err, &pe) {
			return nil, fmt.Errorf("私钥 %s 设置了口令，暂不支持带口令的私钥", opts.KeyFile)
		}
		return nil, fmt.Errorf("解析私钥失败: %w", err)
	}
	cb, err := knownhosts.New(opts.KnownHosts)
	if err != nil {
		return nil, fmt.Errorf("读取 known_hosts 失败: %w", err)
	}
	algos, err := hostKeyAlgorithms(cb, opts.Addr)
	if err != nil {
		return nil, err
	}
	return &Client{
		opts: opts,
		config: &ssh.ClientConfig{
			User:              opts.User,
			Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback:   cb,
			HostKeyAlgorithms: algos,
			Timeout:           opts.DialTimeout,
		},
		sem: make(chan struct{}, opts.MaxSessions),
	}, nil
}

// hostKeyAlgorithms 用一个随机探测公钥查询 known_hosts，取出该主机已登记的 key 类型。
// 这样握手时只协商 known_hosts 里有的算法，避免服务器优先提供其他类型的 key 导致误报不匹配。
func hostKeyAlgorithms(cb ssh.HostKeyCallback, addr string) ([]string, error) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	err = cb(addr, &net.TCPAddr{IP: net.IPv4zero}, probe)
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) || len(ke.Want) == 0 {
		return nil, fmt.Errorf("known_hosts 中没有 %s 的记录，请先用 ssh 登录一次或用 ssh-keyscan 添加，并核对指纹", addr)
	}
	seen := map[string]bool{}
	var algos []string
	addAlgo := func(a string) {
		if !seen[a] {
			seen[a] = true
			algos = append(algos, a)
		}
	}
	for _, w := range ke.Want {
		if t := w.Key.Type(); t == ssh.KeyAlgoRSA {
			addAlgo(ssh.KeyAlgoRSASHA512)
			addAlgo(ssh.KeyAlgoRSASHA256)
			addAlgo(ssh.KeyAlgoRSA)
		} else {
			addAlgo(t)
		}
	}
	return algos, nil
}

// Run 实现 runner.Runner。远程退出码非 0 不视为错误。
// 每条命令最多执行 Timeout；调用方的 deadline 更早时以调用方为准。
func (c *Client) Run(ctx context.Context, cmd string, stdin []byte) (runner.Result, error) {
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return runner.Result{}, timeoutErr()
	}

	conn, err := c.connect(ctx)
	if err != nil {
		return runner.Result{}, err
	}
	sess, err := c.openSession(ctx, conn)
	if err != nil {
		return runner.Result{}, err
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	if stdin != nil {
		sess.Stdin = bytes.NewReader(stdin)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		// 超时可能意味着连接已经变成黑洞，丢弃它，下次调用重新连接
		c.drop(conn)
		return runner.Result{}, timeoutErr()
	}

	res := runner.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return res, nil
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitStatus()
		return res, nil
	}
	c.drop(conn)
	return res, apperr.New(apperr.SSHUnreachable, "SSH 连接中断: "+err.Error(), hintCheckRouter)
}

// openSession 打开 session 并受 ctx 约束：连接失效时 NewSession 可能一直等不到对端确认。
func (c *Client) openSession(ctx context.Context, conn *ssh.Client) (*ssh.Session, error) {
	type opened struct {
		s   *ssh.Session
		err error
	}
	ch := make(chan opened, 1)
	go func() {
		s, err := conn.NewSession()
		ch <- opened{s, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			c.drop(conn)
			return nil, apperr.New(apperr.SSHUnreachable, "无法创建 SSH 会话: "+o.err.Error(), hintCheckRouter)
		}
		return o.s, nil
	case <-ctx.Done():
		c.drop(conn)
		go func() {
			if o := <-ch; o.s != nil {
				o.s.Close()
			}
		}()
		return nil, timeoutErr()
	}
}

func timeoutErr() error {
	return apperr.New(apperr.Timeout, "命令执行超时", "可以缩小查询范围后重试，或检查路由器负载")
}

// connect 返回当前连接，没有时建立新连接。
func (c *Client) connect(ctx context.Context) (*ssh.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn, nil
	}
	d := net.Dialer{Timeout: c.opts.DialTimeout}
	nc, err := d.DialContext(ctx, "tcp", c.opts.Addr)
	if err != nil {
		return nil, apperr.New(apperr.SSHUnreachable, fmt.Sprintf("无法连接路由器 %s: %v", c.opts.Addr, err), hintCheckRouter)
	}
	_ = nc.SetDeadline(time.Now().Add(c.opts.DialTimeout))
	sc, chans, reqs, err := ssh.NewClientConn(nc, c.opts.Addr, c.config)
	if err != nil {
		nc.Close()
		return nil, classifyHandshake(err)
	}
	_ = nc.SetDeadline(time.Time{})
	conn := ssh.NewClient(sc, chans, reqs)
	c.conn = conn
	if c.opts.KeepAlive > 0 {
		go c.keepAlive(conn)
	}
	return conn, nil
}

func classifyHandshake(err error) error {
	var ke *knownhosts.KeyError
	switch {
	case errors.As(err, &ke) && len(ke.Want) > 0, strings.Contains(err.Error(), "knownhosts: key mismatch"):
		return apperr.New(apperr.HostKeyMismatch, "路由器的 host key 与 known_hosts 记录不一致", "如果路由器刚重置或更换过固件，请人工核对指纹后更新 known_hosts；否则可能存在中间人攻击")
	case errors.As(err, &ke), strings.Contains(err.Error(), "knownhosts: key is unknown"):
		return apperr.New(apperr.HostKeyMismatch, "known_hosts 中没有路由器的记录", "用 ssh 登录一次并核对指纹，或用 ssh-keyscan 添加")
	case strings.Contains(err.Error(), "unable to authenticate"):
		return apperr.New(apperr.SSHAuthFailed, "SSH 密钥认证失败", "确认公钥已添加到路由器的 Authorized Keys，且 router.user 正确")
	default:
		return apperr.New(apperr.SSHUnreachable, "SSH 握手失败: "+err.Error(), hintCheckRouter)
	}
}

func (c *Client) keepAlive(conn *ssh.Client) {
	t := time.NewTicker(c.opts.KeepAlive)
	defer t.Stop()
	for range t.C {
		c.mu.Lock()
		current := c.conn == conn
		c.mu.Unlock()
		if !current {
			return
		}
		// 一个间隔内收不到应答就认为连接已失效（路由器断电或死机时 TCP 不会马上报错）
		reply := make(chan error, 1)
		go func() {
			_, _, err := conn.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		select {
		case err := <-reply:
			if err != nil {
				c.drop(conn)
				return
			}
		case <-time.After(c.opts.KeepAlive):
			c.drop(conn)
			return
		}
	}
}

// drop 在 conn 仍是当前连接时丢弃它，并关闭连接。
func (c *Client) drop(conn *ssh.Client) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	conn.Close()
}

// Reset 关闭当前连接，下次 Run 时重新连接。
func (c *Client) Reset() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// Close 关闭连接。
func (c *Client) Close() { c.Reset() }
