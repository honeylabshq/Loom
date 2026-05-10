package server

import (
	"net"
	"sync"
)

// limitListener caps the number of concurrent TCP connections accepted.
// Excess Accept calls block until an existing connection is closed.
type limitListener struct {
	net.Listener
	sem chan struct{}
}

func newLimitListener(l net.Listener, max int) net.Listener {
	return &limitListener{Listener: l, sem: make(chan struct{}, max)}
}

func (l *limitListener) Accept() (net.Conn, error) {
	l.sem <- struct{}{}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{Conn: c, release: func() { <-l.sem }}, nil
}

type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}
