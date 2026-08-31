FROM registry.access.redhat.com/ubi10/go-toolset:1.26 AS builder

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o dra-time-share ./cmd/driver

USER 0
RUN gcc -static -nostdlib -o sched-helper cmd/sched-helper/main.c

FROM registry.access.redhat.com/ubi10/ubi-micro:latest
COPY --from=builder /opt/app-root/src/dra-time-share /usr/bin/dra-time-share
COPY --from=builder /opt/app-root/src/sched-helper /usr/bin/sched-helper
ENTRYPOINT ["/usr/bin/dra-time-share"]
