package api

import (
	"fmt"
	"net/http"
	"sync"
)

type SSEBroker struct {
	mu      sync.RWMutex
	clients map[string]map[chan string]struct{}
}

func NewSSEBroker() *SSEBroker {
	return &SSEBroker{clients: map[string]map[chan string]struct{}{}}
}

func (b *SSEBroker) Publish(userID, data string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.clients[userID] {
		select {
		case ch <- data:
		default:
		}
	}
}

func (b *SSEBroker) ServeHTTP(w http.ResponseWriter, r *http.Request, userID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := make(chan string, 8)
	b.mu.Lock()
	if _, ok := b.clients[userID]; !ok {
		b.clients[userID] = map[chan string]struct{}{}
	}
	b.clients[userID][ch] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.clients[userID], ch)
		if len(b.clients[userID]) == 0 {
			delete(b.clients, userID)
		}
		b.mu.Unlock()
		close(ch)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}
