// Package httpproxy implements an HTTP CONNECT client, plus DNS-over-TCP
// through CONNECT for proxies that cannot carry UDP.
package httpproxy

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"ashgate-lite/internal/proxycore/internal/netx"
)

// Dial opens a TCP tunnel to host:port through an HTTP proxy.
func Dial(ctx context.Context, srv netx.Server, host string, port uint16) (net.Conn, error) {
	conn, err := netx.Dial(ctx, srv.Address)
	if err != nil {
		return nil, err
	}
	done := netx.Bound(ctx, conn, netx.DialTimeout)
	err = handshake(conn, srv, host, port)
	if derr := done(); err == nil {
		err = derr
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// DNSQuery sends one DNS message to server (host:port) through an HTTP CONNECT
// tunnel using DNS-over-TCP framing, and returns the response message.
func DNSQuery(ctx context.Context, srv netx.Server, server string, query []byte) ([]byte, error) {
	if len(query) == 0 || len(query) > 65535 {
		return nil, errors.New("http proxy: dns query has an invalid length")
	}
	host, portStr, err := net.SplitHostPort(server)
	if err != nil {
		return nil, err
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil {
		return nil, err
	}
	conn, err := Dial(ctx, srv, host, uint16(port))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(netx.Deadline(ctx, 10*time.Second))
	var lenb [2]byte
	binary.BigEndian.PutUint16(lenb[:], uint16(len(query)))
	if _, err := conn.Write(lenb[:]); err != nil {
		return nil, err
	}
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, lenb[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(lenb[:])
	if n == 0 || n > 65535 {
		return nil, fmt.Errorf("http proxy: dns response length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// handshake runs under the deadline set by netx.Bound in Dial.
func handshake(conn net.Conn, srv netx.Server, host string, port uint16) error {
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\n", target)
	fmt.Fprintf(&b, "Host: %s\r\n", target)
	fmt.Fprintf(&b, "Proxy-Connection: keep-alive\r\n")
	if srv.HasAuth() {
		token := base64.StdEncoding.EncodeToString([]byte(srv.Username + ":" + srv.Password))
		fmt.Fprintf(&b, "Proxy-Authorization: Basic %s\r\n", token)
	}
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		return err
	}
	status, text, err := readStatus(conn)
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return fmt.Errorf("http proxy: CONNECT %s returned %d %s", target, status, text)
	}
	return nil
}

func readStatus(r io.Reader) (int, string, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 1)
	for len(buf) < 64*1024 {
		if _, err := io.ReadFull(r, tmp); err != nil {
			return 0, "", err
		}
		buf = append(buf, tmp[0])
		if len(buf) >= 4 && string(buf[len(buf)-4:]) == "\r\n\r\n" {
			break
		}
	}
	if len(buf) < 12 {
		return 0, "", errors.New("http proxy: short CONNECT response")
	}
	line := buf
	if i := strings.Index(string(buf), "\r\n"); i >= 0 {
		line = buf[:i]
	}
	parts := strings.SplitN(string(line), " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, "", fmt.Errorf("http proxy: malformed status %q", string(line))
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, "", fmt.Errorf("http proxy: malformed status %q", string(line))
	}
	text := ""
	if len(parts) == 3 {
		text = parts[2]
	}
	return code, text, nil
}
