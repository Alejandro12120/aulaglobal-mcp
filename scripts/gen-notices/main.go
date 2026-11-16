// Command gen-notices writes THIRD_PARTY_NOTICES.md: the licence text of every
// Go module compiled into the release binary, for all six release targets, plus
// the Go standard library. Run it through scripts/gen-third-party-notices.sh.
//
// The licence of each module is read from the module cache and checked against
// the SPDX identifier recorded in spdxOf; a module that is not listed there, or
// whose LICENSE does not contain the expected wording, stops the run so that a
// human reads the new licence before it is shipped.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed preamble.md
var preamble string

type module struct{ Path, Version string }

func (m module) String() string { return m.Path + " " + m.Version }

// targets are the release builds (see .goreleaser.yaml).
var targets = func() []string {
	var t []string
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t = append(t, goos+"/"+arch)
		}
	}
	return t
}()

// spdxOf records the licence of each module, verified by hand against its LICENSE
// file. " AND " means the file carries several licences that all apply.
var spdxOf = map[string]string{
	"github.com/google/jsonschema-go":        "MIT",
	"github.com/ledongthuc/pdf":              "BSD-3-Clause",
	"github.com/modelcontextprotocol/go-sdk": "Apache-2.0 AND MIT",
	"github.com/pelletier/go-toml/v2":        "MIT",
	"github.com/segmentio/asm":               "MIT",
	"github.com/segmentio/encoding":          "MIT",
	"github.com/yosida95/uritemplate/v3":     "BSD-3-Clause",
	"golang.org/x/oauth2":                    "BSD-3-Clause",
	"golang.org/x/sys":                       "BSD-3-Clause",
	"golang.org/x/term":                      "BSD-3-Clause",
}

// markers are phrases every file under that SPDX identifier must contain.
var markers = map[string][]string{
	"MIT":          {"Permission is hereby granted, free of charge, to any person obtaining a copy"},
	"BSD-3-Clause": {"Redistributions of source code must retain", "Redistributions in binary form must reproduce", "Neither the name of"},
	"Apache-2.0":   {"Apache License", "Version 2.0, January 2004"},
}

// modules returns the modules linked into the release binary for each target
// (go list -deps of the main package, which excludes test-only dependencies),
// with the directory of each module in the module cache.
func modules(root string) (map[module]map[string]bool, map[module]string, error) {
	sets := map[module]map[string]bool{}
	dirs := map[module]string{}
	for _, target := range targets {
		goos, goarch, _ := strings.Cut(target, "/")
		cmd := exec.Command("go", "list", "-deps", "-f",
			"{{if .Module}}{{if .Module.Version}}{{.Module.Path}} {{.Module.Version}} {{.Module.Dir}}{{end}}{{end}}", "./cmd/aulaglobal-mcp")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=-mod=readonly")
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				err = fmt.Errorf("%w: %s", err, ee.Stderr)
			}
			return nil, nil, fmt.Errorf("go list for %s: %w", target, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			mod := module{fields[0], fields[1]}
			if sets[mod] == nil {
				sets[mod] = map[string]bool{}
			}
			sets[mod][target] = true
			dirs[mod] = strings.Join(fields[2:], " ")
		}
	}
	return sets, dirs, nil
}

func isLegalFile(name string) bool {
	upper := strings.ToUpper(name)
	for _, candidate := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS"} {
		if strings.HasPrefix(upper, candidate) {
			return true
		}
	}
	return false
}

type legalFile struct{ Name, Text string }

