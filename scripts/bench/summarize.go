//go:build ignore

// Command summarize converts the benchmark logs to a CSV, using nearest-rank p95.
package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var benchmark = regexp.MustCompile(`^(Benchmark\S+?)-\d+\s+\d+\s+([\d.]+) ns/op\s+(?:[\d.]+ MB/s\s+)?(\d+) B/op\s+(\d+) allocs/op$`)

type observations struct {
	ns, allocations, bytesUsed []float64
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("provide the results directory")
	}
	if err := summarize(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}

func summarize(directory string) error {
	rows := [][]string{{
		"commit", "label", "scenario", "unit", "median", "p95",
		"allocs_per_op", "bytes_per_op", "runs",
	}}
	var mainHash string
	for _, label := range []string{"before-refactor", "refactor", "main", "optimized"} {
		prefix := filepath.Join(directory, label)
		commit, err := os.ReadFile(prefix + ".commit")
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		scenarios, hash, err := readSamples(prefix)
		if err != nil {
			return err
		}
		if label == "optimized" && mainHash != "" && mainHash != hash {
			return fmt.Errorf("tools/list hash for %s is %s, expected %s", label, hash, mainHash)
		}
		if label == "main" {
			mainHash = hash
		}
		fmt.Printf("%s tools/list sha256 %s\n", label, hash)
		var names []string
		for name := range scenarios {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			sample := scenarios[name]
			slices.Sort(sample.ns)
			count := len(sample.ns)
			p95 := sample.ns[(95*count+99)/100-1]
			allocations, bytesUsed := "", ""
			if len(sample.allocations) > 0 {
				allocations = strconv.FormatFloat(median(sample.allocations), 'f', -1, 64)
				bytesUsed = strconv.FormatFloat(median(sample.bytesUsed), 'f', -1, 64)
			}
			rows = append(rows, []string{
				strings.TrimSpace(string(commit)), label, name, "us",
				fmt.Sprintf("%.3f", median(sample.ns)/1000), fmt.Sprintf("%.3f", p95/1000),
				allocations, bytesUsed, strconv.Itoa(count),
			})
		}
	}
	output, err := os.Create(filepath.Join(directory, "results.csv"))
	if err != nil {
		return err
	}
	defer output.Close()
	return csv.NewWriter(output).WriteAll(rows)
}

func readSamples(prefix string) (map[string]*observations, string, error) {
	scenarios := map[string]*observations{}
	get := func(name string) *observations {
		if scenarios[name] == nil {
			scenarios[name] = &observations{}
		}
		return scenarios[name]
	}
	for _, group := range []string{"mcp", "store"} {
		raw, err := os.ReadFile(prefix + "." + group + ".txt")
		if err != nil {
			return nil, "", err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if fields := benchmark.FindStringSubmatch(strings.TrimSpace(line)); fields != nil {
				sample := get(fields[1])
				ns, timeErr := strconv.ParseFloat(fields[2], 64)
				bytesUsed, bytesErr := strconv.ParseFloat(fields[3], 64)
				allocations, allocErr := strconv.ParseFloat(fields[4], 64)
				if err := errors.Join(timeErr, bytesErr, allocErr); err != nil {
					return nil, "", err
				}
				sample.ns = append(sample.ns, ns)
				sample.bytesUsed = append(sample.bytesUsed, bytesUsed)
				sample.allocations = append(sample.allocations, allocations)
			}
		}
	}
	raw, err := os.ReadFile(prefix + ".process.tsv")
	if err != nil {
		return nil, "", err
	}
	_, lines, _ := strings.Cut(string(raw), "\n")
	var hash string
	for _, line := range strings.Split(strings.TrimSuffix(lines, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, "", fmt.Errorf("invalid process sample %q", line)
		}
		ns, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, "", err
		}
		sample := get(fields[0])
		sample.ns = append(sample.ns, ns)
		if fields[2] != "" {
			if hash != "" && hash != fields[2] {
				return nil, "", fmt.Errorf("tools/list response varied within %s", prefix)
			}
			hash = fields[2]
		}
	}
	if hash == "" {
		return nil, "", fmt.Errorf("missing tools/list hash for %s", prefix)
	}
	return scenarios, hash, nil
}

func median(values []float64) float64 {
	slices.Sort(values)
	count := len(values)
	return (values[(count-1)/2] + values[count/2]) / 2
}
