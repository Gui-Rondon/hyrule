package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSingleScaffoldRunsWithoutInfrastructure(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if err := runNew([]string{"my-api", "--framework-version=v0.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("my-api"); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}
	run("mod", "edit", "-replace=github.com/Gui-Rondon/hyrule="+root)
	for _, name := range []string{"products", "other-items-test"} {
		if err := runService([]string{"create", name}); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.ReadFile("resource_products_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := runService([]string{"create", "products"}); err == nil {
		t.Fatal("overwrote resource")
	}
	if err := runService([]string{"create", "other_items"}); err == nil {
		t.Fatal("invalid name accepted")
	}
	after, _ := os.ReadFile("resource_products_generated.go")
	if string(after) != string(original) {
		t.Fatal("existing resource changed")
	}
	run("vet", "./...")
	run("build", "-o", "api", ".")
	bin, _ := filepath.Abs("api")
	start := func() (string, func()) {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		cmd := exec.Command(bin)
		cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), "CONFIG_FILE=", "CONFIGSERVER_ADDRESS=", "SQLITE_PATH=hyrule.db", "DRY_RUN=false")
		log, err := os.CreateTemp(t.TempDir(), "server-log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop := func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait(); _ = log.Close() }
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		address := fmt.Sprintf("http://127.0.0.1:%d", port)
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
			resp, err := http.Get(address + "/status")
			if err == nil {
				resp.Body.Close()
				return address, stop
			}
			time.Sleep(25 * time.Millisecond)
		}
		stop()
		raw, _ := os.ReadFile(log.Name())
		t.Fatalf("server did not start: %s", raw)
		return "", func() {}
	}
	address, stop := start()
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, address+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
		var result map[string]any
		_ = json.Unmarshal(raw, &result)
		return result
	}
	doc := request("POST", "/products", `{"name":"Sword"}`, 201)
	id := doc["_id"].(string)
	request("GET", "/other-items-test", "", 200)
	request("GET", "/api/openapi.json", "", 200)
	stop()
	address, stop = start()
	defer stop()
	if got := request("GET", "/products/"+id, "", 200); got["name"] != "Sword" {
		t.Fatal(got)
	}
}
func TestProjectAndVersionValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, value := range []string{"main", "latest", "v1.0.0+dirty", ""} {
		releaseVersion = ""
		if _, err := frameworkVersion(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	releaseVersion = "v0.103.0"
	t.Cleanup(func() { releaseVersion = "" })
	if got, err := frameworkVersion(""); err != nil || got != releaseVersion {
		t.Fatalf("%s %v", got, err)
	}
	if err := runNew([]string{"bad", "--layout=no"}); err == nil {
		t.Fatal("invalid layout")
	}
	if err := runNew([]string{"bad", "--unknown=yes"}); err == nil {
		t.Fatal("unknown flag")
	}
	for _, raw := range []string{`{`, `{"layout":"bad","backend":"sqlite"}`} {
		if err := os.WriteFile("hyrule.json", []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readProject(); err == nil {
			t.Fatal("bad manifest accepted")
		}
	}
}
