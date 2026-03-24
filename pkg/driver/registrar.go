package driver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	registerapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"

	drav1beta1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
)

// Registrar implements the kubelet plugin registration gRPC service.
// It tells the kubelet what type of plugin this is and where its
// DRA endpoint socket lives.
type Registrar struct {
	driverName string
	endpoint   string // path to the DRA plugin socket
}

var _ registerapi.RegistrationServer = &Registrar{}

// NewRegistrar creates a plugin registrar.
// endpoint is the absolute path to the DRA plugin socket (e.g.,
// /var/lib/kubelet/plugins/time-share.fabiendupont.io/plugin.sock).
func NewRegistrar(driverName, endpoint string) *Registrar {
	return &Registrar{
		driverName: driverName,
		endpoint:   endpoint,
	}
}

// GetInfo is called by the kubelet plugin watcher to discover the plugin type,
// driver name, supported versions, and DRA endpoint.
func (r *Registrar) GetInfo(ctx context.Context, req *registerapi.InfoRequest) (*registerapi.PluginInfo, error) {
	klog.InfoS("GetInfo called by kubelet plugin watcher")
	return &registerapi.PluginInfo{
		Type:              registerapi.DRAPlugin,
		Name:              r.driverName,
		Endpoint:          r.endpoint,
		SupportedVersions: []string{drav1beta1.DRAPluginService},
	}, nil
}

// NotifyRegistrationStatus is called by the kubelet to inform the plugin
// whether registration was successful.
func (r *Registrar) NotifyRegistrationStatus(ctx context.Context, status *registerapi.RegistrationStatus) (*registerapi.RegistrationStatusResponse, error) {
	if status.PluginRegistered {
		klog.InfoS("Plugin registered with kubelet successfully",
			"driver", r.driverName)
	} else {
		klog.ErrorS(nil, "Plugin registration failed",
			"driver", r.driverName, "error", status.Error)
	}
	return &registerapi.RegistrationStatusResponse{}, nil
}

// Serve starts the registration gRPC server on a Unix socket in the kubelet
// plugin registry directory. It blocks until ctx is cancelled.
//
// registryDir is typically /var/lib/kubelet/plugins_registry/.
// The socket is named <driverName>-reg.sock.
func (r *Registrar) Serve(ctx context.Context, registryDir string) error {
	socketPath := filepath.Join(registryDir, r.driverName+"-reg.sock")

	if err := os.MkdirAll(registryDir, 0750); err != nil {
		return fmt.Errorf("creating registry directory: %w", err)
	}
	os.Remove(socketPath) // clean up stale socket

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", socketPath, err)
	}

	server := grpc.NewServer()
	registerapi.RegisterRegistrationServer(server, r)

	go func() {
		<-ctx.Done()
		klog.InfoS("Shutting down registration server")
		server.GracefulStop()
		os.Remove(socketPath)
	}()

	klog.InfoS("Registration server listening",
		"socket", socketPath, "driver", r.driverName)
	return server.Serve(listener)
}
