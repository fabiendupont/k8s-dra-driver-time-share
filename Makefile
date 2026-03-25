BINARY    := dra-time-share
IMAGE     := quay.io/fabiendupont/dra-time-share
TAG       := latest
MODULE    := github.com/fabiendupont/k8s-dra-driver-time-share

.PHONY: build test image clean

build:
	go build -o bin/$(BINARY) ./cmd/driver

test:
	go test ./...

image:
	podman build -t $(IMAGE):$(TAG) .

clean:
	rm -rf bin/
