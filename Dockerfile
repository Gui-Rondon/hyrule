# One image per service: docker build --build-arg SERVICE=tags-api .
# SERVICES_DIR defaults to the reference adopter shipped in this repo; a
# consumer of the framework with its own services directory overrides it.
FROM golang:1.27@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /app
COPY --from=build /out/app /out/git_branch.txt /app/
EXPOSE 8080
ENTRYPOINT ["/app/app"]
