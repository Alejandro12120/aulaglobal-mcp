package mcpserver_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/mcpserver"
	"aulaglobal-mcp/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func benchmarkMoodle(b *testing.B) (*httptest.Server, *atomic.Int64) {
	b.Helper()
	var courseRequests atomic.Int64
	var server *httptest.Server
	contents := map[string][]byte{}
	files := map[string][]byte{
		"/small.txt": bytes.Repeat([]byte("s"), 4096),
		"/large.txt": bytes.Repeat([]byte("L"), 5<<20),
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if data, ok := files[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.FormValue("wsfunction") {
		case "core_webservice_get_site_info":
			fmt.Fprint(w, `{"fullname":"Benchmark","userid":1,"sitename":"Benchmark Moodle","release":"4.1"}`)
		case "core_course_get_enrolled_courses_by_timeline_classification":
			courseRequests.Add(1)
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Small course"},{"id":2,"fullname":"Large course"}]}`)
		case "core_course_get_contents":
			data, ok := contents[r.FormValue("courseid")]
			if !ok {
				http.Error(w, "unexpected course", http.StatusBadRequest)
				return
			}
			w.Write(data)
		case "mod_assign_get_assignments":
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"Small course","assignments":[{"id":1,"cmid":2,"name":"Exercise","intro":"<p>Objective</p>","duedate":1893456000,"allowsubmissionsfromdate":1890000000,"cutoffdate":1893500000,"gradingduedate":1893600000}]}]}`)
		case "mod_assign_get_submission_status":
			fmt.Fprint(w, `{"lastattempt":{"submission":{"status":"submitted","timemodified":1700000000},"gradingstatus":"graded"}}`)
		default:
			http.Error(w, "unexpected Moodle request", http.StatusBadRequest)
		}
	}))
	b.Cleanup(server.Close)
	for courseID, count := range map[string]int{"1": 20, "2": 1} {
		location := "/small.txt"
		if courseID == "2" {
			location = "/large.txt"
		}
		var entries []map[string]any
		for index := range count {
			entries = append(entries, map[string]any{
				"type": "file", "filename": fmt.Sprintf("file-%02d.txt", index), "filepath": "/",
				"fileurl": server.URL + location, "filesize": len(files[location]),
				"timemodified": 1700000000, "mimetype": "text/plain",
			})
		}
		data, err := json.Marshal([]any{map[string]any{
			"id": 1, "name": "Materials", "modules": []any{map[string]any{
				"id": 2, "name": "Documents", "contents": entries,
			}},
		}})
		if err != nil {
			b.Fatal(err)
		}
		contents[courseID] = data
	}
	return server, &courseRequests
}

func benchmarkStore(b *testing.B, path string) *store.Store {
	b.Helper()
	dataStore, err := store.Open(path, 256<<20, 4)
	if err != nil {
		b.Fatal(err)
	}
	return dataStore
}

func benchmarkConnect(b *testing.B, api *aulaglobal.Client, dataStore *store.Store) *mcp.ClientSession {
	b.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcpserver.New(api, dataStore, nil)
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		b.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "benchmark", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		b.Fatal(err)
	}
	return session
}

func benchmarkCall(b *testing.B, session *mcp.ClientSession, tool string, args map[string]any) map[string]any {
	b.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		b.Fatal(err)
	}
	if result.IsError {
		b.Fatalf("%s: %v", tool, result.Content)
	}
	return result.StructuredContent.(map[string]any)
}

func BenchmarkMCP(b *testing.B) {
	moodle, requests := benchmarkMoodle(b)
	for _, limiter := range []struct{ name, setting string }{
		{"default_4rps", ""},
		{"max_100rps", strconv.FormatFloat(aulaglobal.MaxMaxRPS, 'f', -1, 64)},
	} {
		b.Run(limiter.name, func(b *testing.B) {
			b.Setenv("AULAGLOBAL_MAX_RPS", limiter.setting)
			// Keep the production client's connection pool warm across fresh limiter
			// and listing-cache instances. Every endpoint still uses loopback TCP.
			transport := aulaglobal.New(moodle.URL, nil).HTTP.Transport
			newAPI := func() *aulaglobal.Client {
				api := aulaglobal.New(moodle.URL, aulaglobal.StaticToken{Value: "bench-fake"})
				api.HTTP.Transport = transport
				if setting := os.Getenv("AULAGLOBAL_MAX_RPS"); setting != "" {
					rate, err := aulaglobal.ParseMaxRPS(setting)
					if err != nil {
						b.Fatal(err)
					}
					if err = api.SetMaxRPS(rate); err != nil {
						b.Fatal(err)
					}
				}
				return api
			}
			b.Run("initialize_tools_list", func(b *testing.B) {
				api := newAPI()
				for b.Loop() {
					session := benchmarkConnect(b, api, nil)
					if list, err := session.ListTools(context.Background(), nil); err != nil || len(list.Tools) == 0 {
						b.Fatalf("tools/list: %v", err)
					}
					b.StopTimer()
					session.Close()
					b.StartTimer()
				}
			})
			for _, scenario := range []struct {
				name, tool string
				args       map[string]any
				fresh      bool
			}{
				{"auth_status", "auth_status", nil, true},
				{"list_courses_cold", "list_courses", nil, true},
				{"list_courses_warm", "list_courses", nil, false},
				{"list_course_files_cold", "list_course_files", map[string]any{"course_id": 1}, true},
				{"list_assignments_submission", "list_assignments", map[string]any{"course_ids": []int{1}, "status": "all", "include_submission_status": true}, true},
			} {
				b.Run(scenario.name, func(b *testing.B) {
					var session *mcp.ClientSession
					if !scenario.fresh {
						session = benchmarkConnect(b, newAPI(), nil)
						defer session.Close()
						benchmarkCall(b, session, scenario.tool, scenario.args)
					}
					startRequests := requests.Load()
					for b.Loop() {
						if scenario.fresh {
							b.StopTimer()
							session = benchmarkConnect(b, newAPI(), nil)
							b.StartTimer()
						}
						output := benchmarkCall(b, session, scenario.tool, scenario.args)
						if scenario.fresh {
							b.StopTimer()
							session.Close()
							if scenario.name == "list_assignments_submission" && !strings.Contains(fmt.Sprint(output["assignments"]), "submitted") {
								b.Fatal("fixture submission missing")
							}
							b.StartTimer()
						}
					}
					if scenario.name == "list_courses_warm" && requests.Load() != startRequests {
						b.Fatal("warm listing missed the cache")
					}
					if scenario.name == "list_courses_cold" && requests.Load()-startRequests != int64(b.N) {
						b.Fatal("cold listing did not reach Moodle")
					}
				})
			}
			for _, scenario := range []struct {
				name            string
				courseID, count int
			}{
				{"download_20_small", 1, 20},
				{"download_5MiB", 2, 1},
			} {
				b.Run(scenario.name, func(b *testing.B) {
					dir := b.TempDir()
					args := map[string]any{"course_id": scenario.courseID}
					for b.Loop() {
						b.StopTimer()
						dataStore := benchmarkStore(b, dir)
						api := newAPI()
						if limiter.setting != "" && scenario.count > 8 {
							// Raising the rate preserves the initial eight tokens.
							// Let the bucket accrue enough for the whole batch.
							time.Sleep(200 * time.Millisecond)
						}
						session := benchmarkConnect(b, api, dataStore)
						b.StartTimer()
						output := benchmarkCall(b, session, "download_files", args)
						b.StopTimer()
						results := output["results"].([]any)
						if len(results) != scenario.count {
							b.Fatalf("downloaded %d files, wanted %d", len(results), scenario.count)
						}
						for _, entry := range results {
							if result := entry.(map[string]any); result["status"] != "downloaded" {
								b.Fatalf("download failed: %v", result)
							}
						}
						session.Close()
						dataStore.Root.Close()
						if err := os.RemoveAll(dir); err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
					}
				})
			}
			b.Run("local", func(b *testing.B) {
				dataStore := benchmarkStore(b, b.TempDir())
				defer dataStore.Root.Close()
				benchmarkDocuments(b, dataStore.Root)
				session := benchmarkConnect(b, newAPI(), dataStore)
				defer session.Close()
				for _, ext := range []string{"txt", "html", "pdf", "docx"} {
					b.Run("read_"+ext+"_warm", func(b *testing.B) {
						args := map[string]any{"path": "sample." + ext}
						if result := benchmarkCall(b, session, "read_file", args); result["text"] == "" {
							b.Fatalf("empty fixture: %v", result)
						}
						for b.Loop() {
							benchmarkCall(b, session, "read_file", args)
						}
					})
				}
				b.Run("search_300_files_warm", func(b *testing.B) {
					dataStore := benchmarkStore(b, b.TempDir())
					defer dataStore.Root.Close()
					session := benchmarkConnect(b, newAPI(), dataStore)
					defer session.Close()
					if err := dataStore.Root.Mkdir("corpus", 0700); err != nil {
						b.Fatal(err)
					}
					data := bytes.Repeat([]byte("benchmark text for a full corpus scan.\n"), 2688)
					for index := range 300 {
						if err := dataStore.Root.WriteFile(fmt.Sprintf("corpus/file-%03d.txt", index), data, 0600); err != nil {
							b.Fatal(err)
						}
					}
					args := map[string]any{"query": "absent needle", "path_prefix": "corpus"}
					benchmarkCall(b, session, "search_files", args)
					for b.Loop() {
						benchmarkCall(b, session, "search_files", args)
					}
				})
			})
		})
	}
}

func benchmarkDocuments(b *testing.B, root *os.Root) {
	b.Helper()
	var docx bytes.Buffer
	archive := zip.NewWriter(&docx)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		b.Fatal(err)
	}
	if _, err = fmt.Fprint(entry, `<document><p><t>Benchmark text</t></p></document>`); err != nil {
		b.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		b.Fatal(err)
	}
	pdf, err := os.ReadFile(filepath.Join("..", "store", "testdata", "tiny.pdf"))
	if err != nil {
		b.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"sample.txt":  []byte("Benchmark text"),
		"sample.html": []byte("<p>Benchmark <b>text</b></p>"),
		"sample.pdf":  pdf,
		"sample.docx": docx.Bytes(),
	} {
		if err := root.WriteFile(name, content, 0600); err != nil {
			b.Fatal(err)
		}
	}
}
