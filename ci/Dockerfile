# Image of the "test" and "detect services" jobs (ci-templates go/, docs/ci.md):
# golang with the modules downloaded and the external packages and the
# standard library already compiled, with and without -race. The runners have
# no shared cache, so without it every job downloads and compiles them again.
# The CI tags it with the hash of go.mod, go.sum and this file.
FROM golang:1.27
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# only to list the packages the repository imports; .dockerignore leaves out
# contract/, hence -e
COPY . .
RUN PKGS=$(go list -e -deps -test -f '{{if and .Module (not .Module.Main)}}{{.ImportPath}}{{end}}' ./...) \
    && go build $PKGS std \
    && go build -race $PKGS std \
    && cd / && rm -rf /src
WORKDIR /
