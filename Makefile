.PHONY: dev db identity vault migrate seed api worker media-worker web test test-integration check generate
db:
	docker compose up -d --build --wait db
identity:
	docker compose up -d --wait identity
migrate:
	cd services/backend && go run ./cmd/migrate -dir ../../db/migrations
seed:
	cd services/backend && go run ./cmd/migrate -dir ../../db/migrations -seed ../../db/seed/local.sql
api:
	cd services/backend && go run ./cmd/public-api
vault:
	cd services/backend && go run ./cmd/vault
worker:
	cd services/backend && go run ./cmd/worker
media-worker:
	cd services/backend && go run ./cmd/media-worker
web:
	npm run dev
dev:
	docker compose up --build
test:
	cd services/backend && go test ./...
test-integration:
	cd services/backend && JANSETU_INTEGRATION=1 go test -race ./...
check:
	python3 scripts/validate_specs.py
	cd services/backend && go vet ./... && go test ./...
	npm run typecheck
	npm run build
generate:
	cd services/backend && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
	npm run contracts
