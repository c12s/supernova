package builder

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListImagesReturnsDigests(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			w.Write([]byte(`{"repositories":["os/edge-base"]}`))
		case r.URL.Path == "/v2/os/edge-base/tags/list":
			w.Write([]byte(`{"name":"os/edge-base","tags":["v4.1","v4.0"]}`))
		case r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, "/v2/os/edge-base/manifests/"):
			if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.manifest.v1+json") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			tag := strings.TrimPrefix(r.URL.Path, "/v2/os/edge-base/manifests/")
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat(tag[len(tag)-1:], 64))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer registry.Close()
	artifacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artifacts":
			w.Write([]byte(`["edge-base-v4.0.qcow2"]`))
		case "/artifacts/edge-base-v4.0.qcow2/sha256":
			w.Write([]byte("sha256:" + strings.Repeat("a", 64) + "\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer artifacts.Close()

	s := &Service{engineRegistry: strings.TrimPrefix(registry.URL, "http://"), artifactsURL: artifacts.URL}
	images, arts, err := s.ListImages()
	if err != nil {
		t.Fatalf("list images: %v", err)
	}
	want := []Image{
		{Ref: "os/edge-base:v4.0", Digest: "sha256:" + strings.Repeat("0", 64)},
		{Ref: "os/edge-base:v4.1", Digest: "sha256:" + strings.Repeat("1", 64)},
	}
	if len(images) != len(want) {
		t.Fatalf("want %d images, got %+v", len(want), images)
	}
	for i := range want {
		if images[i] != want[i] {
			t.Errorf("image %d: want %+v, got %+v", i, want[i], images[i])
		}
	}
	if len(arts) != 1 || arts[0].Checksum != "sha256:"+strings.Repeat("a", 64) {
		t.Errorf("unexpected artifacts %+v", arts)
	}
}
