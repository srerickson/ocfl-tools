FROM --platform=$BUILDPLATFORM golang:latest AS builder

WORKDIR /app
COPY . .
RUN go mod download

ARG TARGETARCH
RUN GOOS=linux GOARCH=$TARGETARCH go build -o ./ocfl ./cmd/ocfl

FROM ubuntu:latest
COPY --from=builder /app/ocfl /usr/local/bin/ocfl