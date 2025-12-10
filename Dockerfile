# Build stage
FROM golang:1.22-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o deep_trader .

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Set timezone
ENV TZ=Asia/Shanghai

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/deep_trader .

# Copy web assets
COPY --from=builder /app/web ./web

# Copy prompt templates
COPY --from=builder /app/extracted_prompts.md .

# Create directories for data persistence
RUN mkdir -p /app/data /app/exports /app/strategies /app/backtest_data /app/backtest_reports

# Create non-root user
RUN adduser -D -u 1000 trader
RUN chown -R trader:trader /app
USER trader

# Expose web dashboard port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/state || exit 1

# Default command
CMD ["./deep_trader"]
