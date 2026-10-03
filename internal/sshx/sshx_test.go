package sshx

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/rshun/merlin-mcp/internal/apperr"
)

type execHandler func(cmd string, stdin []byte) (stdout, stderr string, code int)

type testServer struct {
	addr    string
	hostKey ssh.Signer
	ln      net.Listener
}

func newKey(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

// startServer 启动只接受 authorized 公钥、只支持 exec 请求的 SSH 服务器。
func startServer(t *testing.T, authorized ssh.PublicKey, h execHandler) *testServer {
	t.Helper()
	hostKey, _ := newKey(t)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(k.Marshal(), authorized.Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unauthorized")
		},
	}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serveConn(nc, cfg, h)
		}
	}()
	return &testServer{addr: ln.Addr().String(), hostKey: hostKey, ln: ln}
}

func serveConn(nc net.Conn, cfg *ssh.ServerConfig, h execHandler) {
	sc, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(ssh.UnknownChannelType, "only session")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range creqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				_ = ssh.Unmarshal(req.Payload, &p)
				req.Reply(true, nil)
				go func(cmd string) {
					stdin, _ := io.ReadAll(ch)
					out, errOut, code := h(cmd, stdin)
					io.WriteString(ch, out)
					io.WriteString(ch.Stderr(), errOut)
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
					ch.Close()
				}(p.Command)
			}
		}()
	}
}

// clientOptions 写出客户端私钥和 known_hosts（记录 knownKey 作为服务器的 host key）。
func clientOptions(t *testing.T, addr string, clientPriv ed25519.PrivateKey, knownKey ssh.PublicKey) Options {
	t.Helper()
	dir := t.TempDir()
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "id")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	khPath := filepath.Join(dir, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, knownKey)
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Options{Addr: addr, User: "admin", KeyFile: keyPath, KnownHosts: khPath, Timeout: 2 * time.Second, DialTimeout: 2 * time.Second}
}

func echoHandler(cmd string, stdin []byte) (string, string, int) {
	return "cmd=" + cmd + "|stdin=" + string(stdin), "warn", 3
}

func TestRunReturnsOutputAndExitCode(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := c.Run(context.Background(), "echo hi", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "cmd=echo hi|stdin=data" || string(res.Stderr) != "warn" || res.ExitCode != 3 {
		t.Fatalf("res = %+v", res)
	}
	res, err = c.Run(context.Background(), "again", nil)
	if err != nil || string(res.Stdout) != "cmd=again|stdin=" {
		t.Fatalf("复用连接失败: res=%q err=%v", res.Stdout, err)
	}
}

func TestNewRejectsHostMissingFromKnownHosts(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	opts := clientOptions(t, "192.0.2.99:22", clientPriv, srv.hostKey.PublicKey())
	opts.Addr = srv.addr
	_, err := New(opts)
	if err == nil || !strings.Contains(err.Error(), "known_hosts 中没有") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDetectsHostKeyMismatch(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	other, _ := newKey(t)
	c, err := New(clientOptions(t, srv.addr, clientPriv, other.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Run(context.Background(), "x", nil)
	if apperr.CodeOf(err) != apperr.HostKeyMismatch {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDetectsAuthFailure(t *testing.T) {
	authorized, _ := newKey(t)
	_, clientPriv := newKey(t)
	srv := startServer(t, authorized.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Run(context.Background(), "x", nil)
	if apperr.CodeOf(err) != apperr.SSHAuthFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestRunTimesOut(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), func(string, []byte) (string, string, int) {
		time.Sleep(time.Second)
		return "", "", 0
	})
	opts := clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey())
	opts.Timeout = 100 * time.Millisecond
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Run(context.Background(), "sleep", nil)
	if apperr.CodeOf(err) != apperr.Timeout {
		t.Fatalf("err = %v", err)
	}
}

func TestRunReportsUnreachable(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv.ln.Close()
	_, err = c.Run(context.Background(), "x", nil)
	if apperr.CodeOf(err) != apperr.SSHUnreachable {
		t.Fatalf("err = %v", err)
	}
}

// frozenProxy 转发 TCP 流量；冻结后吞掉数据但不关闭连接，模拟路由器死机或断电后的"黑洞"连接。
type frozenProxy struct {
	ln     net.Listener
	frozen atomic.Bool
	mu     sync.Mutex
	conns  []net.Conn
}

func startProxy(t *testing.T, target string) *frozenProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &frozenProxy{ln: ln}
	t.Cleanup(func() {
		ln.Close()
		p.mu.Lock()
		for _, c := range p.conns {
			c.Close()
		}
		p.mu.Unlock()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, u)
			p.mu.Unlock()
			go p.pipe(c, u)
			go p.pipe(u, c)
		}
	}()
	return p
}

func (p *frozenProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.frozen.Load() {
			dst.Write(buf[:n])
		}
		if err != nil {
			dst.Close()
			return
		}
	}
}

// 调用方的 deadline 比 command_timeout 长时，单条命令仍应在 command_timeout 内超时。
func TestRunCapsAtCommandTimeoutEvenWithLongerDeadline(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), func(string, []byte) (string, string, int) {
		time.Sleep(time.Second)
		return "", "", 0
	})
	opts := clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey())
	opts.Timeout = 100 * time.Millisecond
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err = c.Run(ctx, "sleep", nil)
	if apperr.CodeOf(err) != apperr.Timeout || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("err=%v elapsed=%s", err, time.Since(start))
	}
}

// 连接变成黑洞后，调用必须在超时内返回，并丢弃坏连接，恢复后能重新连上。
func TestRunDoesNotHangOnFrozenConnection(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	proxy := startProxy(t, srv.addr)
	opts := clientOptions(t, proxy.ln.Addr().String(), clientPriv, srv.hostKey.PublicKey())
	opts.Timeout = 300 * time.Millisecond
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Run(context.Background(), "warmup", nil); err != nil {
		t.Fatal(err)
	}

	proxy.frozen.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := c.Run(context.Background(), "x", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if code := apperr.CodeOf(err); code != apperr.Timeout && code != apperr.SSHUnreachable {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("黑洞连接上的调用 3 秒后仍未返回")
	}

	proxy.frozen.Store(false)
	if _, err := c.Run(context.Background(), "after", nil); err != nil {
		t.Fatalf("坏连接应已丢弃，恢复后应能重新连接: %v", err)
	}
}

// keepalive 收不到应答时应丢弃连接。
func TestKeepAliveDropsDeadConnection(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	proxy := startProxy(t, srv.addr)
	opts := clientOptions(t, proxy.ln.Addr().String(), clientPriv, srv.hostKey.PublicKey())
	opts.KeepAlive = 100 * time.Millisecond
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Run(context.Background(), "warmup", nil); err != nil {
		t.Fatal(err)
	}
	proxy.frozen.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		gone := c.conn == nil
		c.mu.Unlock()
		if gone {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("keepalive 没有丢弃已失效的连接")
}

func TestResetReconnects(t *testing.T) {
	clientKey, clientPriv := newKey(t)
	srv := startServer(t, clientKey.PublicKey(), echoHandler)
	c, err := New(clientOptions(t, srv.addr, clientPriv, srv.hostKey.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Run(context.Background(), "a", nil); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	if _, err := c.Run(context.Background(), "b", nil); err != nil {
		t.Fatalf("Reset 后应能重连: %v", err)
	}
}
