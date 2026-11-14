SERVICE ?= tags-api
MONGO_URI ?= mongodb://localhost:27018/?directConnection=true
POSTGRES_URI ?= postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable
GO_PORT ?= 8081

export MONGO_TEST_URI ?= $(MONGO_URI)
export PUBSUB_EMULATOR_HOST ?= localhost:8085
export REDIS_TEST_ADDR ?= localhost:6380
export POSTGRES_TEST_URI ?= $(POSTGRES_URI)

COVERAGE_MIN ?= 85

.PHONY: test unit vet cover build image env-up env-down example-env-up example-env-down run loadtest e2e e2e-down standalone-example-test

# All tests; the ones that need Mongo/Postgres use MONGO_TEST_URI/
# POSTGRES_TEST_URI (make env-up).
test:
	go test -race ./...

# Tests that need no external service.
unit:
	MONGO_TEST_URI= POSTGRES_TEST_URI= go test -race ./...

vet:
	go vet ./...

SERVICES_DIR ?= examples/blog-apis/services

# Same gate as the CI: fails below COVERAGE_MIN % (testkit excluded).
cover:
	@mkdir -p .local
	go test -coverpkg=$$(go list ./app/... ./behaviors/... ./declarative/... ./outbox/... ./platform/... ./rest/... ./restpostgres/... ./testkit/... ./internal/... ./$(SERVICES_DIR)/... | grep -v /testkit | paste -sd, -) \
		-coverprofile=.local/coverage.out ./... > /dev/null
	@go tool cover -func=.local/coverage.out | awk -v m=$(COVERAGE_MIN) \
		'/^total:/ { sub("%", "", $$3); printf "coverage %s%% (minimum %s%%)\n", $$3, m; exit !($$3 + 0 >= m + 0) }'
	@echo "HTML: go tool cover -html=.local/coverage.out"

build:
	go build -o .local/$(SERVICE) ./$(SERVICES_DIR)/$(SERVICE)

image:
	docker build --build-arg SERVICE=$(SERVICE) -t hyrule/$(SERVICE):dev .

# Generic infra only (mongo/pubsub/redis), for the framework's own tests.
env-up:
	docker compose up -d --build --wait

env-down:
	docker compose down

# examples/blog-apis also wants Elasticsearch (--profile debug), for
# posts-api/authors-api/tags-api's optional search indexing.
example-env-up:
	docker compose -f examples/blog-apis/docker-compose.yml up -d --build --wait

example-env-down:
	docker compose -f examples/blog-apis/docker-compose.yml down

# Runs the service with auth disabled against the local Mongo.
run: build
	@mkdir -p .local
	@echo '{"auth": {"enable": "false"}}' > .local/$(SERVICE).json
	CONFIG_FILE=.local/$(SERVICE).json \
	MONGO_CONNECTION_STRING='mongodb://localhost:27018/tags_go?directConnection=true' \
	PORT=$(GO_PORT) .local/$(SERVICE)

# Read-only load test against BASE_URL, e.g. a staging service:
#   make loadtest BASE_URL=http://localhost:8081 RESOURCE=tags LOOKUP=go
loadtest:
	examples/blog-apis/loadtest/run.sh $(BASE_URL) $(RESOURCE) $(LOOKUP)

# Functional/concurrency e2e checks (CRUD, hooks, outbox, auth, CAS) for
# the blog-apis example, over a real JMeter run in Docker:
#   make e2e [LOOPS=1]
#   make e2e-down   # remove the containers/network this target owns
e2e:
	examples/blog-apis/e2e/run.sh $(LOOPS)

e2e-down:
	examples/blog-apis/e2e/run.sh down

# examples/standalone-tags-api is its own Go module (own go.mod/go.sum,
# Fase 2's proof of external importability) — go test ./... from the repo
# root doesn't descend into it, hence this separate target.
standalone-example-test:
	cd examples/standalone-tags-api && go test -race ./...
