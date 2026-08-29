// fake-gpu-device-plugin advertises nvidia.com/gpu on nodes that have no GPU.
//
// There is no NVIDIA hardware on an Apple Silicon laptop, but almost everything
// interesting about GPUs in Kubernetes is *scheduling* behaviour: extended
// resources are integers, they cannot be overcommitted, requests must equal
// limits, and a pod asking for more than any node advertises stays Pending
// forever. All of that is decided by the scheduler and the kubelet, not by the
// silicon — so a plugin that hands out imaginary devices reproduces it exactly.
//
// This is a real device plugin, not a node-status patch: it registers over the
// kubelet's gRPC socket and implements the full DevicePlugin service, which is
// what makes it worth taking apart in the "Extending Kubernetes" module.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	resourceName = "nvidia.com/gpu"
	socketName   = "fake-gpu.sock"
)

type plugin struct {
	devices []*pluginapi.Device
	socket  string
}

func main() {
	log.SetFlags(log.Ltime)

	count := 2
	if v, err := strconv.Atoi(os.Getenv("FAKE_GPU_COUNT")); err == nil && v > 0 {
		count = v
	}
	node := os.Getenv("NODE_NAME")

	p := &plugin{socket: filepath.Join(pluginapi.DevicePluginPath, socketName)}
	for i := 0; i < count; i++ {
		p.devices = append(p.devices, &pluginapi.Device{
			// A stable, node-scoped ID. Real plugins use the GPU UUID.
			ID:     fmt.Sprintf("FAKE-GPU-%s-%d", node, i),
			Health: pluginapi.Healthy,
		})
	}

	srv, err := p.serve()
	if err != nil {
		log.Fatalf("serve: %v", err)
	}
	defer srv.Stop()

	if err := p.register(); err != nil {
		log.Fatalf("register with kubelet: %v", err)
	}
	log.Printf("registered %d fake GPUs as %s on node %s", count, resourceName, node)

	// If the kubelet restarts it recreates its socket and forgets every
	// registered plugin. Exiting lets the DaemonSet restart us, which is the
	// simplest correct way to re-register.
	p.watchKubelet()
}

// serve starts the plugin's own gRPC socket, which the kubelet dials back on.
func (p *plugin) serve() (*grpc.Server, error) {
	if err := os.Remove(p.socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	lis, err := net.Listen("unix", p.socket)
	if err != nil {
		return nil, err
	}
	srv := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(srv, p)
	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Printf("grpc server stopped: %v", err)
		}
	}()

	// Make sure the socket is actually accepting before telling the kubelet
	// about it, otherwise registration races and fails.
	conn, err := dial(p.socket)
	if err != nil {
		return nil, fmt.Errorf("plugin socket not ready: %w", err)
	}
	conn.Close()
	return srv, nil
}

// register tells the kubelet which resource this plugin owns and where to
// reach it.
func (p *plugin) register() error {
	// KubeletSocket is already an absolute path — do not join it with
	// DevicePluginPath, which is where its own name comes from.
	conn, err := dial(pluginapi.KubeletSocket)
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = pluginapi.NewRegistrationClient(conn).Register(ctx, &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     socketName,
		ResourceName: resourceName,
	})
	return err
}

func (p *plugin) watchKubelet() {
	before, err := os.Stat(pluginapi.KubeletSocket)
	if err != nil {
		log.Fatalf("kubelet socket vanished: %v", err)
	}
	for {
		time.Sleep(10 * time.Second)
		now, err := os.Stat(pluginapi.KubeletSocket)
		if err != nil || !now.ModTime().Equal(before.ModTime()) {
			log.Printf("kubelet socket changed — exiting so the DaemonSet re-registers us")
			return
		}
	}
}

func dial(path string) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return grpc.DialContext(ctx, "unix://"+path,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
}

// ------------------------------------------------ DevicePlugin service ----

func (p *plugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// ListAndWatch streams the device inventory. The kubelet keeps the stream open
// forever and turns the latest message into the node's nvidia.com/gpu capacity,
// which is why marking a device Unhealthy here immediately shrinks capacity.
func (p *plugin) ListAndWatch(_ *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	if err := stream.Send(&pluginapi.ListAndWatchResponse{Devices: p.devices}); err != nil {
		return err
	}
	// Real plugins push a new message on health changes. Nothing changes here,
	// so just hold the stream open until the kubelet closes it.
	<-stream.Context().Done()
	return nil
}

// Allocate runs when a container that requested the resource is about to
// start. A real plugin injects device nodes and NVIDIA_VISIBLE_DEVICES here;
// the fake one only records which devices it handed out.
func (p *plugin) Allocate(_ context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	resp := &pluginapi.AllocateResponse{}
	for _, cr := range req.ContainerRequests {
		log.Printf("allocating %v", cr.DevicesIDs)
		resp.ContainerResponses = append(resp.ContainerResponses, &pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{
				"NVIDIA_VISIBLE_DEVICES": join(cr.DevicesIDs),
				"FAKE_GPU":               "1",
			},
		})
	}
	return resp, nil
}

func (p *plugin) GetPreferredAllocation(context.Context, *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return &pluginapi.PreferredAllocationResponse{}, nil
}

func (p *plugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

func join(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}
