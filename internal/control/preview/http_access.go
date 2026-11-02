package preview

import (
	"context"
	"errors"
	"github.com/DROWNING2003/Isodo/internal/runtimeports"
	"strings"
	"sync"
	"time"
)

var ErrRuntimeUnavailable = errors.New("preview runtime unavailable")

type PortPolicy struct {
	Visibility string `json:"visibility"`
	Revision   uint64 `json:"-"`
}
type HTTPAccess struct {
	PortPolicy
	Grant   HTTPGrant
	Prepare func(context.Context) (HTTPGrant, error)
}
type HTTPAccessResolver func(context.Context, string, uint16) (HTTPAccess, error)

func ValidHTTPPort(port uint16) bool {
	return port != 0 && !runtimeports.ReservedHTTP(uint32(port))
}

// ConfigureHTTPAccess is called once before serving, alongside ConfigureHTTP.
func (p *Publisher) ConfigureHTTPAccess(resolve HTTPAccessResolver) {
	if p != nil {
		p.httpAccess = resolve
	}
}

type portKey struct {
	id   string
	port uint16
}
type publicBucket struct {
	tokens  float64
	updated time.Time
	active  int
}

// These are per-Gateway budgets, keyed by the validated Sandbox and port rather
// than client-controlled forwarding headers. A global budget bounds all keys.
type publicAdmission struct {
	mu      sync.Mutex
	buckets map[portKey]*publicBucket
	global  publicBucket
	active  int
}

func (a *publicAdmission) acquire(key portKey) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if a.buckets == nil {
		a.buckets = make(map[portKey]*publicBucket)
		a.global = publicBucket{tokens: 200, updated: now}
	}
	b := a.buckets[key]
	if b == nil {
		if len(a.buckets) >= 2048 {
			for k, v := range a.buckets {
				if v.active == 0 && now.Sub(v.updated) > time.Minute {
					delete(a.buckets, k)
				}
			}
			if len(a.buckets) >= 2048 {
				return nil, false
			}
		}
		b = &publicBucket{tokens: 20, updated: now}
		a.buckets[key] = b
	}
	refill := func(b *publicBucket, rate, burst float64) {
		b.tokens = min(burst, b.tokens+now.Sub(b.updated).Seconds()*rate)
		b.updated = now
	}
	refill(b, 10, 20)
	refill(&a.global, 100, 200)
	if b.tokens < 1 || a.global.tokens < 1 || b.active >= 8 || a.active >= 256 {
		return nil, false
	}
	b.tokens--
	a.global.tokens--
	b.active++
	a.active++
	var once sync.Once
	return func() { once.Do(func() { a.mu.Lock(); b.active--; a.active--; a.mu.Unlock() }) }, true
}

type httpStreams struct {
	mu    sync.Mutex
	next  uint64
	ports map[portKey]map[uint64]context.CancelFunc
}

func (s *httpStreams) add(key portKey, cancel context.CancelFunc) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ports == nil {
		s.ports = make(map[portKey]map[uint64]context.CancelFunc)
	}
	if s.ports[key] == nil {
		s.ports[key] = make(map[uint64]context.CancelFunc)
	}
	s.next++
	index := s.next
	s.ports[key][index] = cancel
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.ports[key], index)
		if len(s.ports[key]) == 0 {
			delete(s.ports, key)
		}
	}
}

// Called after the policy transaction commits and before acknowledging a change.
func (p *Publisher) CancelHTTPPort(id string, port uint16) {
	p.httpStreams.mu.Lock()
	defer p.httpStreams.mu.Unlock()
	for _, cancel := range p.httpStreams.ports[portKey{strings.ToLower(id), port}] {
		cancel()
	}
}
