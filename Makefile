VERSION ?= 0.1.0
GOCACHE ?= /private/tmp/go-build

.PHONY: test build release release-windows clean

test:
	GOCACHE=$(GOCACHE) go test ./...

build:
	GOCACHE=$(GOCACHE) go build -o build/deskbridge ./cmd/deskbridge

release: test
	mkdir -p outputs build/dist/deskbridge-$(VERSION)-darwin-arm64 build/dist/deskbridge-$(VERSION)-linux-amd64 build/deb/DEBIAN build/deb/usr/bin
	GOCACHE=$(GOCACHE) GOOS=darwin GOARCH=arm64 go build -o build/dist/deskbridge-$(VERSION)-darwin-arm64/deskbridge ./cmd/deskbridge
	GOCACHE=$(GOCACHE) GOOS=linux GOARCH=amd64 go build -o build/dist/deskbridge-$(VERSION)-linux-amd64/deskbridge ./cmd/deskbridge
	tar -czf outputs/deskbridge-$(VERSION)-darwin-arm64.tar.gz -C build/dist/deskbridge-$(VERSION)-darwin-arm64 deskbridge
	tar -czf outputs/deskbridge-$(VERSION)-linux-amd64.tar.gz -C build/dist/deskbridge-$(VERSION)-linux-amd64 deskbridge
	cp build/dist/deskbridge-$(VERSION)-linux-amd64/deskbridge build/deb/usr/bin/deskbridge
	cp packaging/debian/control build/deb/DEBIAN/control
	printf '2.0\n' > build/debian-binary
	tar -czf build/control.tar.gz -C build/deb/DEBIAN control
	tar -czf build/data.tar.gz -C build/deb usr
	GOCACHE=$(GOCACHE) go run ./tools/mkdeb outputs/deskbridge_$(VERSION)_amd64.deb build/debian-binary build/control.tar.gz build/data.tar.gz

release-windows: test
	mkdir -p outputs build/dist/deskbridge-$(VERSION)-windows-amd64
	GOCACHE=$(GOCACHE) GOOS=windows GOARCH=amd64 go build -o build/dist/deskbridge-$(VERSION)-windows-amd64/deskbridge.exe ./cmd/deskbridge
	tar -czf outputs/deskbridge-$(VERSION)-windows-amd64.tar.gz -C build/dist/deskbridge-$(VERSION)-windows-amd64 deskbridge.exe

clean:
	rm -rf build outputs
