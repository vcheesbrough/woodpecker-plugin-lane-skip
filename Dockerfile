FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG RELEASE_TAG=
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/version.ReleaseTag=${RELEASE_TAG}" \
    -o /plugin .

FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

RUN apk add --no-cache ca-certificates git

COPY --from=builder /plugin /plugin

ENTRYPOINT ["/plugin"]
