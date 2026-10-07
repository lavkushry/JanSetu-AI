.PHONY: dev db identity vault migrate seed api worker media-worker vision-setup pothole-setup vision-test web test test-integration check generate recommendation recommendation-check recommendation-benchmark recommendation-generate recommendation-browser-proof recommendation-stream-proof
PYTHON ?= python3
vision-setup:
	$(PYTHON) -m venv services/vision/.venv
	services/vision/.venv/bin/pip install -r services/vision/requirements.txt
	services/vision/.venv/bin/python services/vision/download_model.py
	services/vision/run --version
pothole-setup: vision-setup
	services/vision/.venv/bin/python -m venv services/vision/.export-venv
	services/vision/.export-venv/bin/pip install -r services/vision/export-requirements.txt
	services/vision/.export-venv/bin/pip install --no-deps torch==2.10.0+cpu torchvision==0.25.0+cpu --index-url https://download.pytorch.org/whl/cpu
	services/vision/.export-venv/bin/python -I services/vision/export_pothole.py
	services/vision/pothole-run --version
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
	$(MAKE) recommendation-check
	python3 scripts/validate_specs.py
	cd services/backend && go vet ./... && go test ./...
	npm run typecheck
	npm run build
generate:
	cd services/backend && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
	npm run contracts

recommendation:
	cargo run --locked --manifest-path services/recommendation/Cargo.toml --bin jansetu-recommendation
recommendation-check:
	cargo fmt --manifest-path services/recommendation/Cargo.toml --check
	cargo clippy --locked --manifest-path services/recommendation/Cargo.toml --all-targets -- -D warnings
	cargo test --locked --manifest-path services/recommendation/Cargo.toml
	python3 -m unittest discover -s services/training -p '*_test.py'
recommendation-benchmark:
	cargo run --locked --release --manifest-path services/recommendation/Cargo.toml --bin benchmark -- http://127.0.0.1:50051 1000 8
recommendation-generate:
	bash scripts/generate_recommendation_contract.sh

recommendation-browser-proof:
	python3 scripts/recommendation_browser_proof.py

recommendation-stream-proof:
	@set -eu; trap 'docker compose -f "$(CURDIR)/infra/recommendation/compose.proof.yaml" down -v' EXIT; \
	 docker compose -f infra/recommendation/compose.proof.yaml up -d --wait; \
	 cd services/backend && JANSETU_INTEGRATION=1 JANSETU_RECOMMENDATION_STREAM_PROOF=1 go test -race ./internal/app -run TestRecommendationStream -count=1
