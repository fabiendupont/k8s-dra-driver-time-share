FROM golang:1.23 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /dra-time-share ./cmd/driver

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /dra-time-share /usr/bin/dra-time-share
ENTRYPOINT ["/usr/bin/dra-time-share"]
