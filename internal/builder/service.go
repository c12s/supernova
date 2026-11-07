package builder

import (
	"log"
	"strings"
)

// Service wires the store, engine and configuration together. The gRPC
// server (grpc.go) is the only transport: Supernova is reached through the
// lunar-gateway, which forwards the gRPC call as an HTTP+JSON route.
type Service struct {
	store             *Store
	engine            *Engine
	engineRegistry    string // registry address as seen by the container engine (host mapping)
	artifactsURL      string // singularity files server, reachable from this container
	cosignKey         string // path to cosign private key; empty disables signing
	qcow2BuilderImage string
}

func NewService(engineRegistry, artifactsURL, cosignKey, qcow2BuilderImage string) (*Service, error) {
	engine, err := NewEngine()
	if err != nil {
		return nil, err
	}
	log.Printf("supernova using engine %q", engine.Name())
	return &Service{
		store:             NewStore(),
		engine:            engine,
		engineRegistry:    engineRegistry,
		artifactsURL:      strings.TrimSuffix(artifactsURL, "/"),
		cosignKey:         cosignKey,
		qcow2BuilderImage: qcow2BuilderImage,
	}, nil
}
