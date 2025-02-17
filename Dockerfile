# Use the official Golang image as the build stage
FROM golang:1.23 AS builder

WORKDIR /app

# Download and cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o swarm-monitor

# Use a minimal base image for the final stage
FROM scratch

WORKDIR /

# Copy the binary from the build stage
COPY --from=builder /app/swarm-monitor /swarm-monitor
COPY static/ /static

# Expose port the default port 8080m but can be overridden
EXPOSE 8080

CMD ["/swarm-monitor"]
