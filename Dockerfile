FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /supernova ./cmd/supernova

FROM alpine:3.20
# docker CLI talks to the bind-mounted daemon socket; cosign signs images.
# cosign lives in the community repo, hence the edge repository pin.
RUN apk add --no-cache docker-cli && \
    apk add --no-cache -X http://dl-cdn.alpinelinux.org/alpine/edge/community cosign
COPY --from=build /supernova /usr/local/bin/supernova
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/supernova"]
