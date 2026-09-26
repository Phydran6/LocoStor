VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web build test demo clean

all: web build

web:
	cd web && npm ci && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o locostor ./cmd/locostor

test:
	go vet ./...
	go test ./...

# Run with fake data on http://localhost:8080 (password: demo)
demo: web
	go run ./cmd/locostor -demo

clean:
	rm -f locostor
	find web/dist -mindepth 1 ! -name .gitkeep -delete
