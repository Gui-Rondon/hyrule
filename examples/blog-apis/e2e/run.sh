#!/usr/bin/env bash
# Functional/concurrency e2e checks (CRUD, hooks, outbox, auth, optimistic
# concurrency) for the blog-apis example: a real JMeter instance (in
# Docker) drives real containers of tags-api/authors-api/categories-api/
# posts-api/comments-api, talking to the same Mongo/Redis/Pub-Sub/Postgres
# `make env-up` starts for the framework's own tests, plus Elasticsearch
# (best effort) -- no mocks anywhere in this path. comments-api is the
# Postgres path (restpostgres, not app.Main -- it has no Mongo config
# file or /status, see examples/blog-apis/services/comments-api/main.go):
# the other four stay Mongo-only, unchanged. Complements loadtest/
# (read-only perf): this plan asserts correctness, and its loop count
# doubles as a light load knob.
#
#   examples/blog-apis/e2e/run.sh [loops]
#   examples/blog-apis/e2e/run.sh down
#
#   loops   how many times each functional Thread Group repeats (default
#           1).
#   down    remove the e2e containers and network this script owns, and
#           exit -- make env-up's own infra (mongo/redis/pubsub/postgres)
#           is left running, since this script does not own it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
here="$root/examples/blog-apis/e2e"
net=hyrule-e2e
services="tags-api authors-api categories-api posts-api comments-api"

teardown() {
	# shellcheck disable=SC2086
	docker rm -f $(printf 'e2e-%s ' $services) >/dev/null 2>&1 || true
	# $net can only be removed once every container is off it -- including
	# the shared infra this script joined to it (never created by it).
	for cid in $(docker network inspect "$net" --format '{{range .Containers}}{{.Name}} {{end}}' 2>/dev/null || true); do
		docker network disconnect "$net" "$cid" >/dev/null 2>&1 || true
	done
	docker network rm "$net" >/dev/null 2>&1 || true
}

if [ "${1:-}" = down ]; then
	teardown
	echo "e2e containers and network removed (make env-up's own infra left running)."
	exit 0
fi
loops=${1:-1}

echo "==> starting shared infra (make env-up)"
make -C "$root" env-up

echo "==> starting Elasticsearch (best effort -- tags-api logs and skips indexing without it)"
docker compose -f "$root/examples/blog-apis/docker-compose.yml" --profile debug up -d --wait elasticsearch 2>&1 | tail -5 ||
	echo "    (Elasticsearch unavailable -- the two ES checks in the plan will fail, everything else still runs)"

