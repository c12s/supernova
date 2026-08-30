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

// Run executes an engine command, streaming combined output into the sink.
func (e *Engine) Run(sink *strings.Builder, args ...string) error {
	cmd := exec.Command(e.bin, args...)
	cmd.Stdout = sink
	cmd.Stderr = sink
	return cmd.Run()
}

func (e *Engine) Name() string { return e.bin }

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
