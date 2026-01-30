.PHONY: all build test clean migrate-up migrate-down docker-up docker-down generate

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOVET=$(GOCMD) vet

# Build directory
BUILD_DIR=./bin

# Services
SERVICES=iam customer rules fees ledger wallet deposits payments

# Database
DATABASE_URL?=postgres://fineract:fineract@localhost:5432/fineract_default?sslmode=disable

all: build

build: $(SERVICES)

$(SERVICES):
	$(GOBUILD) -o $(BUILD_DIR)/$@ ./cmd/$@

test:
	$(GOTEST) -v -race ./...

test-coverage:
	$(GOTEST) -v -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Linting
vet:
	$(GOVET) ./...

# Migrations
migrate-up:
	migrate -path ./migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path ./migrations -database "$(DATABASE_URL)" down 1

migrate-create:
	@read -p "Enter migration name: " name; \
	migrate create -ext sql -dir ./migrations -seq $$name

migrate-force:
	@read -p "Enter version to force: " version; \
	migrate -path ./migrations -database "$(DATABASE_URL)" force $$version

# Docker
docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

# SQLC (if using generated code)
generate:
	sqlc generate

# Run individual services
run-iam:
	$(GOCMD) run ./cmd/iam

run-customer:
	$(GOCMD) run ./cmd/customer

run-rules:
	$(GOCMD) run ./cmd/rules

run-fees:
	$(GOCMD) run ./cmd/fees

run-ledger:
	$(GOCMD) run ./cmd/ledger

run-wallet:
	$(GOCMD) run ./cmd/wallet

run-deposits:
	$(GOCMD) run ./cmd/deposits

run-payments:
	$(GOCMD) run ./cmd/payments

# Run all services (development)
run-all:
	@echo "Starting all services..."
	@$(MAKE) run-iam &
	@$(MAKE) run-customer &
	@$(MAKE) run-rules &
	@$(MAKE) run-fees &
	@$(MAKE) run-ledger &
	@$(MAKE) run-wallet &
	@$(MAKE) run-deposits &
	@$(MAKE) run-payments &
	@wait

# Development setup
dev-setup: deps docker-up migrate-up
	@echo "Development environment ready!"

# Health check
health:
	@echo "Checking service health..."
	@curl -s http://localhost:8081/health || echo "IAM: DOWN"
	@curl -s http://localhost:8082/health || echo "Customer: DOWN"
	@curl -s http://localhost:8083/health || echo "Rules: DOWN"
	@curl -s http://localhost:8084/health || echo "Fees: DOWN"
	@curl -s http://localhost:8085/health || echo "Ledger: DOWN"
	@curl -s http://localhost:8086/health || echo "Wallet: DOWN"
	@curl -s http://localhost:8087/health || echo "Deposits: DOWN"
	@curl -s http://localhost:8088/health || echo "Payments: DOWN"
