package main

import (
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestNoticesListEveryCompiledModule fails when a Go module linked into the release
// binary (any of the six targets) is missing from THIRD_PARTY_NOTICES.md, is listed
// with another version, or is listed without licence text. Fix: run
// scripts/gen-third-party-notices.sh and commit the result.
//
// Dependency sets do not depend on the machine running the test (GOOS/GOARCH are set
// explicitly), so this runs on Linux only to keep the Windows/macOS jobs quick.
func TestNoticesListEveryCompiledModule(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checked on Linux only; the result does not depend on the host OS")
	}
	if testing.Short() {
		t.Skip("runs go list six times")
	}
	sets, _, err := modules("../..")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for m := range sets {
		want[m.String()] = true
	}
	b, err := os.ReadFile("../../THIRD_PARTY_NOTICES.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	heading := regexp.MustCompile(`(?m)^### (\S+) (v\S+)$`)
	got := map[string]bool{}
	var starts []int
	var names []string
	for _, m := range heading.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[2]:m[3]] + " " + text[m[4]:m[5]]
		got[name] = true
		starts = append(starts, m[0])
		names = append(names, name)
	}
	var problems []string
	for m := range want {
		if !got[m] {
			problems = append(problems, "missing from THIRD_PARTY_NOTICES.md: "+m)
		}
	}
	for m := range got {
		if !want[m] {
			problems = append(problems, "listed but not compiled in (stale): "+m)
		}
	}
	// Every section carries its licence text.
	for i, start := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		section := text[start:end]
		if !strings.Contains(section, "- License: ") || !strings.Contains(section, "````text\n") || len(section) < 400 {
			problems = append(problems, "section without licence text: "+names[i])
		}
	}
	for _, must := range []string{"## AGDownloader", "Copyright (c) 2024 Eduardo Alarcón", "## Go standard library and runtime"} {
		if !strings.Contains(text, must) {
			problems = append(problems, "missing "+must)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("THIRD_PARTY_NOTICES.md is out of date; run scripts/gen-third-party-notices.sh\n  %s", strings.Join(problems, "\n  "))
	}
}
