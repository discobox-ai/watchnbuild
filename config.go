package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfigNames are searched, in order, in the working directory when no
// -config flag is given. YAML is a superset of JSON, so a single parser
// handles both.
var DefaultConfigNames = []string{".wnb.yaml", ".wnb.yml", ".wnb.json"}

// DefaultExtensions are the file types that trigger a build when
// watch.extensions is not set: source, templates, and the config and schema
// files builds usually read. The point of a default allowlist is that the
// things a build *writes* — logs, databases, binaries, coverage profiles —
// no longer retrigger it, which is the most common cause of a build loop.
//
// The cost of an allowlist is that an unlisted type is silently ignored, so
// this list errs long. Set watch.extensions to override it, or to an
// explicit empty list ("extensions: []") to trigger on every file.
var DefaultExtensions = []string{
	// Go, and the module files that change what a build resolves to.
	"go", "mod", "sum", "work",
	// C, C++, Objective-C, and assembly.
	"c", "h", "cc", "cpp", "cxx", "hpp", "hh", "hxx", "inl", "m", "mm", "s", "asm",
	// Other compiled languages.
	"rs", "zig", "swift", "d", "nim", "cr", "v", "odin",
	// JVM and .NET.
	"java", "kt", "kts", "scala", "sc", "clj", "cljc", "cljs", "groovy",
	"cs", "fs", "fsi", "fsx", "vb",
	// Scripting languages.
	"py", "pyi", "rb", "rake", "php", "pl", "pm", "lua", "tcl", "r", "jl",
	"ex", "exs", "erl", "hrl", "hs", "lhs", "ml", "mli", "dart", "elm", "pas",
	// Shell and batch.
	"sh", "bash", "zsh", "fish", "ps1", "psm1", "psd1", "bat", "cmd", "awk", "sed",
	// JavaScript, TypeScript, and component frameworks.
	"js", "jsx", "mjs", "cjs", "ts", "tsx", "mts", "cts", "vue", "svelte", "astro",
	// Markup and styles.
	"html", "htm", "xhtml", "css", "scss", "sass", "less", "styl",
	// Templates.
	"tmpl", "tpl", "gotmpl", "gohtml", "hbs", "handlebars", "mustache", "ejs",
	"erb", "haml", "slim", "liquid", "twig", "pug", "jinja", "jinja2", "j2",
	// Interface definitions and schemas.
	"proto", "graphql", "gql", "thrift", "avsc", "capnp", "fbs", "sql", "cql",
	"xsd", "wsdl", "openapi", "swagger",
	// Data and configuration a build or a running process reads.
	"json", "jsonc", "json5", "yaml", "yml", "toml", "ini", "cfg", "conf",
	"properties", "xml", "plist", "env", "tf", "tfvars", "hcl",
	// Build definitions.
	"mk", "mak", "cmake", "gradle", "sbt", "bzl", "bazel", "nix", "just",
	// Hardware description, since a "build" here is a synthesis run.
	"vhd", "vhdl", "sv", "svh", "f90", "f95",
}

// Duration is a time.Duration that unmarshals from a YAML/JSON string like
// "300ms" or "10s", or from a bare number of seconds.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var secs float64
	if err := node.Decode(&secs); err == nil {
		*d = Duration(time.Duration(secs * float64(time.Second)))
		return nil
	}
	return fmt.Errorf("invalid duration value on line %d", node.Line)
}

func (d Duration) D() time.Duration { return time.Duration(d) }

type WatchConfig struct {
	// Paths are the roots watched recursively. Default: ["."].
	Paths []string `yaml:"paths"`
	// Extensions of files that trigger a build (no leading dot). Omitted
	// means DefaultExtensions; an explicit empty list means every file not
	// otherwise excluded triggers.
	Extensions []string `yaml:"extensions"`
	// Include lists extra files that trigger a build even if their
	// extension does not match: exact base names ("go.mod") or globs.
	Include []string `yaml:"include"`
	// Exclude lists files that never trigger a build: globs matched
	// against the base name and the path relative to the working
	// directory ("*_test.go", "docs/**").
	Exclude []string `yaml:"exclude"`
	// ExcludeDirs are directory names or relative paths that are not
	// watched at all. Hidden directories are always skipped.
	ExcludeDirs []string `yaml:"exclude_dirs"`

	// extDefaulted records that Extensions came from DefaultExtensions, so
	// startup can say so — an allowlist that silently drops a file type is
	// only debuggable if you know one is in effect.
	extDefaulted bool
}

// ExtensionsDefaulted reports whether the built-in extension list is in use.
func (w WatchConfig) ExtensionsDefaulted() bool { return w.extDefaulted }

type BuildConfig struct {
	// Command is run through the shell whenever watched files change. A
	// still-running build is stopped (whole process group) before a new
	// one starts.
	Command string `yaml:"command"`
	// Grace is how long a cancelled build gets after the stop signal
	// before its process group is SIGKILLed. Default: 2s.
	Grace Duration `yaml:"grace"`
}

