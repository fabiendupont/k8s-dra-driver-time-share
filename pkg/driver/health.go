package driver

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

// HealthServer exposes HTTP health endpoints for liveness and readiness probes.
// /healthz returns 200 when the server is running (liveness).
// /readyz returns 200 only after MarkReady() has been called (readiness).
type HealthServer struct {
	port  int
	ready atomic.Bool
}

// NewHealthServer creates a health server on the given port.
func NewHealthServer(port int) *HealthServer {
	return &HealthServer{port: port}
}

// MarkReady signals that the driver is ready to serve requests.
// Call this after ResourceSlice publication and informer cache sync.
func (h *HealthServer) MarkReady() {
	h.ready.Store(true)
	klog.InfoS("Health server: marked ready")
}

// Serve starts the health HTTP server. It blocks until ctx is cancelled.
func (h *HealthServer) Serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !h.ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", h.port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	klog.InfoS("Health server listening", "port", h.port)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
