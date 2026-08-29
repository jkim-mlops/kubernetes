package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A tiny, dependency-free RESP2 client.
//
// The lab only needs a handful of list commands. Keeping mlsim stdlib-only
// means the image builds without network access and there is no module cache
// to invalidate when you rebuild between scenarios.

type redisConn struct {
	c  net.Conn
	br *bufio.Reader
}

func dialRedis(addr string) (*redisConn, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &redisConn{c: c, br: bufio.NewReader(c)}, nil
}

func (r *redisConn) Close() error { return r.c.Close() }

// do sends a command and reads one reply. deadline bounds the whole exchange;
// blocking commands (BRPOP/BLPOP) need it set beyond their own timeout.
func (r *redisConn) do(deadline time.Duration, args ...string) (any, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if err := r.c.SetDeadline(time.Now().Add(deadline)); err != nil {
		return nil, err
	}
	if _, err := r.c.Write([]byte(b.String())); err != nil {
		return nil, err
	}
	return r.read()
}

func (r *redisConn) read() (any, error) {
	line, err := r.br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, errors.New("empty reply")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, errors.New(line[1:])
	case ':':
		return strconv.ParseInt(line[1:], 10, 64)
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil // null bulk string
		}
		buf := make([]byte, n+2) // payload + CRLF
		if _, err := ioReadFull(r.br, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, nil // null array — BRPOP timed out
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := r.read()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected RESP type %q", line[0])
	}
}

func ioReadFull(br *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := br.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ---------------------------------------------------------------- pool ----

// redisPool hands out connections to goroutines. Blocking reads occupy a
// connection for their whole duration, so the gateway needs more than one.
type redisPool struct {
	addr string
	mu   sync.Mutex
	idle []*redisConn
}

func newRedisPool(addr string) *redisPool { return &redisPool{addr: addr} }

func (p *redisPool) get() (*redisConn, error) {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		c := p.idle[n-1]
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()
	return dialRedis(p.addr)
}

// put returns a healthy connection to the pool; a connection that errored is
// closed instead, because its stream position is no longer trustworthy.
func (p *redisPool) put(c *redisConn, err error) {
	if c == nil {
		return
	}
	if err != nil {
		_ = c.Close()
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) >= 64 {
		_ = c.Close()
		return
	}
	p.idle = append(p.idle, c)
}

// ------------------------------------------------------------ commands ----

func (p *redisPool) LPush(key, value string) error {
	c, err := p.get()
	if err != nil {
		return err
	}
	_, err = c.do(5*time.Second, "LPUSH", key, value)
	p.put(c, err)
	return err
}

func (p *redisPool) LLen(key string) (int64, error) {
	c, err := p.get()
	if err != nil {
		return 0, err
	}
	v, err := c.do(5*time.Second, "LLEN", key)
	p.put(c, err)
	if err != nil {
		return 0, err
	}
	n, _ := v.(int64)
	return n, nil
}

func (p *redisPool) Del(key string) error {
	c, err := p.get()
	if err != nil {
		return err
	}
	_, err = c.do(5*time.Second, "DEL", key)
	p.put(c, err)
	return err
}

// PushReply publishes a worker's answer and expires it so a gateway that has
// already given up does not leak keys.
func (p *redisPool) PushReply(key, value string, ttl time.Duration) error {
	c, err := p.get()
	if err != nil {
		return err
	}
	if _, err = c.do(5*time.Second, "LPUSH", key, value); err == nil {
		_, err = c.do(5*time.Second, "EXPIRE", key, strconv.Itoa(int(ttl.Seconds())))
	}
	p.put(c, err)
	return err
}

// BPop blocks until an element arrives on key or timeout elapses.
// Returns ("", nil) on timeout.
func (p *redisPool) BPop(cmd, key string, timeout time.Duration) (string, error) {
	c, err := p.get()
	if err != nil {
		return "", err
	}
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	v, err := c.do(timeout+10*time.Second, cmd, key, strconv.Itoa(secs))
	p.put(c, err)
	if err != nil {
		return "", err
	}
	arr, ok := v.([]any)
	if !ok || len(arr) < 2 { // null array => timed out
		return "", nil
	}
	s, _ := arr[1].(string)
	return s, nil
}
