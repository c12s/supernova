# supernova — c12s build service

Turns operator build contexts into **signed OCI images** in Singularity, and
bootc OS images into **qcow2 artifacts** for provisioning (Phase 3.2,
decision **D6**). CPU-heavy and independently scalable: when Supernova is
down, running fleets keep updating from existing images — only *new* builds
are blocked.

## Role in c12s

| Pipeline | Flow |
|---|---|
| OS image build | operator → `cockpit image build` → gateway `BuildImage` → engine build + push to Singularity (Zot) → cosign sign → digest returned; the digest is what `cockpit image update --digest` pins |
| Disk provisioning | operator → `cockpit image build qcow2` → gateway `BuildQCOW2` → bootc-image-builder → qcow2 PUT to the artifact server → wormhole / the VM reconciler download it |
| Experiments | `tools/experiments/common.sh` `build_image <tag> <version>` still builds directly with `docker`/`podman` for speed; the operator path is the cockpit command |

It runs in the `tools/docker-compose.yml` stack as the `supernova`
container, with the **host docker socket mounted** (the build engine lives
on the host daemon) and is published only on `127.0.0.1:${SUPERNOVA_HOST_PORT}`
— privileged builds via the host socket must never be reachable from the LAN
(review S3). The gateway reaches it in-network as `supernova:8090`.

## API

Supernova speaks **gRPC only**. The gateway discovers it through server
reflection (the same mechanism as every other c12s backend) and exposes the
RPCs as authenticated HTTP+JSON routes:

| RPC | Gateway route | Body | Result |
|---|---|---|---|
| `BuildImage` | `POST /apis/core/v1/builds` | `{"spec":{...},"context":"<base64 tar.gz>"}` | `{"jobId":"..."}` |
| `BuildQCOW2` | `POST /apis/core/v1/builds/qcow2` | JSON `QCOW2Spec` | `{"jobId":"..."}` |
| `GetJob` | `GET /apis/core/v1/builds/{id}` | — | full `Job` (status, digest, log tail) |
| `ListJobs` | `GET /apis/core/v1/builds` | — | all jobs |

`BuildImage` carries the build context as a tar.gz in one unary message. The
gateway's grpcurl JSON parser accepts it base64-encoded. The server and the
gateway's client both raise the gRPC message limit to 64 MiB (the edge-base
context is ~23 MB raw, so ~10 MB compressed).

Image pipeline per job: `extract context → engine build → engine push →
read digest → cosign sign`. Signing is skipped (with a log note) when
`COSIGN_KEY` is unset — Star's verification degrades accordingly (Phase 3.3).

**Authorization.** The gateway verifies the vault token with Apollo and
forwards the signed JWT as the `authz-token` gRPC metadata. Supernova parses
the JWT with the shared `SECRET_KEY` and requires
`image.build|image|<image name>` in the `permissions` claim. The object is the
repo path (`os/edge-base`), so an operator policy grants the build permission
on the image, not on a node:

```yaml
objectScope:   { id: "os/edge-base", kind: "image" }
permission:    { condition: { expression: "" }, kind: "ALLOW", name: "image.build" }
subjectScope:  { id: "<username>", kind: "user" }
```

Re-login after creating the policy so the new permission is baked into the
token. The loopback port itself remains unauthenticated for local debugging
(review S3); the gateway route is the authenticated path.

## Configuration (env)

| Var | Default | Meaning |
|---|---|---|
| `SUPERNOVA_PORT` | `8090` | gRPC listen port |
| `SUPERNOVA_ENGINE_REGISTRY` | `localhost:5001` | registry address the **push** (skopeo, runs in the Supernova container) reaches; under compose it is `singularity:5000` |
| `SUPERNOVA_ARTIFACTS_URL` | `http://singularity-files:8080` | file server for qcow2 uploads |
| `COSIGN_KEY` | *(unset)* | path to cosign private key; unset ⇒ signing disabled |
| `SUPERNOVA_BUILDER_IMAGE` | `quay.io/centos-bootc/bootc-image-builder:latest` | image used for qcow2 builds |
| `SUPERNOVA_ENGINE` | *(unset)* | force engine binary (`docker`/`podman`); default autodetect |
| `SUPERNOVA_WORK_DIR` | system temp | where qcow2 build dirs are created; under compose it must be a host dir mounted at the **same path** (`/var/tmp/supernova`), because the host daemon resolves `-v` paths on the host |
| `SECRET_KEY` | *(required)* | shared JWT key; without it every build is rejected (no authorizer) |

