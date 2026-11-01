# supernova — c12s build service

Turns operator build contexts into **signed OCI images** in Singularity, and
bootc OS images into **qcow2 artifacts** for provisioning (Phase 3.2,
decision **D6**). CPU-heavy and independently scalable: when Supernova is
down, running fleets keep updating from existing images — only *new* builds
are blocked.

## Role in c12s

| Pipeline | Flow |
|---|---|
| OS image build | operator → `POST /build` → engine build + push to Singularity (Zot) → cosign sign → digest returned; the digest is what `cockpit image update --digest` pins |
| Disk provisioning | operator → `POST /build/qcow2` → bootc-image-builder → qcow2 PUT to the artifact server → wormhole / the VM reconciler download it |
| Experiments | `tools/experiments/common.sh` `build_image <tag> <version>` wraps `POST /build` |

It runs in the `tools/docker-compose.yml` stack as the `supernova`
container, with the **host docker socket mounted** (the build engine lives
on the host daemon) and is published only on `127.0.0.1:${SUPERNOVA_HOST_PORT}`
— privileged builds via the host socket must never be reachable from the LAN
(review S3).

## API

| Method | Path | Body | Result |
|---|---|---|---|
| `POST` | `/build` | multipart: `spec` (JSON) + `context` (tar.gz) | `{"jobId": "..."}` |
| `POST` | `/build/qcow2` | JSON `QCOW2Spec` | `{"jobId": "..."}` |
| `GET` | `/jobs/{id}` | — | full `Job` (status, digest, log tail) |
| `GET` | `/jobs` | — | all jobs |
| `GET` | `/healthz` | — | liveness |

Image pipeline per job: `extract context → engine build → engine push →
read digest → cosign sign`. Signing is skipped (with a log note) when
`COSIGN_KEY` is unset — Star's verification degrades accordingly (Phase 3.3).

## Configuration (env)

| Var | Default | Meaning |
|---|---|---|
| `SUPERNOVA_PORT` | `8090` | HTTP listen port |
| `SUPERNOVA_ENGINE_REGISTRY` | `localhost:5001` | registry address **as seen by the engine** (host port mapping) |
| `SUPERNOVA_ARTIFACTS_URL` | `http://singularity-files:8080` | file server for qcow2 uploads |
| `COSIGN_KEY` | *(unset)* | path to cosign private key; unset ⇒ signing disabled |
| `SUPERNOVA_BUILDER_IMAGE` | `quay.io/centos-bootc/bootc-image-builder:latest` | image used for qcow2 builds |
| `SUPERNOVA_ENGINE` | *(unset)* | force engine binary (`docker`/`podman`); default autodetect |
| `SUPERNOVA_WORK_DIR` | system temp | where qcow2 build dirs are created; under compose it must be a host dir mounted at the **same path** (`/var/tmp/supernova`), because the host daemon resolves `-v` paths on the host |

The engine talks to the registry through the **host-mapped** port
(`localhost:5001`) because the daemon lives outside the compose network;
nodes and Star use the in-network name (`singularity:5000`). Supernova
returns the node-visible ref, never the engine address.

## Build and run

```bash
go build ./... && go test ./...   # builds; no tests exist
go run ./cmd/supernova            # needs a container engine (podman/docker) on PATH
```

In the lab it runs from the compose stack (see above). Run directly on a
host with podman/docker: point `SUPERNOVA_WORK_DIR` at a real directory and
`SUPERNOVA_ENGINE_REGISTRY` at the registry address that host can reach.

## Example

```bash
tar -C . -czf ctx.tgz .
curl -s -F 'spec={"name":"os/edge-base","tag":"v1.0","registry":"singularity:5000"}' \
     -F 'context=@ctx.tgz' http://supernova:8090/build
# -> {"jobId":"a1b2c3..."}
curl -s http://supernova:8090/jobs/a1b2c3... | jq '{status,digest,imageRef}'
```

## qcow2 path (Phase 4 dependency)

`POST /build/qcow2` runs `bootc-image-builder` in a **privileged** engine
container (needs KVM-capable host) and PUTs the resulting disk to
Singularity-files with a computed `sha256:` checksum. This path is exercised
by the E2 substrate setup (wormhole libvirt backend imports these disks).

```bash
curl -s -X POST localhost:8090/build/qcow2 -H 'Content-Type: application/json' \
  -d '{"name":"edge-base-v3.10","imageRef":"192.168.122.1:5001/os/edge-base:v3.10"}'
```

Spec fields: `name` (artifact base name), `imageRef` (pullable by the engine;
use the node-visible ref so booted nodes report a resolvable image),
`targetArch` (default `x86_64`), `rootfs` (default `ext4`; fedora-bootc
declares none). bootc-image-builder reads the source image only from the
host's containers-storage, so Supernova first pulls it there with the podman
inside the builder image (`--network host`, `--tls-verify=false`) and removes
it afterwards.

## Status

Stable for the lab build workflows: every `edge-base` tag and every
provisioning disk was produced through this service. Not production-shaped
as deployed (see auth note below).

## Bugs, disabled and unfinished features

### Confirmed bugs

None known that block the current flows. The build paths have been exercised
end to end (image builds for every `edge-base` tag, qcow2 disks for the lab
fleet).

### Disabled / unfinished features

- **No authentication on the API** — acceptable only because the port is
  bound to `127.0.0.1` in the compose deployment (review S3, lab fix);
  exposing it elsewhere would hand out root on the host via the docker
  socket. Auth is listed as open work for real deployments.
- **Job history is in-memory only** (`internal/builder/job.go`): a
  Supernova restart loses all job records; the built images/artifacts
  themselves survive in Singularity.
- **No tests** (`go test ./...` → no test files).
- **Cosign signing is skipped when `COSIGN_KEY` is unset** (the lab
  default) — images are digest-verifiable only; see DEV-7 for the deferred
  key-distribution work.
- **qcow2 builds depend on host path parity** (`SUPERNOVA_WORK_DIR` must
  exist at the same path on host and in the container) and on the host
  docker socket — compose-specific coupling that will break under other
  runtimes without adapting.

### PoC boundaries (by design, not bugs)

- Builds are synchronous at the engine level but asynchronous to the
  caller (`jobId` + polling); there is no queue, retry, or cancellation.
- The engine registry address is the host-mapped one; the ref returned to
  callers is the node-visible one. Mixing them up is the most common
  configuration error (documented above).
