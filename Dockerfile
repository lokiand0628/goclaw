FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache make git

# Copy only go.mod and go.sum first to cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the binary
RUN make build

# --- Runtime Stage ---
FROM alpine:latest

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/goclaw .

# Install runtime dependencies (certificates, timezone, bash, and GIT for backup)
RUN apk add --no-cache ca-certificates tzdata bash git && \
    git config --system --add safe.directory '*'

# Copy default assets if needed (though init usually handles this, binary has embeds)
# COPY --from=builder /app/.env.example .

# Create workspace directory
RUN mkdir -p workspace

# Expose ports if needed (e.g. for webhooks or admin UI)
# EXPOSE 8080

# Command to run
CMD ["./goclaw", "start"]
