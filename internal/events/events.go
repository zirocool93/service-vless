package events

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Event struct {
	Type    string    `json:"type"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}
type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
}

func New() *Hub { return &Hub{subscribers: make(map[chan Event]struct{})} }
func (h *Hub) Publish(kind, message string) {
	e := Event{Type: kind, Message: message, Time: time.Now().UTC()}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subscribers {
		select {
		case c <- e:
		default:
		}
	}
}
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ServeAuthorized(w, r, func() bool { return true })
}

// ServeAuthorized повторно проверяет сессию перед событиями и heartbeat.
func (h *Hub) ServeAuthorized(w http.ResponseWriter, r *http.Request, valid func() bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "поток событий не поддерживается", 500)
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		http.Error(w, "не удалось настроить поток событий", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	c := make(chan Event, 16)
	h.mu.Lock()
	if len(h.subscribers) >= 64 {
		h.mu.Unlock()
		http.Error(w, "слишком много подключений к потоку событий", http.StatusServiceUnavailable)
		return
	}
	h.subscribers[c] = struct{}{}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.subscribers, c); h.mu.Unlock() }()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	fmt.Fprint(w, ": connected\n\n")
	f.Flush()
	for {
		select {
		case e := <-c:
			if !valid() {
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
			b, _ := json.Marshal(e)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			f.Flush()
		case <-ticker.C:
			if !valid() {
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			f.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