The engine runs on the host (the compose stack mounts the host docker socket),
so `docker build` tags the image with the address the **push** reaches from
inside the Supernova container: `singularity:5000` in the compose network, or
`localhost:5001` when running directly on the host. Nodes and Star pull the
node-visible ref (`192.168.122.1:5001/...`); Supernova returns that, never the
engine address. The build does not contact the registry (the daemon only
tags); the push and the digest read do.

## Build and run

```bash
go build ./... && go test ./...   # builds; auth and gRPC server tests exist
go run ./cmd/supernova            # needs a container engine (podman/docker) on PATH
```

In the lab it runs from the compose stack (see above). Run directly on a
host with podman/docker: point `SUPERNOVA_WORK_DIR` at a real directory and
`SUPERNOVA_ENGINE_REGISTRY` at the registry address that host can reach.

## Examples

Through the operator CLI (recommended):

```bash
cockpit image build --name os/edge-base --tag v3.18 \
  --context tools/bootc --containerfile Containerfile.bootc \
  --build-arg OS_VERSION=v3.18 --wait
# prints the digest; next: cockpit image update --node-id <id> --image-ref ... --digest ...
```

Directly against the gRPC port with grpcurl (local debugging only):

```bash
tar -C tools/bootc -czf ctx.tgz .
grpcurl -plaintext -H 'authz-token: <jwt>' \
  -d "{\"spec\":{\"name\":\"os/edge-base\",\"tag\":\"v3.18\",\"registry\":\"192.168.122.1:5001\",\"buildArgs\":{\"OS_VERSION\":\"v3.18\"}},\"context\":\"$(base64 -w0 ctx.tgz)\"}" \
  localhost:8090 supernova.Supernova/BuildImage
# -> {"jobId":"a1b2c3..."}
grpcurl -plaintext -H 'authz-token: <jwt>' -d '{"id":"a1b2c3..."}' \
  localhost:8090 supernova.Supernova/GetJob
```

`BuildImage` spec fields: `name` (repo path), `tag` (default `latest`),
`containerfile` (default `Containerfile`), `registry` (node-visible ref),
`buildArgs` (extra `--build-arg` values; the edge-base image needs
`OS_VERSION`, and `BASE` for a broken/alt build on top of another image).

## qcow2 path (Phase 4 dependency)

`BuildQCOW2` runs `bootc-image-builder` in a **privileged** engine container
(needs a KVM-capable host) and PUTs the resulting disk to Singularity-files
with a computed `sha256:` checksum. This path is exercised by the E2
substrate setup (wormhole libvirt backend imports these disks).

```bash
cockpit image build qcow2 --name edge-base-v3.18 \
  --image-ref 192.168.122.1:5001/os/edge-base:v3.18 --wait
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

- **The loopback port is still unauthenticated.** The gateway route is the
  authenticated path; `127.0.0.1:8090` remains open for local debugging
  (review S3). Exposing the port elsewhere would hand out root on the host
  via the docker socket.
- **Job history is in-memory only** (`internal/builder/job.go`): a
  Supernova restart loses all job records; the built images/artifacts
  themselves survive in Singularity. `GetJob`/`ListJobs` only see jobs from
  the current process.
- **Cosign signing is skipped when `COSIGN_KEY` is unset** (the lab
  default) — images are digest-verifiable only; see DEV-7 for the deferred
  key-distribution work.
- **qcow2 builds depend on host path parity** (`SUPERNOVA_WORK_DIR` must
  exist at the same path on host and in the container) and on the host
  docker socket — compose-specific coupling that will break under other
  runtimes without adapting.
- **The experiment scripts still build directly** with `docker`/`podman`
  (`tools/experiments/common.sh build_image`); only the cockpit command goes
  through the gateway. Switching the scripts is a follow-up.

### PoC boundaries (by design, not bugs)

- Builds are synchronous at the engine level but asynchronous to the
  caller (`jobId` + polling); there is no queue, retry, or cancellation.
- The engine registry address is the host-mapped one; the ref returned to
  callers is the node-visible one. Mixing them up is the most common
  configuration error (documented above).
- The build context travels as one gRPC message. That is fine for the
  edge-base context; a multi-hundred-MB context would need a streaming RPC.