func legalFiles(dir string) ([]legalFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []legalFile
	for _, entry := range entries {
		if entry.IsDir() || !isLegalFile(entry.Name()) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, legalFile{entry.Name(), strings.TrimSpace(strings.ReplaceAll(string(content), "\r\n", "\n"))})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// verify checks that the licence files back the recorded SPDX expression.
func verify(mod module, spdx string, files []legalFile) error {
	var license string
	for _, file := range files {
		if strings.HasPrefix(strings.ToUpper(file.Name), "LICEN") || strings.HasPrefix(strings.ToUpper(file.Name), "COPYING") {
			license += file.Text + "\n"
		}
	}
	if license == "" {
		return fmt.Errorf("%s: no LICENSE file found", mod.Path)
	}
	for _, id := range strings.Split(spdx, " AND ") {
		want, ok := markers[id]
		if !ok {
			return fmt.Errorf("%s: no verification markers for %s", mod.Path, id)
		}
		for _, phrase := range want {
			if !strings.Contains(license, phrase) {
				return fmt.Errorf("%s: LICENSE does not look like %s (missing %q); read it and update spdxOf", mod.Path, id, phrase)
			}
		}
	}
	return nil
}

func fence(text string) string {
	ticks := "````"
	for strings.Contains(text, ticks) {
		ticks += "`"
	}
	return ticks
}

func block(out *strings.Builder, title string, text string) {
	ticks := fence(text)
	fmt.Fprintf(out, "%s\n\n%stext\n%s\n%s\n\n", title, ticks, text, ticks)
}

func generate(root string) (string, error) {
	sets, dirs, err := modules(root)
	if err != nil {
		return "", err
	}
	var list []module
	for mod := range sets {
		list = append(list, mod)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })

	var out strings.Builder
	out.WriteString(preamble)
	if !strings.HasSuffix(preamble, "\n") {
		out.WriteString("\n")
	}

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return "", err
	}
	goversion, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return "", err
	}
	std, err := legalFiles(strings.TrimSpace(string(goroot)))
	if err != nil {
		return "", err
	}
	if len(std) == 0 {
		return "", fmt.Errorf("no licence found in GOROOT")
	}
	if err := verify(module{Path: "Go standard library"}, "BSD-3-Clause", std); err != nil {
		return "", err
	}
	fmt.Fprintf(&out, "\n## Go standard library and runtime\n\nStatically linked into every binary. License: BSD-3-Clause. Copyright 2009 The Go Authors. Source: https://go.dev/ (toolchain %s).\n\n", strings.TrimSpace(string(goversion)))
	for _, file := range std {
		block(&out, "**"+file.Name+"**", file.Text)
	}

	out.WriteString("\n## Go modules\n\nEvery module below is compiled into the release binaries (`go list -deps` of `./cmd/aulaglobal-mcp` with `CGO_ENABLED=0`, for linux, darwin and windows on amd64 and arm64). Test-only dependencies are not linked and not listed. Regenerate with `scripts/gen-third-party-notices.sh`; CI fails if this list and the build disagree.\n\n")
	for _, mod := range list {
		spdx, ok := spdxOf[mod.Path]
		if !ok {
			return "", fmt.Errorf("%s: licence not recorded in spdxOf; read its LICENSE, then add it", mod)
		}
		files, err := legalFiles(dirs[mod])
		if err != nil {
			return "", err
		}
		if err := verify(mod, spdx, files); err != nil {
			return "", err
		}
		var tg []string
		for _, t := range targets {
			if sets[mod][t] {
				tg = append(tg, t)
			}
		}
		compiled := "all six release targets"
		if len(tg) != len(targets) {
			compiled = strings.Join(tg, ", ")
		}
		fmt.Fprintf(&out, "### %s %s\n\n- License: %s\n- Source: https://pkg.go.dev/%s@%s\n- Compiled into: %s\n\n", mod.Path, mod.Version, spdx, mod.Path, mod.Version, compiled)
		for _, file := range files {
			block(&out, "**"+file.Name+"**", file.Text)
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n", nil
}

func main() {
	out := flag.String("o", "THIRD_PARTY_NOTICES.md", "output file")
	root := flag.String("C", ".", "module root")
	flag.Parse()
	text, err := generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-notices:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-notices:", err)
		os.Exit(1)
	}
}
