# supernova — c12s build service

Turns operator build contexts into **signed OCI images** in Singularity, and
bootc OS images into **qcow2 artifacts** for provisioning (Phase 3.2,
decision **D6**). CPU-heavy and independently scalable: when Supernova is
down, running fleets keep updating from existing images — only *new* builds
are blocked.

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

The engine talks to the registry through the **host-mapped** port
(`localhost:5001`) because the daemon lives outside the compose network;
nodes and Star use the in-network name (`singularity:5000`). Supernova
returns the node-visible ref, never the engine address.

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
