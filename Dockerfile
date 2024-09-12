FROM --platform=$BUILDPLATFORM docker.io/golang:latest AS builder

WORKDIR /app
COPY . .
RUN go mod download

ARG TARGETARCH
ARG OCFLTOOLS_VERSION
ARG OCFLTOOLS_BUILDTIME
RUN GOOS=linux GOARCH=$TARGETARCH go build -ldflags "-X github.com/srerickson/ocfl-tools/cmd/ocfl/run.Version=${OCFLTOOLS_VERSION} -X github.com/srerickson/ocfl-tools/cmd/ocfl/run.BuildTime=${OCFLTOOLS_BUILDTIME}" -o ./ocfl ./cmd/ocfl

FROM ubuntu:latest
COPY --from=builder /app/ocfl /usr/local/bin/ocfl