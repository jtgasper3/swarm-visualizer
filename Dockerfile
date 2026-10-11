# Use the official Golang image as the build stage. It runs on the machine doing
# the build, whatever the image is for: Go cross-compiles, so building the other
# architecture does not have to happen under emulation.
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:e432b43af23a9328d56a7c499be0476810aa344acbcf65fc7c455d4ff5a40602 AS builder

WORKDIR /app

# Download and cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code and build. The frontend in web/static is
# compiled into the server binary.
COPY . .

ARG TARGETOS
ARG TARGETARCH
# What the server reports at startup; the build workflow sets both.
ARG VERSION=dev
ARG REVISION=unknown

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
    -o swarm-visualizer ./cmd/server
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" \
    -o healthcheck ./cmd/healthcheck

# Use a minimal base image for the final stage
FROM scratch

WORKDIR /

# Copy the binaries from the build stage
COPY --from=builder /app/swarm-visualizer /swarm-visualizer
COPY --from=builder /app/healthcheck /healthcheck

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

# Expose the default port 8080, but it can be overridden
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/healthcheck"]

CMD ["/swarm-visualizer"]
