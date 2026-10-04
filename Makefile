.PHONY: dev db identity vault migrate seed api worker media-worker vision-setup vision-test web test test-integration check generate
vision-setup:
	python3 -m venv services/vision/.venv
	services/vision/.venv/bin/pip install -r services/vision/requirements.txt
	services/vision/.venv/bin/python services/vision/download_model.py
	services/vision/run --version
vision-test:
	services/vision/.venv/bin/python -m unittest discover -s services/vision -p '*_test.py'
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
