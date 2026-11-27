//go:build linux

package shell

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"
)

func sockopts(t *testing.T, c net.Conn) (on, idle, intvl, cnt int) {
	t.Helper()
	raw, err := c.(*net.TCPConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	err = raw.Control(func(fd uintptr) {
		get := func(level, opt int) int {
			v, err := syscall.GetsockoptInt(int(fd), level, opt)
			if err != nil {
				errs = append(errs, err)
			}
			return v
		}
		on = get(syscall.SOL_SOCKET, syscall.SO_KEEPALIVE)
		idle = get(syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE)
		intvl = get(syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL)
		cnt = get(syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT)
	})
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	return on, idle, intvl, cnt
}

func TestKeepAliveDialerSetsTheDatabaseSocketOptions(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	on := keepAliveDialer(net.KeepAliveConfig{Enable: true, Idle: 30 * time.Second, Interval: 10 * time.Second, Count: 3})
	c, err := on(ctx, l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ka, idle, intvl, cnt := sockopts(t, c); ka != 1 || idle != 30 || intvl != 10 || cnt != 3 {
		t.Fatalf("SO_KEEPALIVE %d TCP_KEEPIDLE %d TCP_KEEPINTVL %d TCP_KEEPCNT %d, want 1 30 10 3", ka, idle, intvl, cnt)
	}
	off := keepAliveDialer(net.KeepAliveConfig{Enable: false, Idle: -1, Interval: -1, Count: -1})
	d, err := off(ctx, l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if ka, _, _, _ := sockopts(t, d); ka != 0 {
		t.Fatalf("keepalive off left SO_KEEPALIVE %d", ka)
	}
}
