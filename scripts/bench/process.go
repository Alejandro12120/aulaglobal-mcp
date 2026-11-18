//go:build ignore

// Command process measures the stdio executable against local files only.
// Run it with networking disabled. Its TSV output contains raw timings in ns.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	binary := flag.String("binary", "", "path to the server executable")
	runs := flag.Int("runs", 20, "number of fresh processes per scenario")
	flag.Parse()
	if *binary == "" || *runs < 1 {
		log.Fatal("provide -binary and a positive -runs")
	}
	if err := run(*binary, *runs); err != nil {
		log.Fatal(err)
	}
}

func run(binary string, runs int) error {
	fmt.Println("scenario\tns\ttools_list_sha256")
	for range runs {
		if err := sample(binary); err != nil {
			return err
		}
	}
	return nil
}

func sample(binary string) (err error) {
	root, err := os.MkdirTemp("", "mcp-process-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	home, data := filepath.Join(root, "home"), filepath.Join(root, "data")
	if err := os.Mkdir(home, 0700); err != nil {
		return err
	}
	if err := prepareData(data); err != nil {
		return err
	}
	env := []string{
		"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"AULAGLOBAL_DOWNLOAD_DIR=" + data, "AULAGLOBAL_TOKEN=bench-fake",
		"AULAGLOBAL_UPDATE_CHECK=0",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	version := exec.CommandContext(ctx, binary, "--version")
	version.Env = env
	start := time.Now()
	if output, err := version.Output(); err != nil || len(bytes.TrimSpace(output)) == 0 {
		return fmt.Errorf("--version failed: %v", err)
	}
	fmt.Printf("process/version\t%d\t\n", time.Since(start).Nanoseconds())

	cmd := exec.CommandContext(ctx, binary, "--transport", "stdio")
	cmd.Env = env
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer output.Close()
	client := rpc{input: input, output: bufio.NewReader(output)}
	start = time.Now()
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		input.Close()
		if err != nil {
			cmd.Process.Kill()
		}
		if waitErr := cmd.Wait(); err == nil {
			err = waitErr
		}
	}()

	if _, err = client.call(1, "initialize", json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"process-benchmark","version":"1"}}`)); err != nil {
		return err
	}
	fmt.Printf("process/initialize_from_start\t%d\t\n", time.Since(start).Nanoseconds())
	if err = client.notifyInitialized(); err != nil {
		return err
	}
	list, err := client.call(2, "tools/list", nil)
	if err != nil {
		return err
	}
	fmt.Printf("process/tools_list_from_start\t%d\t%x\n", time.Since(start).Nanoseconds(), sha256.Sum256(list))
	for index, tool := range []struct {
		name, scenario string
		args           json.RawMessage
	}{
		{"read_file", "read_file_cold", json.RawMessage(`{"path":"sample.txt"}`)},
		{"search_files", "search_files_300_cold", json.RawMessage(`{"query":"absent needle","path_prefix":"corpus"}`)},
		{"search_files", "search_files_300_warm", json.RawMessage(`{"query":"absent needle","path_prefix":"corpus"}`)},
	} {
		params := struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}{tool.name, tool.args}
		start = time.Now()
		result, err := client.call(index+3, "tools/call", params)
		elapsed := time.Since(start)
		if err != nil {
			return err
		}
		var payload struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		}
		if err := json.Unmarshal(result, &payload); err != nil || payload.IsError || len(payload.StructuredContent) == 0 {
			return fmt.Errorf("%s failed: %s", tool.name, result)
		}
		fmt.Printf("process/%s\t%d\t\n", tool.scenario, elapsed.Nanoseconds())
	}
	return nil
}

func prepareData(dir string) error {
	corpus := filepath.Join(dir, "corpus")
	if err := os.MkdirAll(corpus, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("Benchmark text"), 0600); err != nil {
		return err
	}
	content := bytes.Repeat([]byte("benchmark text for a full corpus scan.\n"), 2688)
	for index := range 300 {
		path := filepath.Join(corpus, fmt.Sprintf("file-%03d.txt", index))
		if err := os.WriteFile(path, content, 0600); err != nil {
			return err
		}
	}
	return nil
}

type rpc struct {
	input  io.Writer
	output *bufio.Reader
}

func (client rpc) notifyInitialized() error {
	_, err := fmt.Fprintln(client.input, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return err
}

func (client rpc) call(id int, method string, params any) (json.RawMessage, error) {
	request := struct {
		Version string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", id, method, params}
	if err := json.NewEncoder(client.input).Encode(request); err != nil {
		return nil, err
	}
	for {
		line, err := client.output.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("%s response: %w", method, err)
		}
		var response struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &response); err != nil {
			return nil, err
		}
		if response.ID == nil && response.Method == "notifications/tools/list_changed" {
			continue
		}
		if response.ID == nil || *response.ID != id || len(response.Error) != 0 || len(response.Result) == 0 {
			return nil, fmt.Errorf("%s: unexpected response %s", method, line)
		}
		return response.Result, nil
	}
}
