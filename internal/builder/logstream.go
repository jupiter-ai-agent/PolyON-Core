package builder

import (
	"sync"
)

// LogStream provides real-time log broadcasting for builds.
type LogStream struct {
	mu          sync.RWMutex
	subscribers map[string][]chan string // buildKey → channels
}

// NewLogStream creates a LogStream.
func NewLogStream() *LogStream {
	return &LogStream{
		subscribers: make(map[string][]chan string),
	}
}

// Subscribe returns a channel that receives log lines for a build.
func (ls *LogStream) Subscribe(key string) chan string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ch := make(chan string, 100)
	ls.subscribers[key] = append(ls.subscribers[key], ch)
	return ch
}

// Unsubscribe removes a channel.
func (ls *LogStream) Unsubscribe(key string, ch chan string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	subs := ls.subscribers[key]
	for i, s := range subs {
		if s == ch {
			ls.subscribers[key] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	// safe close — may already be closed by CloseAll
	defer func() { recover() }()
	close(ch)
}

// Publish sends a log line to all subscribers of a build.
func (ls *LogStream) Publish(key string, line string) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	for _, ch := range ls.subscribers[key] {
		select {
		case ch <- line:
		default:
			// Drop if subscriber is slow
		}
	}
}

// CloseAll closes all subscribers for a build key.
func (ls *LogStream) CloseAll(key string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	for _, ch := range ls.subscribers[key] {
		close(ch)
	}
	delete(ls.subscribers, key)
}
