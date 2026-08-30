FROM golang:1.23-alpine AS build
WORKDIR /src
COPY supernova/go.mod ./
COPY supernova/cmd ./cmd
COPY supernova/internal ./internal
RUN CGO_ENABLED=0 go build -o /supernova ./cmd/supernova

FROM alpine:3.20
# docker CLI talks to the bind-mounted daemon socket; skopeo converts docker
# schema2 images to OCI for the Zot registry; cosign signs images.
RUN apk add --no-cache docker-cli skopeo && \
    apk add --no-cache -X http://dl-cdn.alpinelinux.org/alpine/edge/community cosign
COPY --from=build /supernova /usr/local/bin/supernova
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/supernova"]
