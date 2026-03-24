package driver

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	drav1beta1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
)

func TestRegistrarGetInfo(t *testing.T) {
	driverName := "test-driver.example.com"
	endpoint := "/var/lib/kubelet/plugins/test-driver.example.com/plugin.sock"

	registrar := NewRegistrar(driverName, endpoint)
	info, err := registrar.GetInfo(context.Background(), &registerapi.InfoRequest{})
	if err != nil {
		t.Fatalf("GetInfo failed: %v", err)
	}

	if info.Type != registerapi.DRAPlugin {
		t.Errorf("type = %q, want %q", info.Type, registerapi.DRAPlugin)
	}
	if info.Name != driverName {
		t.Errorf("name = %q, want %q", info.Name, driverName)
	}
	if info.Endpoint != endpoint {
		t.Errorf("endpoint = %q, want %q", info.Endpoint, endpoint)
	}
	if len(info.SupportedVersions) != 1 || info.SupportedVersions[0] != drav1beta1.DRAPluginService {
		t.Errorf("supportedVersions = %v, want [%s]", info.SupportedVersions, drav1beta1.DRAPluginService)
	}
}

func TestRegistrarNotifyRegistrationStatus(t *testing.T) {
	registrar := NewRegistrar("test-driver", "/tmp/test.sock")

	// Success case.
	_, err := registrar.NotifyRegistrationStatus(context.Background(), &registerapi.RegistrationStatus{
		PluginRegistered: true,
	})
	if err != nil {
		t.Fatalf("NotifyRegistrationStatus (success) failed: %v", err)
	}

	// Failure case — should not error, just log.
	_, err = registrar.NotifyRegistrationStatus(context.Background(), &registerapi.RegistrationStatus{
		PluginRegistered: false,
		Error:            "some error",
	})
	if err != nil {
		t.Fatalf("NotifyRegistrationStatus (failure) failed: %v", err)
	}
}

func TestRegistrarServe(t *testing.T) {
	tmpDir := t.TempDir()
	driverName := "test-driver.example.com"
	endpoint := "/tmp/test-plugin.sock"

	registrar := NewRegistrar(driverName, endpoint)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- registrar.Serve(ctx, tmpDir)
	}()

	// Wait for the socket to appear.
	socketPath := filepath.Join(tmpDir, driverName+"-reg.sock")
	waitForSocket(t, socketPath)

	// Connect and call GetInfo.
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	client := registerapi.NewRegistrationClient(conn)
	info, err := client.GetInfo(context.Background(), &registerapi.InfoRequest{})
	if err != nil {
		t.Fatalf("GetInfo RPC failed: %v", err)
	}
	if info.Name != driverName {
		t.Errorf("name = %q, want %q", info.Name, driverName)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("socket %s did not become available", path)
}
