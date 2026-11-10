# One image per service: docker build --build-arg SERVICE=tags-api .
# SERVICES_DIR defaults to the reference adopter shipped in this repo; a
# consumer of the framework with its own services directory overrides it.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# after go mod download: a build arg is part of the cache key of every later
# RUN, so declaring them earlier would miss the module layer on each new
# VERSION (the CI pushes this stage as :build-cache)
ARG SERVICE
ARG SERVICES_DIR=examples/blog-apis/services
ARG VERSION=dev
RUN test -n "$SERVICE" && test -d "$SERVICES_DIR/$SERVICE"
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/app ./${SERVICES_DIR}/${SERVICE}
# git_branch.txt is written by the CI build job and read by /status
RUN touch git_branch.txt && cp git_branch.txt /out/

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/app /out/git_branch.txt /app/
EXPOSE 8080
ENTRYPOINT ["/app/app"]
