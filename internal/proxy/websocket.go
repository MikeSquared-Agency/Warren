package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type WSCounter struct {
	counts sync.Map  // hostname → *int64
	total  int64     // total across all hostnames
	done   chan struct{}
	mu     sync.Mutex
}

func NewWSCounter() *WSCounter {
	return &WSCounter{
		done: make(chan struct{}, 1),
	}
}

func (w *WSCounter) Inc(hostname string) {
	v, _ := w.counts.LoadOrStore(hostname, new(int64))
	atomic.AddInt64(v.(*int64), 1)
	atomic.AddInt64(&w.total, 1)
}

func (w *WSCounter) Dec(hostname string) {
	v, ok := w.counts.Load(hostname)
	if ok {
		atomic.AddInt64(v.(*int64), -1)
	}
	if atomic.AddInt64(&w.total, -1) <= 0 {
		// Signal waiters.
		select {
		case w.done <- struct{}{}:
		default:
		}
	}
}

func (w *WSCounter) Count(hostname string) int64 {
	v, ok := w.counts.Load(hostname)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(v.(*int64))
}

// Total returns the total number of active WebSocket connections.
func (w *WSCounter) Total() int64 {
	return atomic.LoadInt64(&w.total)
}

// Wait blocks until all WebSocket connections close or the timeout expires.
// Returns true if all connections drained, false on timeout.
func (w *WSCounter) Wait(timeout time.Duration) bool {
	if w.Total() <= 0 {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-w.done:
			if w.Total() <= 0 {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

func IsWebSocket(r *http.Request) bool {
	return connectionHasUpgrade(r.Header.Get("Connection")) &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// connectionHasUpgrade checks if "upgrade" appears as a token in a
// potentially comma-separated Connection header value.
func connectionHasUpgrade(value string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request, backend *url.URL, hostname string, ws *WSCounter, activity *ActivityTracker, logger *slog.Logger) {
	handleWebSocket(r.Context(), w, r, backend, hostname, ws, activity, logger)
}

func handleWebSocket(ctx context.Context, w http.ResponseWriter, r *http.Request, backend *url.URL, hostname string, ws *WSCounter, activity *ActivityTracker, logger *slog.Logger) {
	// Dial the backend.
	backendAddr := backend.Host
	if !strings.Contains(backendAddr, ":") {
		if backend.Scheme == "https" || backend.Scheme == "wss" {
			backendAddr += ":443"
		} else {
			backendAddr += ":80"
		}
	}

	backConn, err := net.Dial("tcp", backendAddr)
	if err != nil {
		logger.Error("websocket: failed to dial backend", "error", err, "backend", backendAddr)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}

	// Hijack the client connection.
	hj, ok := w.(http.Hijacker)
	if !ok {
		backConn.Close()
		http.Error(w, "websocket hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		backConn.Close()
		logger.Error("websocket: hijack failed", "error", err)
		return
	}

	// Forward the original request to the backend.
	if err := r.Write(backConn); err != nil {
		clientConn.Close()
		backConn.Close()
		logger.Error("websocket: failed to write request to backend", "error", err)
		return
	}

	ws.Inc(hostname)
	activity.Touch(hostname)

	// Force-close connections when context is cancelled (graceful shutdown).
	go func() {
		<-ctx.Done()
		clientConn.Close()
		backConn.Close()
	}()

	// Bidirectional copy.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(backConn, clientBuf) //nolint:errcheck
		backConn.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(clientConn, backConn) //nolint:errcheck
		clientConn.Close()
	}()

	wg.Wait()
	ws.Dec(hostname)
}
