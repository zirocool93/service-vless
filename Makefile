.PHONY: frontend build check run test

frontend:
	cd web && npm ci && npm run build

build: frontend
	go build -o bin/gateway ./cmd/gateway

test: frontend
	go test ./cmd/... ./internal/... ./web

check:
	cd web && npm ci && npm run lint && npm run typecheck && npm run test && npm run build
	@test -z "$$(gofmt -l cmd internal web/embed.go)" || { echo "Go-файлы требуют gofmt"; exit 1; }
	go vet ./cmd/... ./internal/... ./web
	go test ./cmd/... ./internal/... ./web
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/gateway-linux-amd64 ./cmd/gateway

run: build
	./bin/gateway serve --dev-http --listen 127.0.0.1:8443 --data-dir .ssh/local-data --secrets-dir .ssh/local-secrets
