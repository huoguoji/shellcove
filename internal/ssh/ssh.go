// Package ssh 负责建立 SSH 连接（直连 / Socks5 / HTTP 代理 / 跳板机）并托管终端会话。
package ssh

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
	xproxy "golang.org/x/net/proxy"

	"github.com/huoguoji/shellcove/internal/apperr"
	"github.com/huoguoji/shellcove/internal/sshinfo"
)

// DialTimeout 建立 TCP/SSH 握手的超时时间。
const DialTimeout = 15 * time.Second

// Dial 按凭证配置建立 SSH 连接，自动处理代理与跳板机。
func Dial(creds *sshinfo.Credentials) (*ssh.Client, error) {
	if creds == nil {
		return nil, apperr.ErrBadRequest.WithMessage("缺少连接凭证")
	}
	config, err := BuildClientConfig(creds)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(creds.Host, strconv.Itoa(creds.Port))

	conn, err := dialTCP(creds, addr)
	if err != nil {
		return nil, apperr.New("SSH_DIAL_FAILED", "网络连接失败："+err.Error(), 502)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return nil, apperr.New("SSH_HANDSHAKE_FAILED", "SSH 握手或认证失败："+err.Error(), 502)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// BuildClientConfig 组装 ssh.ClientConfig。
func BuildClientConfig(creds *sshinfo.Credentials) (*ssh.ClientConfig, error) {
	methods, err := authMethods(creds)
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User: creds.Username,
		Auth: methods,
		// 自托管场景下服务器指纹由使用者自行判断，这里不做主机密钥校验。
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         DialTimeout,
		ClientVersion:   "SSH-2.0-ShellCove",
	}, nil
}

// authMethods 依据认证方式生成认证方法列表，优先使用配置的认证方式。
func authMethods(creds *sshinfo.Credentials) ([]ssh.AuthMethod, error) {
	var passwordAuth, keyAuth ssh.AuthMethod

	if creds.Password != "" {
		passwordAuth = ssh.Password(creds.Password)
	}
	keyboardAuth := ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range answers {
			answers[i] = creds.Password
		}
		return answers, nil
	})

	if creds.PrivateKey != "" {
		signer, err := parsePrivateKey(creds.PrivateKey, creds.Passphrase)
		if err != nil {
			return nil, apperr.ErrBadRequest.WithMessage("私钥解析失败：" + err.Error())
		}
		keyAuth = ssh.PublicKeys(signer)
	}

	methods := make([]ssh.AuthMethod, 0, 3)
	switch creds.AuthType {
	case sshinfo.AuthPublicKey:
		if keyAuth != nil {
			methods = append(methods, keyAuth)
		}
		if passwordAuth != nil {
			methods = append(methods, passwordAuth)
		}
	case sshinfo.AuthKeyboardInteractive:
		if creds.Password != "" {
			methods = append(methods, keyboardAuth)
		}
		if passwordAuth != nil {
			methods = append(methods, passwordAuth)
		}
	default: // password
		if passwordAuth != nil {
			methods = append(methods, passwordAuth)
		}
		if creds.Password != "" {
			methods = append(methods, keyboardAuth)
		}
		if keyAuth != nil {
			methods = append(methods, keyAuth)
		}
	}

	if len(methods) == 0 {
		return nil, apperr.ErrBadRequest.WithMessage("没有可用的认证凭证，请先配置密码或私钥")
	}
	return methods, nil
}

func parsePrivateKey(privateKey, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(privateKey), []byte(passphrase))
	}
	return ssh.ParsePrivateKey([]byte(privateKey))
}

// dialTCP 建立到底层主机的 TCP 连接，按需经过跳板机或代理。
func dialTCP(creds *sshinfo.Credentials, addr string) (net.Conn, error) {
	// 跳板机优先：先连跳板机，再由跳板机拨号到目标主机。
	if creds.Jump != nil {
		jumpClient, err := Dial(creds.Jump)
		if err != nil {
			return nil, fmt.Errorf("跳板机连接失败: %w", err)
		}
		conn, err := jumpClient.Dial("tcp", addr)
		if err != nil {
			jumpClient.Close()
			return nil, fmt.Errorf("通过跳板机连接目标主机失败: %w", err)
		}
		return &jumpConn{Conn: conn, closer: jumpClient}, nil
	}

	switch creds.ProxyType {
	case sshinfo.ProxySocks5:
		return dialSocks5(creds.ProxyHost, creds.ProxyPort, addr)
	case sshinfo.ProxyHTTP:
		return dialHTTPProxy(creds.ProxyHost, creds.ProxyPort, addr)
	}
	return net.DialTimeout("tcp", addr, DialTimeout)
}

// jumpConn 让跳板机连接在关闭时一并释放底层 SSH 客户端。
type jumpConn struct {
	net.Conn
	closer interface{ Close() error }
}

func (c *jumpConn) Close() error {
	err := c.Conn.Close()
	if c.closer != nil {
		c.closer.Close()
	}
	return err
}

func dialSocks5(proxyHost string, proxyPort int, target string) (net.Conn, error) {
	proxyAddr := net.JoinHostPort(proxyHost, strconv.Itoa(proxyPort))
	dialer, err := xproxy.SOCKS5("tcp", proxyAddr, nil, &net.Dialer{Timeout: DialTimeout})
	if err != nil {
		return nil, fmt.Errorf("Socks5 代理初始化失败: %w", err)
	}
	type contextDialer interface {
		DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	}
	ctx, cancel := context.WithTimeout(context.Background(), DialTimeout)
	defer cancel()

	var conn net.Conn
	if cd, ok := dialer.(contextDialer); ok {
		conn, err = cd.DialContext(ctx, "tcp", target)
	} else {
		conn, err = dialer.Dial("tcp", target)
	}
	if err != nil {
		return nil, fmt.Errorf("Socks5 代理连接失败: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(DialTimeout))
	return conn, nil
}

func dialHTTPProxy(proxyHost string, proxyPort int, target string) (net.Conn, error) {
	proxyAddr := net.JoinHostPort(proxyHost, strconv.Itoa(proxyPort))
	conn, err := net.DialTimeout("tcp", proxyAddr, DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("HTTP 代理连接失败: %w", err)
	}
	if err := conn.SetDeadline(time.Now().Add(DialTimeout)); err != nil {
		conn.Close()
		return nil, err
	}

	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\nProxy-Connection: keep-alive\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("HTTP 代理 CONNECT 请求失败: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("HTTP 代理响应解析失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("HTTP 代理拒绝连接: %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// KeepAlive 发送 keepalive 请求，探测连接是否仍然可用。
func KeepAlive(client *ssh.Client) error {
	if client == nil {
		return fmt.Errorf("连接已关闭")
	}
	_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	return err
}
