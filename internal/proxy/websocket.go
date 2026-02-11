package proxy

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

type WSCounter struct {
	counts sync.Map // hostname → *int64
}

func NewWSCounter() *WSCounter {
	return &WSCounter{}
}

func (w *WSCounter) Inc(hostname string) {
	v, _ := w.counts.LoadOrStore(hostname, new(int64))
	atomic.AddInt64(v.(*int64), 1)
}

func (w *WSCounter) Dec(hostname string) {
	v, ok := w.counts.Load(hostname)
	if ok {
		atomic.AddInt64(v.(*int64), -1)
	}
}

func (w *WSCounter) Count(hostname string) int64 {
	v, ok := w.counts.Load(hostname)
	if !ok {
		return 0
	}
	return atomic.LoadInt64(v.(*int64))
}

func IsWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request, backend *url.URL, hostname string, ws *WSCounter, activity *ActivityTracker, logger *slog.Logger) {
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
