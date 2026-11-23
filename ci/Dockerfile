# Image of the "test" and "detect services" jobs (ci-templates go/, docs/ci.md):
# golang with the modules downloaded and the external packages and the
# standard library already compiled, with and without -race. The runners have
# no shared cache, so without it every job downloads and compiles them again.
# The CI tags it with the hash of go.mod, go.sum and this file.
FROM golang:1.27@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
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
