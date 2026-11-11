package builder

import (
	"context"
	"os"

	"github.com/c12s/supernova/internal/auth"
	api "github.com/c12s/supernova/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCServer implements supernova.Supernova. The gateway reaches it through
// gRPC reflection (the same mechanism as every other c12s backend), so there
// is no HTTP server and no special passthrough in the gateway.
type GRPCServer struct {
	api.UnimplementedSupernovaServer
	svc *Service
}

func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

// AuthInterceptor lifts the "authz-token" metadata into the context so the
// handlers can authorize the caller, matching magnetar/gravity/kuiper.
func AuthInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if ok && len(md.Get("authz-token")) > 0 {
		ctx = context.WithValue(ctx, "authz-token", md.Get("authz-token")[0])
	}
	return handler(ctx, req)
}

// BuildImage accepts the spec plus a tar.gz context in one unary message and
// returns immediately with a job id. The context is copied to a temp file
// before the handler returns: the build runs after the RPC, and the request
// message must not be kept alive for the duration of a multi-minute build.
func (g *GRPCServer) BuildImage(ctx context.Context, req *api.BuildImageReq) (*api.BuildJob, error) {
	spec := req.GetSpec()
	if spec == nil || spec.GetName() == "" || spec.GetRegistry() == "" {
		return nil, status.Error(codes.InvalidArgument, "spec requires name and registry")
	}
	if len(req.GetContext()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "missing context")
	}
	if !auth.Authorize(ctx, "image.build", "image", spec.GetName()) {
		return nil, status.Errorf(codes.PermissionDenied, "image.build not permitted for %s", spec.GetName())
	}

	f, err := os.CreateTemp("", "supernova-ctx-*.tar.gz")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create context file: %v", err)
	}
	if _, err := f.Write(req.GetContext()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, status.Errorf(codes.Internal, "write context file: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, status.Errorf(codes.Internal, "rewind context file: %v", err)
	}

	internal := ImageSpec{
		Name:          spec.GetName(),
		Tag:           spec.GetTag(),
		Containerfile: spec.GetContainerfile(),
		Registry:      spec.GetRegistry(),
		BuildArgs:     spec.GetBuildArgs(),
	}

	jobID := newID()
	g.svc.store.Create(jobID, JobImage)
	go func() {
		defer f.Close()
		defer os.Remove(f.Name())
		g.svc.BuildImage(jobID, internal, f)
	}()

	return &api.BuildJob{JobId: jobID}, nil
}

// BuildQCOW2 turns an already-pushed bootc image into a qcow2 artifact.
func (g *GRPCServer) BuildQCOW2(ctx context.Context, req *api.QCOW2Req) (*api.BuildJob, error) {
	if req.GetName() == "" || req.GetImageRef() == "" {
		return nil, status.Error(codes.InvalidArgument, "spec requires name and imageRef")
	}
	if !auth.Authorize(ctx, "image.build", "image", req.GetName()) {
		return nil, status.Errorf(codes.PermissionDenied, "image.build not permitted for %s", req.GetName())
	}

	internal := QCOW2Spec{
		Name:       req.GetName(),
		ImageRef:   req.GetImageRef(),
		TargetArch: req.GetTargetArch(),
		RootFS:     req.GetRootfs(),
	}

	jobID := newID()
	g.svc.store.Create(jobID, JobQCOW2)
	go g.svc.BuildQCOW2(jobID, internal)

	return &api.BuildJob{JobId: jobID}, nil
}

func (g *GRPCServer) GetJob(ctx context.Context, req *api.GetJobReq) (*api.Job, error) {
	job, err := g.svc.store.Snapshot(req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return toProtoJob(job), nil
}

func (g *GRPCServer) ListJobs(ctx context.Context, req *api.ListJobsReq) (*api.JobList, error) {
	jobs := g.svc.store.List()
	out := make([]*api.Job, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, toProtoJob(job))
	}
	return &api.JobList{Jobs: out}, nil
}

// ListImages reads the registry and the artifact server. It is read-only and
// intentionally does not require a permission: the operator's local view of
// what the control plane already has should not be gated behind a policy.
func (g *GRPCServer) ListImages(ctx context.Context, req *api.ListImagesReq) (*api.ImageList, error) {
	images, artifacts, err := g.svc.ListImages()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list images: %v", err)
	}
	out := &api.ImageList{Images: images}
	for _, a := range artifacts {
		out.Artifacts = append(out.Artifacts, &api.Artifact{Name: a.Name, Checksum: a.Checksum})
	}
	return out, nil
}

func toProtoJob(j *Job) *api.Job {
	return &api.Job{
		Id:           j.ID,
		Type:         string(j.Type),
		Status:       string(j.Status),
		ImageRef:     j.ImageRef,
		Digest:       j.Digest,
		ArtifactName: j.ArtifactName,
		ArtifactSum:  j.ArtifactSum,
		Error:        j.Error,
		Log:          j.Log,
	}
}
