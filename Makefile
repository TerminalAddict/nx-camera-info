GO ?= go

.PHONY: build test check linux-amd64 linux-arm64

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info .
	cp THIRD_PARTY_NOTICES.txt dist/

test:
	$(GO) test ./...

check:
	$(GO) vet ./...
	$(GO) test -race ./...

linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info-linux-amd64 .
	cp THIRD_PARTY_NOTICES.txt dist/

linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info-linux-arm64 .
	cp THIRD_PARTY_NOTICES.txt dist/
