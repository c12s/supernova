package main

import (
	"log"
	"net/http"
	"os"

	"github.com/c12s/supernova/internal/builder"
)

// Supernova is the control plane's build service: it turns uploaded build
// contexts into signed OCI images in Singularity, and bootc OS images into
// qcow2 artifacts for provisioning (plan Phase 3.2, decision D6).

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

	mux := http.NewServeMux()
	mux.HandleFunc("POST /build", svc.ImageBuildRequest)
	mux.HandleFunc("POST /build/qcow2", svc.QCOW2BuildRequest)
	mux.HandleFunc("GET /jobs/{id}", svc.JobStatus)
	mux.HandleFunc("GET /jobs", svc.Jobs)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("supernova listening on %s (engine registry %s)", addr, engineRegistry)
	log.Fatal(http.ListenAndServe(addr, mux))
}
