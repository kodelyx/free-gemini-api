# Build Stage
FROM golang:alpine AS builder

WORKDIR /app

# Install git, ca-certificates and timezone data
RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build pure Go static binary (ModernC SQLite is pure Go)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o goapi main.go

# Production Minimal Stage
FROM alpine:3.20

WORKDIR /app

# Install CA certificates for TLS requests and timezone data
RUN apk add --no-cache ca-certificates tzdata

# Create data directories
RUN mkdir -p data cookies output

# Copy the compiled binary
COPY --from=builder /app/goapi /app/goapi

# Expose API (8001) and WebSocket cookie bridge (9226)
EXPOSE 8001
EXPOSE 9226

ENV TZ=Asia/Kolkata

CMD ["./goapi"]
