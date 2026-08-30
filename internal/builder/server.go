package builder

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

// Service wires the store, engine and configuration together.
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

// ImageBuildRequest handles multipart POST /build:
//
//	spec    (part)  JSON ImageSpec
//	context (part)  tar.gz of the build context (Containerfile + payloads)
func (s *Service) ImageBuildRequest(w http.ResponseWriter, r *http.Request) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		http.Error(w, "expected multipart/form-data", http.StatusBadRequest)
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])

	var spec ImageSpec
	var contextPart io.Reader
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch p.FormName() {
		case "spec":
			if err := json.NewDecoder(p).Decode(&spec); err != nil {
				http.Error(w, "spec: "+err.Error(), http.StatusBadRequest)
				return
			}
		case "context":
			contextPart = p
		}
	}
	if contextPart == nil {
		http.Error(w, "missing context part", http.StatusBadRequest)
		return
	}
	if spec.Name == "" || spec.Registry == "" {
		http.Error(w, "spec requires name and registry", http.StatusBadRequest)
		return
	}

	jobID := newID()
	s.store.Create(jobID, JobImage)
	go s.BuildImage(jobID, spec, contextPart)

	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// QCOW2BuildRequest handles POST /build/qcow2 with a JSON QCOW2Spec body.
func (s *Service) QCOW2BuildRequest(w http.ResponseWriter, r *http.Request) {
	var spec QCOW2Spec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if spec.Name == "" || spec.ImageRef == "" {
		http.Error(w, "spec requires name and imageRef", http.StatusBadRequest)
		return
	}

	jobID := newID()
	s.store.Create(jobID, JobQCOW2)
	go s.BuildQCOW2(jobID, spec)

	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

// JobStatus handles GET /jobs/{id}.
func (s *Service) JobStatus(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// Jobs handles GET /jobs.
func (s *Service) Jobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.jobs)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintln(w)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}
