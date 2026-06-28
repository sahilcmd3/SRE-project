# Stage 1: Build
FROM golang:1.25-alpine AS builder

# Install certificates and tzdata for standard Go dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create a non-root user and group for better security
RUN adduser -D -g '' appuser

WORKDIR /app

# Copy module files and download dependencies (caching layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically linked binary and strip debug info (-s -w) for smaller size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o main .

# Stage 2: Final minimal production image
FROM scratch

# Copy CA certificates and timezone data from the builder stage
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the unprivileged user from the builder stage
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

WORKDIR /app

# Copy the compiled binary
COPY --from=builder /app/main .

# Enforce running as the non-root user
USER appuser:appuser

EXPOSE 8080

ENTRYPOINT ["/app/main"]
