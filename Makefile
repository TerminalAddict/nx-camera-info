GO ?= go
GOEXE := $(shell $(GO) env GOEXE)

.PHONY: build test check release linux-amd64 linux-arm64 windows-amd64 windows-arm64 macos-amd64 macos-arm64

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info$(GOEXE) .
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

windows-amd64:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info-windows-amd64.exe .
	cp THIRD_PARTY_NOTICES.txt dist/

windows-arm64:
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info-windows-arm64.exe .
	cp THIRD_PARTY_NOTICES.txt dist/

macos-amd64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info-macos-amd64 .
	cp THIRD_PARTY_NOTICES.txt dist/

macos-arm64:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -trimpath -ldflags='-s -w' -o dist/nx-camera-info-macos-arm64 .
	cp THIRD_PARTY_NOTICES.txt dist/

release: linux-amd64 linux-arm64 windows-amd64 windows-arm64 macos-amd64 macos-arm64
	cd dist && sha256sum nx-camera-info-linux-amd64 nx-camera-info-linux-arm64 nx-camera-info-windows-amd64.exe nx-camera-info-windows-arm64.exe nx-camera-info-macos-amd64 nx-camera-info-macos-arm64 THIRD_PARTY_NOTICES.txt > SHA256SUMS
