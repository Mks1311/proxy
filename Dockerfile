# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git for fetching dependencies
RUN apk add --no-cache git

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=0 creates a static binary making it smaller and more secure
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/poolify ./cmd/poolify

# Final stage
FROM alpine:latest

WORKDIR /app

# Add CA certificates for HTTPS requests (needed to talk to Groq/OpenAI)
RUN apk --no-cache add ca-certificates tzdata

# Copy the binary from builder
COPY --from=builder /app/poolify /app/poolify

# Expose the port
EXPOSE 8080

# Run the binary
CMD ["/app/poolify"]
