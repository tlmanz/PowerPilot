.PHONY: build test hwtest run app tray install uninstall dist clean

VERSION ?= dev

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/powerpilot ./cmd/powerpilot

test:
	go vet ./...
	GOOS=linux CGO_ENABLED=0 go vet ./...
	GOOS=windows CGO_ENABLED=0 go vet ./...
	go test ./...

# Read-only queries against the attached UPS.
hwtest:
	UPS_HARDWARE=1 go test -count=1 -v ./internal/hid

# Service in the foreground (Ctrl+C to stop); dashboard on http://127.0.0.1:8484.
run: build
	./bin/powerpilot serve

# Dashboard window against a running service.
app: build
	./bin/powerpilot app -no-install

# Tray icon against a running service (exits if another PowerPilot shows it).
tray: build
	./bin/powerpilot tray

# macOS: build PowerPilot.app into ~/Applications and start the service.
install:
	./scripts/install.sh

uninstall:
	./scripts/uninstall.sh

# Release archives for every platform into dist/ (macOS needs to run on a Mac).
dist:
	./scripts/build-release.sh darwin $(VERSION)
	./scripts/build-release.sh linux $(VERSION)
	./scripts/build-release.sh windows $(VERSION)

clean:
	rm -rf bin dist
