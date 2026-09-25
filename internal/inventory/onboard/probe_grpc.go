// The gRPC probe: the standard health and reflection services.
package onboard

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionalpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// maxGRPCMessage bounds any one answer the probe receives.
const maxGRPCMessage = 1 << 20

type grpcProber struct {
	// tls is replaced by tests that serve a certificate the system does
	// not trust; nil means the system's roots, with verification on.
	tls *tls.Config
}

func init() { Register(generic.TypeGRPC, grpcProber{}) }

func (grpcProber) Protocol() string { return "grpc" }

// Probe connects to the device's target and asks the standard health
// service (grpc.health.v1) and server reflection (grpc.reflection.v1) what
// it serves. Any gRPC answer proves a gRPC server, Unimplemented included,
// since only a gRPC server can say it; a transport failure, a timeout or a
// refused credential proves nothing. A server reporting NOT_SERVING is
// refused.
//
// A stored credential is sent as a bearer token, and only over TLS: over
// a plaintext connection a stored credential is refused rather than sent.
func (p grpcProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(capability.GRPCCapable)
	if !ok {
		return Probed{}, errors.New("the device names no gRPC target")
	}
	if _, err := generic.ValidateGRPCTarget(dev.GRPCTarget()); err != nil {
		return Probed{}, err
	}
	token := secrets[wire.SecretPassword]
	creds := credentials.NewTLS(p.tlsConfig())
	if dev.GRPCPlaintext() {
		if token != "" {
			return Probed{}, errors.New("a credential is stored for this device and grpc_plaintext is true: refusing to send it unencrypted")
		}
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient("passthrough:///"+dev.GRPCTarget(),
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxGRPCMessage)))
	if err != nil {
		return Probed{}, err
	}
	defer func() { _ = conn.Close() }()
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}

	facts := map[string]any{}
	health, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	switch {
	case err == nil:
		if health.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			return Probed{}, fmt.Errorf("the server reports %s", health.GetStatus())
		}
		facts["health"] = "SERVING"
	case status.Code(err) == codes.Unimplemented:
		facts["health"] = "not served"
	default:
		return Probed{}, fmt.Errorf("health check: %w", err)
	}

	version, services, err := reflectServices(ctx, conn)
	switch {
	case err == nil:
		facts["reflection"] = version
		facts["services"] = factList(services)
	case status.Code(err) == codes.Unimplemented:
		facts["reflection"] = "not served"
	default:
		return Probed{}, fmt.Errorf("server reflection: %w", err)
	}
	return Probed{Capabilities: []capability.Name{capability.NameGRPC}, Facts: facts}, nil
}

// tlsConfig returns the TLS settings: the test override, or the system's
// roots at TLS 1.2 or later.
func (p grpcProber) tlsConfig() *tls.Config {
	if p.tls != nil {
		return p.tls.Clone()
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

// reflectServices asks server reflection for the services the server serves,
// through grpc.reflection.v1 and, when a server implements only the older
// API (as grpc-java did until 1.57), grpc.reflection.v1alpha.
func reflectServices(ctx context.Context, conn *grpc.ClientConn) (string, []string, error) {
	names, err := listServices(ctx, conn)
	if err == nil {
		return "v1", names, nil
	}
	if status.Code(err) != codes.Unimplemented {
		return "", nil, err
	}
	names, err = listServicesAlpha(ctx, conn)
	return "v1alpha", names, err
}

// listServices asks grpc.reflection.v1 for the services served.
func listServices(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		return nil, err
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.GetErrorCode()), e.GetErrorMessage())
	}
	var names []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		names = append(names, s.GetName())
	}
	slices.Sort(names)
	return names, nil
}

// listServicesAlpha is listServices over grpc.reflection.v1alpha, whose
// messages are identical under another package name. grpc-go marks that
// package deprecated; it is used deliberately, and only after v1 answered
// Unimplemented, since a server that predates v1 serves nothing else.
func listServicesAlpha(ctx context.Context, conn *grpc.ClientConn) ([]string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := reflectionalpha.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&reflectionalpha.ServerReflectionRequest{
		MessageRequest: &reflectionalpha.ServerReflectionRequest_ListServices{ListServices: "*"},
	}); err != nil {
		return nil, err
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := resp.GetErrorResponse(); e != nil {
		return nil, status.Error(codes.Code(e.GetErrorCode()), e.GetErrorMessage())
	}
	var names []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		names = append(names, s.GetName())
	}
	slices.Sort(names)
	return names, nil
}
