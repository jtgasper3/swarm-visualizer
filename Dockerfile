# Use the official Golang image as the build stage
FROM golang:1.23 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -o swarm-monitor

# Use a minimal base image for the final stage
FROM gcr.io/distroless/static:nonroot

# Set the working directory inside the container
WORKDIR /

USER root

# Copy the binary from the build stage
COPY --from=builder /app/swarm-monitor /swarm-monitor
COPY static/ /static

# Expose port 8080
EXPOSE 8080

# Command to run the application
CMD ["/swarm-monitor"]
