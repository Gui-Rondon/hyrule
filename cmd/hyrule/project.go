package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/semver"
)

type projectConfig struct {
	Layout  string `json:"layout"`
	Backend string `json:"backend"`
}

// releaseVersion can be set for release artifacts using -ldflags.
var releaseVersion string

func frameworkVersion(explicit string) (string, error) {
	value := explicit
	if value == "" {
		value = releaseVersion
	}
	if value == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			value = info.Main.Version
		}
	}
	if !semver.IsValid(value) || strings.Contains(value, "+") {
		return "", fmt.Errorf("development CLI requires --framework-version=vX.Y.Z (or a Go pseudo-version)")
	}
	return value, nil
}
func readProject() (projectConfig, error) {
	raw, err := os.ReadFile("hyrule.json")
	if os.IsNotExist(err) { // Legacy platform scaffolds had no manifest.
		if info, e := os.Stat("services"); e == nil && info.IsDir() {
			return projectConfig{"platform", "mongo"}, nil
		}
	}
	if err != nil {
		return projectConfig{}, fmt.Errorf("run from a Hyrule project root: %w", err)
	}
	var cfg projectConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Layout == "single" && cfg.Backend == "sqlite" || cfg.Layout == "platform" && cfg.Backend == "mongo" {
		return cfg, nil
	}
	return cfg, fmt.Errorf("unsupported project layout/backend %q/%q", cfg.Layout, cfg.Backend)
}
