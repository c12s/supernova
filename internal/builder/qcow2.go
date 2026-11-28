package builder

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// QCOW2Spec describes a VM disk image build: a bootc-image-builder run that
// turns an already-built and pushed bootc OS image into a qcow2 artifact for
// provisioning (wormhole libvirt backend / node VM reconciler).
type QCOW2Spec struct {
	Name       string `json:"name"`        // artifact base name, e.g. "edge-base-v1.0"
	ImageRef   string `json:"imageRef"`    // engine-reachable ref of the built bootc image
	TargetArch string `json:"targetArch"`  // default x86_64
	RootFS     string `json:"rootfs"`      // default ext4 (fedora-bootc declares no default)
}

// BuildQCOW2 runs bootc-image-builder (privileged engine container), pulls the
// qcow2 out of the output dir, and uploads it to the Singularity artifact file
// server together with its sha256 checksum.
func (s *Service) BuildQCOW2(jobID string, spec QCOW2Spec) {
	s.store.Update(jobID, func(j *Job) { j.Status = JobRunning })
	log := &strings.Builder{}
	defer s.finish(jobID, log)

	if spec.TargetArch == "" {
		spec.TargetArch = "x86_64"
	}
	if spec.RootFS == "" {
		spec.RootFS = "ext4"
	}
	outDir, err := os.MkdirTemp(workDir(), "supernova-qcow2-"+jobID+"-")
	if err != nil {
		s.fail(jobID, log, err)
		return
	}
	defer os.RemoveAll(outDir)

	// bootc-image-builder needs a type-specific config; default empty config
	// uses image defaults.
	cfgDir, err := os.MkdirTemp(workDir(), "supernova-cfg-")
	if err != nil {
		s.fail(jobID, log, err)
		return
	}
	defer os.RemoveAll(cfgDir)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(""), 0o644); err != nil {
		s.fail(jobID, log, err)
		return
	}

	// bootc-image-builder only reads the source image from the host's
	// containers-storage, which a docker engine never fills, so pull it there
	// first with the podman shipped inside the builder image.
	storage := []string{
		"run", "--rm",
		"--privileged",
		"--security-opt", "label=type:unconfined_t",
		"--network", "host",
		"-v", containersStorage + ":" + containersStorage,
		"--entrypoint", "podman",
		s.qcow2BuilderImage,
	}
	log.WriteString("==> pull " + spec.ImageRef + " into containers-storage\n")
	if err := s.engine.Run(log, append(storage, "pull", "--tls-verify=false", spec.ImageRef)...); err != nil {
		s.fail(jobID, log, fmt.Errorf("pull into containers-storage: %w", err))
		return
	}
	defer func() { _ = s.engine.Run(log, append(storage, "rmi", spec.ImageRef)...) }()

	args := []string{
		"run", "--rm",
		"--privileged",
		"--security-opt", "label=type:unconfined_t",
		"-v", containersStorage + ":" + containersStorage,
		"-v", cfgDir + ":/config:ro",
		"-v", outDir + ":/output",
		s.qcow2BuilderImage,
		spec.ImageRef,
		"--type", "qcow2",
		"--target-arch", spec.TargetArch,
		"--rootfs", spec.RootFS,
	}
	log.WriteString("==> bootc-image-builder (privileged, may take several minutes)\n")
	if err := s.engine.Run(log, args...); err != nil {
		s.fail(jobID, log, fmt.Errorf("bootc-image-builder: %w", err))
		return
	}

	// output lands at /output/qcow2/disk.qcow2
	disk := filepath.Join(outDir, "qcow2", "disk.qcow2")
	f, err := os.Open(disk)
	if err != nil {
		s.fail(jobID, log, fmt.Errorf("open built disk: %w", err))
		return
	}
	defer f.Close()

	artifactName := spec.Name + ".qcow2"
	uploadURL := fmt.Sprintf("%s/artifacts/%s", s.artifactsURL, artifactName)
	log.WriteString("==> upload to singularity files\n")
	req, err := http.NewRequest(http.MethodPut, uploadURL, f)
	if err != nil {
		s.fail(jobID, log, err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.fail(jobID, log, fmt.Errorf("upload: %w", err))
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	log.Write(body)
	if resp.StatusCode != http.StatusCreated {
		s.fail(jobID, log, fmt.Errorf("upload: unexpected status %d", resp.StatusCode))
		return
	}

	var sum string
	for _, line := range strings.Split(string(body), "\n") {
		if i := strings.Index(line, `"checksum":"`); i >= 0 {
			rest := line[i+len(`"checksum":"`):]
			if j := strings.Index(rest, `"`); j >= 0 {
				sum = rest[:j]
			}
		}
	}

	s.store.Update(jobID, func(j *Job) {
		j.Status = JobSucceeded
		j.ArtifactName = artifactName
		j.ArtifactSum = sum
	})
}

const containersStorage = "/var/lib/containers/storage"

// workDir is where build dirs are created. The engine is the host daemon
// (bind-mounted socket), which resolves -v paths on the host, so in compose
// this must be a host dir mounted at the same path inside the container.
func workDir() string {
	return os.Getenv("SUPERNOVA_WORK_DIR")
}