type RunConfig struct {
	// Command is (re)started after every successful build. Optional: with
	// no run command watchnbuild is just a watch-and-build loop.
	Command string `yaml:"command"`
	// StopSignal is sent to the process group to request a graceful stop.
	// Accepts "TERM", "SIGTERM", "INT", "HUP", "USR1", "USR2", "QUIT",
	// "KILL". Default: TERM.
	StopSignal string `yaml:"stop_signal"`
	// Grace is how long the process gets after StopSignal before its
	// process group is SIGKILLed. 0 force-kills immediately. Default: 10s.
	Grace Duration `yaml:"grace"`
	// AllowExit treats a clean (status 0) exit of the run command as a
	// legitimate end rather than a failure. Off by default: a run command
	// is normally a server, and a server that exits — even politely — has
	// stopped serving, which is the state retries exist to recover from.
	// Turn it on for a run command that is meant to finish.
	AllowExit bool `yaml:"allow_exit"`
}

// RetryConfig controls automatic re-running of the build after a failure,
// so a broken state recovers without needing a file change. Triggers alone
// are not enough: a fix can land in a file that doesn't match the watch
// patterns, and a start failure is often transient (port still held, a
// dependency not up yet).
type RetryConfig struct {
	// Enabled turns retries on. Default: true.
	Enabled *bool `yaml:"enabled"`
	// Initial is the delay before the first retry. Default: 15s.
	Initial Duration `yaml:"initial"`
	// Max caps the delay, which doubles after each consecutive failure.
	// Default: 10m.
	Max Duration `yaml:"max"`
}

// On reports whether retries are enabled (they are, unless turned off).
func (r RetryConfig) On() bool { return r.Enabled == nil || *r.Enabled }

type Config struct {
	Watch    WatchConfig `yaml:"watch"`
	Debounce Duration    `yaml:"debounce"`
	Build    BuildConfig `yaml:"build"`
	Run      RunConfig   `yaml:"run"`
	Retry    RetryConfig `yaml:"retry"`
}

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		for _, name := range DefaultConfigNames {
			if _, err := os.Stat(name); err == nil {
				path = name
				break
			}
		}
		if path == "" {
			return nil, fmt.Errorf("no config file found (looked for %s)", strings.Join(DefaultConfigNames, ", "))
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if len(c.Watch.Paths) == 0 {
		c.Watch.Paths = []string{"."}
	}
	// nil means "not configured" and takes the defaults; an explicit
	// "extensions: []" is a real choice — every file triggers — and is
	// left alone.
	if c.Watch.Extensions == nil {
		c.Watch.Extensions = DefaultExtensions
		c.Watch.extDefaulted = true
	}
	if c.Debounce == 0 {
		c.Debounce = Duration(300 * time.Millisecond)
	}
	if c.Build.Grace == 0 {
		c.Build.Grace = Duration(2 * time.Second)
	}
	if c.Run.Grace == 0 {
		c.Run.Grace = Duration(10 * time.Second)
	}
	if c.Run.StopSignal == "" {
		c.Run.StopSignal = "TERM"
	}
	if c.Retry.Initial == 0 {
		c.Retry.Initial = Duration(15 * time.Second)
	}
	if c.Retry.Max == 0 {
		c.Retry.Max = Duration(10 * time.Minute)
	}
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Build.Command) == "" {
		return fmt.Errorf("build.command is required")
	}
	if _, err := ParseSignal(c.Run.StopSignal); err != nil {
		return fmt.Errorf("run.stop_signal: %w", err)
	}
	if c.Retry.Initial < 0 || c.Retry.Max < 0 {
		return fmt.Errorf("retry delays must not be negative")
	}
	if c.Retry.Max < c.Retry.Initial {
		return fmt.Errorf("retry.max (%s) is less than retry.initial (%s)", c.Retry.Max.D(), c.Retry.Initial.D())
	}
	for _, g := range append(append([]string{}, c.Watch.Include...), c.Watch.Exclude...) {
		if _, err := filepath.Match(g, "x"); err != nil {
			return fmt.Errorf("invalid glob %q: %w", g, err)
		}
	}
	return nil
}

// Matches reports whether a change to path (relative to the working
// directory) should trigger a build.
func (c *Config) Matches(rel string) bool {
	base := filepath.Base(rel)

	for _, g := range c.Watch.Exclude {
		if matchGlob(g, base) || matchGlob(g, rel) {
			return false
		}
	}
	for _, g := range c.Watch.Include {
		if matchGlob(g, base) || matchGlob(g, rel) {
			return true
		}
	}
	if len(c.Watch.Extensions) == 0 {
		return true
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	for _, e := range c.Watch.Extensions {
		if strings.EqualFold(ext, strings.TrimPrefix(e, ".")) {
			return true
		}
	}
	return false
}

// ExcludesDir reports whether the directory at rel (relative to the working
// directory) should not be watched. Matches on base name or relative path.
func (c *Config) ExcludesDir(rel string) bool {
	base := filepath.Base(rel)
	if base != "." && strings.HasPrefix(base, ".") {
		return true
	}
	for _, d := range c.Watch.ExcludeDirs {
		d = filepath.Clean(d)
		if d == base || d == rel {
			return true
		}
	}
	return false
}

func matchGlob(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}
