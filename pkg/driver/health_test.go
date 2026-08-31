package driver

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestHealthServerReadiness(t *testing.T) {
	hs := NewHealthServer(0) // port 0 = random available port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a channel to capture the actual port.
	portCh := make(chan int, 1)
	go func() {
		// We need the actual port, so start on a known port for the test.
		port := 18923
		hs.port = port
		portCh <- port
		_ = hs.Serve(ctx)
	}()

	port := <-portCh
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait for server to be listening.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// /healthz should always return 200.
	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200", resp.StatusCode)
	}

	// /readyz should return 503 before MarkReady.
	resp, err = http.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz before MarkReady: status = %d, want 503", resp.StatusCode)
	}

	// Mark ready.
	hs.MarkReady()

	// /readyz should now return 200.
	resp, err = http.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/readyz after MarkReady: status = %d, want 200", resp.StatusCode)
	}
}