echo "==> network: $net"
docker network create "$net" >/dev/null 2>&1 || true
# Each shared container is discovered through the compose file that owns
# it (so this works whatever the checkout directory -- and so the
# compose project name -- is called), then joined to $net under the
# plain alias the e2e config files expect ("mongo", "redis"...).
for entry in "docker-compose.yml:mongo" "docker-compose.yml:redis" "docker-compose.yml:pubsub" "docker-compose.yml:postgres" "examples/blog-apis/docker-compose.yml:elasticsearch"; do
	file=${entry%%:*}
	svc=${entry##*:}
	cid=$(docker compose -f "$root/$file" ps -q "$svc" 2>/dev/null || true)
	[ -n "$cid" ] && docker network connect --alias "$svc" "$net" "$cid" 2>/dev/null || true
done

echo "==> generating a throwaway RSA key pair for the legacy JWT fallback"
mkdir -p "$here/.keys"
openssl genrsa -out "$here/.keys/legacy.key" 2048 >/dev/null 2>&1
openssl rsa -in "$here/.keys/legacy.key" -pubout -out "$here/.keys/legacy.pub" >/dev/null 2>&1

echo "==> minting a 2h test token"
token=$(cd "$root" && go run ./examples/blog-apis/e2e/mint "$here/.keys/legacy.key" e2e_automation 2h)

echo "==> building service images"
for svc in $services; do
	docker build --build-arg SERVICE="$svc" -t "hyrule-e2e/${svc}:dev" "$root" >/dev/null
done

echo "==> starting e2e-{tags,authors,categories,posts,comments}-api"
# shellcheck disable=SC2086
docker rm -f $(printf 'e2e-%s ' $services) >/dev/null 2>&1 || true

docker run -d --name e2e-tags-api --network "$net" \
	-e PORT=8080 -e MONGO_CONNECTION_STRING="mongodb://mongo:27017/?replicaSet=rs0" \
	-e CONFIG_FILE=/cfg/tags-api.json \
	-v "$here/config/tags-api.json:/cfg/tags-api.json:ro" \
	"hyrule-e2e/tags-api:dev" >/dev/null

docker run -d --name e2e-authors-api --network "$net" \
	-e PORT=8080 -e MONGO_CONNECTION_STRING="mongodb://mongo:27017/?replicaSet=rs0" \
	-e CONFIG_FILE=/cfg/authors-api.json \
	-v "$here/config/authors-api.json:/cfg/authors-api.json:ro" \
	-v "$here/.keys/legacy.pub:/keys/legacy.pub:ro" \
	"hyrule-e2e/authors-api:dev" >/dev/null

docker run -d --name e2e-categories-api --network "$net" \
	-e PORT=8080 -e MONGO_CONNECTION_STRING="mongodb://mongo:27017/?replicaSet=rs0" \
	-e CONFIG_FILE=/cfg/categories-api.json \
	-v "$here/config/categories-api.json:/cfg/categories-api.json:ro" \
	"hyrule-e2e/categories-api:dev" >/dev/null

docker run -d --name e2e-posts-api --network "$net" \
	-e PORT=8080 -e MONGO_CONNECTION_STRING="mongodb://mongo:27017/?replicaSet=rs0" \
	-e CONFIG_FILE=/cfg/posts-api.json -e PUBSUB_EMULATOR_HOST=pubsub:8085 \
	-v "$here/config/posts-api.json:/cfg/posts-api.json:ro" \
	-v "$here/.keys/legacy.pub:/keys/legacy.pub:ro" \
	"hyrule-e2e/posts-api:dev" >/dev/null

# No CONFIG_FILE/MONGO_*: comments-api bypasses app.Main entirely
# (platform/postgres+restpostgres directly, see its main.go) -- its own
# table/indexes (EnsureFunctions/EnsureIndexes) are created at startup
# against the postgres alias joined to $net above.
docker run -d --name e2e-comments-api --network "$net" \
	-e PORT=8080 -e POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@postgres:5432/postgres?sslmode=disable" \
	"hyrule-e2e/comments-api:dev" >/dev/null

echo "==> waiting for the 5 services to answer"
docker run --rm --network "$net" curlimages/curl:8.10.1 sh -c '
	for i in $(seq 1 30); do
		ok=1
		for svc in e2e-tags-api e2e-authors-api e2e-categories-api e2e-posts-api; do
			curl -sf "http://$svc:8080/status" >/dev/null || ok=0
		done
		# comments-api has no app.Main /status -- its home route (Eve
		# _links document) is the equivalent readiness signal.
		curl -sf "http://e2e-comments-api:8080/" >/dev/null || ok=0
		[ "$ok" = 1 ] && exit 0
		sleep 1
	done
	echo "timed out waiting for services" >&2
	exit 1
'

echo "==> creating the Pub/Sub topics posts-api publishes to (idempotent)"
curl -sf -X PUT http://localhost:8085/v1/projects/local-dev/topics/posts-to-publish >/dev/null || true
curl -sf -X PUT http://localhost:8085/v1/projects/local-dev/topics/posts-to-unpublish >/dev/null || true

echo "==> running the JMeter plan ($loops loop(s))"
mkdir -p "$here/results"
rm -f "$here"/results/*
docker run --rm --network "$net" \
	-v "$here/jmeter:/jmx:ro" -v "$here/results:/results" \
	justb4/jmeter:latest -n -t /jmx/hyrule-e2e.jmx -l /results/results.jtl -j /results/jmeter.log \
	-Jauth_token="$token" -Jloops="$loops"

echo
echo "Results: $here/results/results.jtl ($here/results/jmeter.log for the full JMeter log)"
echo "Containers left running for inspection -- '$0 down' to remove them."
