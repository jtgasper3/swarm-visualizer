# Use the official Golang image as the build stage
FROM golang:1.26@sha256:792443b89f65105abba56b9bd5e97f680a80074ac62fc844a584212f8c8102c3 AS builder

WORKDIR /app

# Download and cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o swarm-monitor ./cmd/server

# Use a minimal base image for the final stage
FROM scratch

WORKDIR /

# Copy the binary from the build stage
COPY --from=builder /app/swarm-monitor /swarm-monitor
COPY /web/static/ /static

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

# Expose port the default port 8080m but can be overridden
EXPOSE 8080

CMD ["/swarm-monitor"]
