package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"cloud.google.com/go/cloudsqlconn"
)

// Manager runs one local listener per enabled instance, all sharing a dialer.
type Manager struct {
	cfg  *ConfigStore
	auth *Auth

	mu        sync.Mutex
	connected bool
	dialer    *cloudsqlconn.Dialer
	proxies   map[string]*proxy

	OnChange func()
}

func NewManager(cfg *ConfigStore, a *Auth) *Manager {
	return &Manager{cfg: cfg, auth: a, proxies: map[string]*proxy{}}
}

// Status returns whether the proxies are connected and how many are running.
func (m *Manager) Status() (connected bool, running int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected, len(m.proxies)
}

func (m *Manager) changed() {
	if m.OnChange != nil {
		m.OnChange()
	}
}

// Connect starts a proxy for every enabled instance.
func (m *Manager) Connect() error {
	defer m.changed()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connected {
		return nil
	}

	creds, err := m.auth.Credentials()
	if err != nil {
		return err
	}
	dialer, err := cloudsqlconn.NewDialer(context.Background(),
		cloudsqlconn.WithCredentials(creds),
		cloudsqlconn.WithLazyRefresh(),
		cloudsqlconn.WithDebugLogger(debugLogger{}),
	)
	if err != nil {
		return fmt.Errorf("create dialer: %w", err)
	}
	m.dialer = dialer
	m.connected = true

	for _, t := range m.cfg.Enabled() {
		m.startLocked(t.Instance, t.Port)
	}
	log.Printf("Connected %d proxies", len(m.proxies))
	return nil
}

// Disconnect stops all proxies.
func (m *Manager) Disconnect() {
	defer m.changed()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnectLocked()
}

func (m *Manager) disconnectLocked() {
	if !m.connected {
		return
	}
	for connName := range m.proxies {
		m.stopLocked(connName)
	}
	_ = m.dialer.Close()
	m.dialer = nil
	m.connected = false
	log.Println("Disconnected all proxies")
}

// Reconnect restarts all proxies with freshly loaded credentials.
func (m *Manager) Reconnect() error {
	m.mu.Lock()
	wasConnected := m.connected
	m.disconnectLocked()
	m.mu.Unlock()
	if !wasConnected {
		m.changed()
		return nil
	}
	return m.Connect()
}

// Apply brings the proxy of one instance in line with its configuration.
func (m *Manager) Apply(connName string) {
	defer m.changed()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.connected {
		return
	}
	t, _ := m.cfg.Get(connName)
	if p, ok := m.proxies[connName]; ok {
		if t.Enabled && p.port == t.Port {
			return
		}
		m.stopLocked(connName)
	}
	if t.Enabled {
		m.startLocked(connName, t.Port)
	}
}

func (m *Manager) startLocked(connName string, port int) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[%s] failed to listen on %s: %v", connName, addr, err)
		return
	}
	p := &proxy{connName: connName, port: port, listener: l, dialer: m.dialer, conns: map[net.Conn]struct{}{}}
	m.proxies[connName] = p
	go p.serve()
	log.Printf("[%s] listening on %s", connName, addr)
}

func (m *Manager) stopLocked(connName string) {
	p, ok := m.proxies[connName]
	if !ok {
		return
	}
	p.close()
	delete(m.proxies, connName)
	log.Printf("[%s] stopped listening on port %d", connName, p.port)
}

type proxy struct {
	connName string
	port     int
	listener net.Listener
	dialer   *cloudsqlconn.Dialer

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

func (p *proxy) serve() {
	for {
		c, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

// track registers a connection so close() can terminate it. It returns false
// if the proxy is already closed.
func (p *proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.conns[c] = struct{}{}
	return true
}

func (p *proxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
}

func (p *proxy) handle(client net.Conn) {
	defer client.Close()
	if !p.track(client) {
		return
	}
	defer p.untrack(client)

	server, err := p.dialer.Dial(context.Background(), p.connName)
	if err != nil {
		log.Printf("[%s] failed to connect: %v", p.connName, err)
		return
	}
	defer server.Close()
	if !p.track(server) {
		return
	}
	defer p.untrack(server)
	log.Printf("[%s] client %s connected", p.connName, client.RemoteAddr())

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(server, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, server)
		done <- struct{}{}
	}()
	<-done
	log.Printf("[%s] client %s disconnected", p.connName, client.RemoteAddr())
}

func (p *proxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for c := range p.conns {
		_ = c.Close()
	}
}

type debugLogger struct{}

func (debugLogger) Debugf(format string, args ...any) {
	log.Printf("[cloudsqlconn] "+format, args...)
}
