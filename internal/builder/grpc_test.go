package builder

import (
	"context"
	"testing"

	api "github.com/c12s/supernova/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestServer() *GRPCServer {
	return NewGRPCServer(&Service{store: NewStore()})
}

func TestBuildImageRejectsMissingSpec(t *testing.T) {
	g := newTestServer()
	_, err := g.BuildImage(context.Background(), &api.BuildImageReq{Context: []byte("x")})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestBuildImageRejectsMissingContext(t *testing.T) {
	g := newTestServer()
	_, err := g.BuildImage(context.Background(), &api.BuildImageReq{
		Spec: &api.ImageSpec{Name: "os/edge-base", Registry: "192.168.122.1:5001"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestBuildImageRejectsUnauthorized(t *testing.T) {
	g := newTestServer()
	_, err := g.BuildImage(context.Background(), &api.BuildImageReq{
		Spec:    &api.ImageSpec{Name: "os/edge-base", Registry: "192.168.122.1:5001"},
		Context: []byte("x"),
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("want PermissionDenied, got %v", err)
	}
}

func TestGetJobNotFound(t *testing.T) {
	g := newTestServer()
	_, err := g.GetJob(context.Background(), &api.GetJobReq{Id: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestGetAndListJobs(t *testing.T) {
	g := newTestServer()
	g.svc.store.Create("j1", JobImage)
	g.svc.store.Update("j1", func(j *Job) {
		j.Status = JobSucceeded
		j.ImageRef = "192.168.122.1:5001/os/edge-base:v3.18"
		j.Digest = "sha256:abc"
	})

	job, err := g.GetJob(context.Background(), &api.GetJobReq{Id: "j1"})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.GetDigest() != "sha256:abc" || job.GetStatus() != "succeeded" {
		t.Fatalf("unexpected job: %+v", job)
	}

	list, err := g.ListJobs(context.Background(), &api.ListJobsReq{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list.GetJobs()) != 1 || list.GetJobs()[0].GetId() != "j1" {
		t.Fatalf("unexpected list: %+v", list)
	}
}
