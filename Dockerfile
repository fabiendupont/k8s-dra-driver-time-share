FROM registry.access.redhat.com/ubi10/go-toolset:1.26 AS builder

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o dra-time-share ./cmd/driver

FROM registry.access.redhat.com/ubi10/ubi-micro:latest
COPY --from=builder /opt/app-root/src/dra-time-share /usr/bin/dra-time-share
ENTRYPOINT ["/usr/bin/dra-time-share"]
