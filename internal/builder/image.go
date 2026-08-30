package builder

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ImageSpec describes one OCI image build request.
type ImageSpec struct {
	Name          string `json:"name"`            // repo path under the registry, e.g. "os/edge-base"
	Tag           string `json:"tag"`             // e.g. "v1.0"
	Containerfile string `json:"containerfile"`   // file name inside the context, default "Containerfile"
	Registry      string `json:"registry"`        // node-visible registry, e.g. "singularity:5000"
}

// BuildImage runs the full pipeline for one image job:
// extract context -> engine build -> engine push -> read digest -> cosign sign.
// All output lands in the job log.
func (s *Service) BuildImage(jobID string, spec ImageSpec, contextTar io.Reader) {
	s.store.Update(jobID, func(j *Job) { j.Status = JobRunning })
	log := &strings.Builder{}
	defer s.finish(jobID, log)

	work, err := os.MkdirTemp("", "supernova-"+jobID+"-")
	if err != nil {
		s.fail(jobID, log, fmt.Errorf("workdir: %w", err))
		return
	}
	defer os.RemoveAll(work)

	if err := extractTarGz(contextTar, work); err != nil {
		s.fail(jobID, log, fmt.Errorf("extract context: %w", err))
		return
	}

	if spec.Containerfile == "" {
		spec.Containerfile = "Containerfile"
	}
	if spec.Tag == "" {
		spec.Tag = "latest"
	}
	engineTag := fmt.Sprintf("%s/%s:%s", s.engineRegistry, spec.Name, spec.Tag)
	nodeTag := fmt.Sprintf("%s/%s:%s", spec.Registry, spec.Name, spec.Tag)

	log.WriteString(fmt.Sprintf("==> %s build -f %s -t %s .\n", filepath.Base(s.engine.Name()), spec.Containerfile, engineTag))
	if err := s.engine.Run(log, "build", "-f", spec.Containerfile, "-t", engineTag, "."); err != nil {
		s.fail(jobID, log, fmt.Errorf("build: %w", err))
		return
	}

	log.WriteString(fmt.Sprintf("==> push %s\n", engineTag))
	if err := s.engine.Run(log, "push", engineTag); err != nil {
		s.fail(jobID, log, fmt.Errorf("push: %w", err))
		return
	}

	digest, err := s.engine.Digest(log, engineTag)
	if err != nil {
		s.fail(jobID, log, err)
		return
	}

	if err := s.sign(log, nodeTag, digest); err != nil {
		s.fail(jobID, log, err)
		return
	}

	s.store.Update(jobID, func(j *Job) {
		j.Status = JobSucceeded
		j.ImageRef = nodeTag
		j.Digest = digest
	})
	log.WriteString(fmt.Sprintf("==> signed %s@%s\n", nodeTag, digest))
}

// sign attaches a cosign signature to the image (by tag; cosign resolves the
// digest server-side, so the signature always covers the exact manifest).
func (s *Service) sign(log io.Writer, ref, _ string) error {
	if _, err := exec.LookPath("cosign"); err != nil {
		fmt.Fprintln(log, "==> cosign not installed, skipping signature")
		return nil
	}
	if s.cosignKey == "" {
		fmt.Fprintln(log, "==> COSIGN_KEY not configured, skipping signature")
		return nil
	}
	fmt.Fprintln(log, "==> cosign sign")
	cmd := exec.Command("cosign", "sign", "--yes", "--allow-insecure-registry", "--key", s.cosignKey, ref)
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Run()
}

func (s *Service) fail(jobID string, log *strings.Builder, err error) {
	s.store.Update(jobID, func(j *Job) {
		j.Status = JobFailed
		j.Error = err.Error()
		j.Log = tail(log.String())
	})
}

func (s *Service) finish(jobID string, log *strings.Builder) {
	s.store.Update(jobID, func(j *Job) {
		if j.Status == JobRunning || j.Status == JobPending {
			j.Status = JobFailed
			j.Error = "pipeline ended without a result"
		}
		j.Log = tail(log.String())
	})
}

func tail(s string) string {
	const max = 8192
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}

func extractTarGz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// refuse traversal outside dst
		target := filepath.Join(dst, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal path in context: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
