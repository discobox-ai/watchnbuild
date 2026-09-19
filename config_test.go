package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadYAMLWithDefaults(t *testing.T) {
	path := writeConfig(t, ".wnb.yaml", `
watch:
  extensions: [go]
  exclude: ["*_test.go"]
  exclude_dirs: [bin, node_modules]
build:
  command: go build -o bin/app .
run:
  command: ./bin/app
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Debounce.D() != 300*time.Millisecond {
		t.Fatalf("default debounce = %v", cfg.Debounce.D())
	}
	if cfg.Run.Grace.D() != 10*time.Second || cfg.Run.StopSignal != "TERM" {
		t.Fatalf("run defaults = %+v", cfg.Run)
	}
	if cfg.Build.Grace.D() != 2*time.Second {
		t.Fatalf("build grace = %v", cfg.Build.Grace.D())
	}
	if got := cfg.Watch.Paths; len(got) != 1 || got[0] != "." {
		t.Fatalf("default paths = %v", got)
	}
	if !cfg.Retry.On() || cfg.Retry.Initial.D() != 15*time.Second || cfg.Retry.Max.D() != 10*time.Minute {
		t.Fatalf("retry defaults = %+v", cfg.Retry)
	}
}

func TestDefaultExtensionsApplied(t *testing.T) {
	path := writeConfig(t, ".wnb.yaml", `build: {command: make}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Watch.ExtensionsDefaulted() {
		t.Fatal("extensions were not defaulted")
	}
	for _, rel := range []string{"main.go", "src/app.ts", "web/index.html", "go.mod", "db/schema.sql"} {
		if !cfg.Matches(rel) {
			t.Errorf("default extensions do not match %q", rel)
		}
	}
	// The reason for a default allowlist: build output stops retriggering.
	for _, rel := range []string{"build-errors.log", "build/app", "data.db", "cover.out", "notes.md"} {
		if cfg.Matches(rel) {
			t.Errorf("default extensions match build output %q", rel)
		}
	}
}

func TestEmptyExtensionsMatchesEverything(t *testing.T) {
	path := writeConfig(t, ".wnb.yaml", `
build: {command: make}
watch: {extensions: []}
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watch.ExtensionsDefaulted() {
		t.Fatal("an explicit empty list was overwritten by the defaults")
	}
	if !cfg.Matches("anything.whatever") {
		t.Error("explicit empty extensions should match every file")
	}
}

func TestDefaultExtensionsAreNormalized(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range DefaultExtensions {
		if strings.HasPrefix(e, ".") || e != strings.ToLower(e) || e == "" {
			t.Errorf("extension %q should be lowercase with no leading dot", e)
		}
		if seen[e] {
			t.Errorf("duplicate extension %q", e)
		}
		seen[e] = true
	}
}

func TestRetryDisabledAndValidated(t *testing.T) {
	path := writeConfig(t, ".wnb.yaml", `
build: {command: make}
retry: {enabled: false}
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retry.On() {
		t.Fatal("retry.enabled: false was not honored")
	}

	path = writeConfig(t, ".wnb.yml", `
build: {command: make}
retry: {initial: 5m, max: 1m}
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for retry.max below retry.initial")
	}
}

func TestLoadJSON(t *testing.T) {
	path := writeConfig(t, ".wnb.json", `{
  "debounce": "1s",
  "build": {"command": "make", "grace": "5s"},
  "run": {"command": "./bin/app", "stop_signal": "SIGINT", "grace": "3s"}
}`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Debounce.D() != time.Second || cfg.Build.Grace.D() != 5*time.Second {
		t.Fatalf("parsed durations wrong: %+v", cfg)
	}
	if cfg.Run.StopSignal != "SIGINT" {
		t.Fatalf("stop_signal = %q", cfg.Run.StopSignal)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	path := writeConfig(t, ".wnb.yaml", `
build:
  command: make
  comand_typo: oops
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestDefaultCommands(t *testing.T) {
	cases := []struct {
		yaml, build, run string
	}{
		// Nothing set: build and run the Go app.
		{`{}`, DefaultBuildCommand, DefaultRunCommand},
		// A run command alone keeps the default build.
		{`run: {command: ./app serve}`, DefaultBuildCommand, "./app serve"},
		// A custom build with no run is watch-and-build only.
		{`build: {command: make}`, "make", ""},
	}
	for _, c := range cases {
		cfg, err := LoadConfig(writeConfig(t, ".wnb.yaml", c.yaml))
		if err != nil {
			t.Fatalf("%s: %v", c.yaml, err)
		}
		if cfg.Build.Command != c.build || cfg.Run.Command != c.run {
			t.Errorf("%s: commands = %q, %q; want %q, %q", c.yaml, cfg.Build.Command, cfg.Run.Command, c.build, c.run)
		}
	}
}

func TestBadSignalRejected(t *testing.T) {
	path := writeConfig(t, ".wnb.yaml", `
build: {command: make}
run: {command: ./app, stop_signal: SIGWAT}
`)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for bad signal")
	}
}

func TestMatches(t *testing.T) {
	cfg := &Config{Watch: WatchConfig{
		Extensions: []string{"go", ".yaml"},
		Include:    []string{"go.mod", "go.sum"},
		Exclude:    []string{"*_test.go", "docs/*"},
	}}
	cases := []struct {
		path string
		want bool
	}{
		{"pkg/api/server.go", true},
		{"pkg/api/server_test.go", false},
		{"chart/values.yaml", true},
		{"go.mod", true},
		{"docs/readme.md", false},
		{"pkg/readme.md", false},
	}
	for _, c := range cases {
		if got := cfg.Matches(c.path); got != c.want {
			t.Errorf("Matches(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestMatchesAllWhenNoExtensions(t *testing.T) {
	cfg := &Config{Watch: WatchConfig{Exclude: []string{"*.log"}}}
	if !cfg.Matches("anything.txt") {
		t.Fatal("empty extensions should match everything")
	}
	if cfg.Matches("noisy.log") {
		t.Fatal("exclude should still apply")
	}
	for _, name := range []string{buildReportFile, runReportFile, reportTemp(buildReportFile)} {
		if cfg.Matches(name) {
			t.Errorf("failure report %s must never trigger a build", name)
		}
	}
}

func TestExcludesDir(t *testing.T) {
	cfg := &Config{Watch: WatchConfig{ExcludeDirs: []string{"bin", "ui/user/node_modules"}}}
	cases := []struct {
		path string
		want bool
	}{
		{"bin", true},
		{"pkg/bin", true}, // base-name match, like air's exclude_dir
		{"ui/user/node_modules", true},
		{".git", true}, // hidden always skipped
		{"pkg", false},
	}
	for _, c := range cases {
		if got := cfg.ExcludesDir(c.path); got != c.want {
			t.Errorf("ExcludesDir(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestSampleConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := writeSampleConfig(".wnb.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := writeSampleConfig(".wnb.yaml"); err == nil {
		t.Fatal("overwrote an existing config")
	}

	// Untouched, the sample loads as all defaults.
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("untouched sample does not load: %v", err)
	}
	if cfg.Build.Command != DefaultBuildCommand || cfg.Run.Command != DefaultRunCommand {
		t.Errorf("untouched sample commands = %q, %q", cfg.Build.Command, cfg.Run.Command)
	}

	// Every option line uncommented must be a valid config, so the
	// sample can't drift from the real field names.
	var lines []string
	for _, l := range strings.Split(sampleConfig, "\n") {
		body := strings.TrimLeft(l, " ")
		if strings.HasPrefix(body, "#") && !strings.HasPrefix(body, "##") {
			l = l[:len(l)-len(body)] + body[1:]
		}
		lines = append(lines, l)
	}
	path := writeConfig(t, "full.yaml", strings.Join(lines, "\n"))
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("uncommented sample does not load: %v", err)
	}
	if cfg.Build.Command == "" || cfg.Run.Command == "" || cfg.Retry.Max.D() != 10*time.Minute {
		t.Errorf("uncommented sample loaded as %+v", cfg)
	}
}
