package main

import (
	"log"
	"net"
	"os"

	"github.com/c12s/supernova/internal/builder"
	api "github.com/c12s/supernova/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Supernova is the control plane's build service: it turns uploaded build
// contexts into signed OCI images in Singularity, and bootc OS images into
// qcow2 artifacts for provisioning.
//
// The service speaks gRPC only. lunar-gateway discovers it through server
// reflection and exposes BuildImage/BuildQCOW2/GetJob/ListJobs as the
// authenticated HTTP routes under /apis/core/v1.

const maxMsgSize = 64 << 20 // the edge-base build context is ~23 MB raw

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := ":" + envOr("SUPERNOVA_PORT", "8090")
	engineRegistry := envOr("SUPERNOVA_ENGINE_REGISTRY", "localhost:5001")
	artifactsURL := envOr("SUPERNOVA_ARTIFACTS_URL", "http://singularity-files:8080")
	cosignKey := os.Getenv("COSIGN_KEY")
	builderImage := envOr("SUPERNOVA_BUILDER_IMAGE", "quay.io/centos-bootc/bootc-image-builder:latest")

	svc, err := builder.NewService(engineRegistry, artifactsURL, cosignKey, builderImage)
	if err != nil {
		log.Fatalf("init builder: %v", err)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
		grpc.UnaryInterceptor(builder.AuthInterceptor),
	)
	api.RegisterSupernovaServer(server, builder.NewGRPCServer(svc))
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)

	log.Printf("supernova gRPC listening on %s (engine registry %s)", addr, engineRegistry)
	log.Fatal(server.Serve(lis))
}
