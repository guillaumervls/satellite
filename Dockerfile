# Stage 1: Build the Go binary
FROM golang:alpine AS builder

WORKDIR /app

COPY . .

RUN go build -o satellite

# Stage 2: Create a minimal runtime image
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/satellite .

CMD ["./satellite"]

EXPOSE 8080