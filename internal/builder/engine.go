package builder

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Engine abstracts the local container engine used for builds. In the lab
// compose stack the daemon socket is bind-mounted, so `docker` is the
// default; `podman` works identically when Supernova runs on a host that
// has it (both take the same build/push/inspect surface we use).

type Engine struct {
	bin string
}

func NewEngine() (*Engine, error) {
	for _, bin := range []string{os.Getenv("SUPERNOVA_ENGINE"), "podman", "docker"} {
		if bin == "" {
			continue
		}
		if p, err := exec.LookPath(bin); err == nil {
			return &Engine{bin: p}, nil
		}
	}
	return nil, fmt.Errorf("no container engine found (tried SUPERNOVA_ENGINE, podman, docker)")
}

// Run executes an engine command in the current directory, streaming combined
// output into the sink.
func (e *Engine) Run(sink *strings.Builder, args ...string) error {
	return e.RunIn("", sink, args...)
}

// RunIn executes an engine command in dir, streaming combined output into the
// sink. The image build needs this: the context is extracted to a temp dir and
// the build must run with that dir as the working directory so `-f
// Containerfile` and `.` resolve to the extracted files.
func (e *Engine) RunIn(dir string, sink *strings.Builder, args ...string) error {
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = dir
	cmd.Stdout = sink
	cmd.Stderr = sink
	return cmd.Run()
}

func (e *Engine) Name() string { return e.bin }

// Push uploads the built image to the registry. Zot is an OCI-only registry
// (docker schema2 manifests are rejected with 415), so docker-engine builds
// are pushed through skopeo, which converts to OCI on the wire; podman-built
// images are already OCI and push natively.
func (e *Engine) Push(sink *strings.Builder, tag string) error {
	if skopeo, err := exec.LookPath("skopeo"); err == nil {
		transport := "docker-daemon:"
		if strings.HasSuffix(e.bin, "podman") {
			transport = "containers-storage:"
		}
		cmd := exec.Command(skopeo, "copy", "--dest-tls-verify=false",
			transport+tag, "docker://"+tag)
		cmd.Stdout = sink
		cmd.Stderr = sink
		return cmd.Run()
	}
	if strings.HasSuffix(e.bin, "docker") {
		return fmt.Errorf("skopeo is required to push with the docker engine (zot rejects schema2 manifests)")
	}
	return e.Run(sink, "push", tag)
}

// RegistryDigest reads the digest of a pushed image directly from the
// registry (the engine's local RepoDigests may be absent for skopeo pushes).
func (e *Engine) RegistryDigest(sink *strings.Builder, tag string) (string, error) {
	out := &strings.Builder{}
	cmd := exec.Command("skopeo", "inspect", "--tls-verify=false", "--format", "{{.Digest}}", "docker://"+tag)
	cmd.Stdout = out
	cmd.Stderr = sink
	if err := cmd.Run(); err != nil {
		sink.WriteString(out.String())
		// fallback to the engine's own view (works after a native push)
		digest, derr := e.Digest(sink, tag)
		if derr != nil {
			return "", fmt.Errorf("inspect digest: %w", err)
		}
		return digest, nil
	}
	digest := strings.TrimSpace(out.String())
	if digest == "" {
		return "", fmt.Errorf("empty digest reported for %s", tag)
	}
	return digest, nil
}

// Digest returns the repository digest of a pushed image tag.
func (e *Engine) Digest(sink *strings.Builder, tag string) (string, error) {
	out := &strings.Builder{}
	err := e.Run(out, "inspect", "--format", "{{index .RepoDigests 0}}", tag)
	sink.WriteString(out.String())
	if err != nil {
		return "", fmt.Errorf("inspect digest: %w", err)
	}
	line := strings.TrimSpace(out.String())
	if line == "" {
		return "", fmt.Errorf("no RepoDigest for %s", tag)
	}
	// "<registry>/<repo>@sha256:..." -> "sha256:..."
	if _, d, ok := strings.Cut(line, "@"); ok {
		return d, nil
	}
	return line, nil
}
