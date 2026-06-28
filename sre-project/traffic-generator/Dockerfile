# Stage 1: Build
FROM golang:1.25-alpine AS builder
RUN apk --no-cache add ca-certificates tzdata
RUN adduser -D -g '' appuser
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o main .

# Stage 2: Minimal production image
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
WORKDIR /app
COPY --from=builder /app/main .
USER appuser:appuser
ENTRYPOINT ["/app/main"]
