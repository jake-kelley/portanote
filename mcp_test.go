package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestMCP(t *testing.T) http.Handler {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newMCP(store)
}

func mcpPost(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// mcpResult posts a request and decodes the JSON-RPC result into out.
func mcpResult(t *testing.T, h http.Handler, body string, out any) {
	t.Helper()
	rec := mcpPost(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response %s: %v", rec.Body.String(), err)
	}
	if resp.Error != nil {
		t.Fatalf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		t.Fatalf("bad result %s: %v", resp.Result, err)
	}
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// callTool invokes tools/call and unmarshals the text content into out (unless nil).
func callTool(t *testing.T, h http.Handler, name, args string, out any) toolResult {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	var res toolResult
	mcpResult(t, h, body, &res)
	if out != nil {
		if res.IsError {
			t.Fatalf("tool %s errored: %s", name, res.Content[0].Text)
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), out); err != nil {
			t.Fatalf("tool %s: bad content %q: %v", name, res.Content[0].Text, err)
		}
	}
	return res
}

func TestMCPInitialize(t *testing.T) {
	h := newTestMCP(t)
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools *struct{} `json:"tools"`
		} `json:"capabilities"`
	}
	mcpResult(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		&res)
	if res.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want 2025-06-18", res.ProtocolVersion)
	}
	if res.ServerInfo.Name != "portanote" || res.ServerInfo.Version != version {
		t.Errorf("serverInfo = %+v", res.ServerInfo)
	}
	if res.Capabilities.Tools == nil {
		t.Error("capabilities.tools missing")
	}

	// an unknown client version gets our latest instead of an echo
	mcpResult(t, h,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		&res)
	if res.ProtocolVersion != mcpLatestProtocol {
		t.Errorf("protocolVersion = %q, want %s", res.ProtocolVersion, mcpLatestProtocol)
	}
}

func TestMCPNotificationGets202(t *testing.T) {
	h := newTestMCP(t)
	rec := mcpPost(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body, got %s", rec.Body.String())
	}
}

func TestMCPPing(t *testing.T) {
	h := newTestMCP(t)
	var res map[string]any
	mcpResult(t, h, `{"jsonrpc":"2.0","id":7,"method":"ping"}`, &res)
	if len(res) != 0 {
		t.Errorf("ping result = %v, want {}", res)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	h := newTestMCP(t)
	rec := mcpPost(t, h, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	var resp struct {
		Error *rpcError `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("want -32601 method-not-found, got %s", rec.Body.String())
	}
}

func TestMCPToolsList(t *testing.T) {
	h := newTestMCP(t)
	var res struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	mcpResult(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, &res)
	if len(res.Tools) != len(mcpTools) {
		t.Fatalf("got %d tools, want %d", len(res.Tools), len(mcpTools))
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		if tool.Description == "" || tool.InputSchema["type"] != "object" {
			t.Errorf("tool %s: missing description or bad schema", tool.Name)
		}
	}
	for _, want := range []string{"search_notes", "list_notes", "read_note", "create_note", "update_note"} {
		if !seen[want] {
			t.Errorf("tool %s missing from tools/list", want)
		}
	}
}

func TestMCPNoteRoundTrip(t *testing.T) {
	h := newTestMCP(t)

	var created Note
	callTool(t, h, "create_note",
		`{"title":"MCP Round Trip","body":"hello from the protocol","folder":"Tests/MCP","tags":["mcp","zz-test"]}`,
		&created)
	if created.ID == "" || created.Body != "hello from the protocol" || created.Folder != "Tests/MCP" {
		t.Fatalf("created = %+v", created)
	}

	var read struct {
		Note      Note       `json:"note"`
		Backlinks []ListItem `json:"backlinks"`
	}
	callTool(t, h, "read_note", `{"id":"`+created.ID+`"}`, &read)
	if read.Note.Title != "MCP Round Trip" || len(read.Note.Tags) != 2 {
		t.Fatalf("read = %+v", read.Note)
	}

	var results []SearchResult
	callTool(t, h, "search_notes", `{"query":"protocol"}`, &results)
	if len(results) == 0 || results[0].ID != created.ID {
		t.Fatalf("search did not find the note: %+v", results)
	}

	var listed []ListItem
	callTool(t, h, "list_notes", `{"folder":"Tests"}`, &listed)
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list_notes folder filter: %+v", listed)
	}
	callTool(t, h, "list_notes", `{"tag":"MCP"}`, &listed) // tag match is case-insensitive
	if len(listed) != 1 {
		t.Fatalf("list_notes tag filter: %+v", listed)
	}

	var updated Note
	callTool(t, h, "update_note", `{"id":"`+created.ID+`","body":"edited body","trashed":true}`, &updated)
	if updated.Body != "edited body" || !updated.Trashed {
		t.Fatalf("updated = %+v", updated)
	}
	callTool(t, h, "list_notes", `{}`, &listed)
	for _, it := range listed {
		if it.ID == created.ID {
			t.Error("trashed note still listed without include_trashed")
		}
	}

	var folders []FolderInfo
	callTool(t, h, "list_folders", `{}`, &folders)
	foundFolder := false
	for _, f := range folders {
		if f.Name == "Tests/MCP" {
			foundFolder = true
		}
	}
	if !foundFolder {
		t.Errorf("folder created via create_note missing: %+v", folders)
	}

	// unknown id surfaces as a tool error, not a protocol error
	res := callTool(t, h, "read_note", `{"id":"nope"}`, nil)
	if !res.IsError {
		t.Error("read_note on unknown id should set isError")
	}
}

func TestMCPRenameFolder(t *testing.T) {
	h := newTestMCP(t)

	var created Note
	callTool(t, h, "create_note", `{"title":"In Old Folder","folder":"Work/Old"}`, &created)

	var folders []FolderInfo
	callTool(t, h, "rename_folder", `{"from":"Work/Old","to":"Work/New"}`, &folders)
	names := map[string]bool{}
	for _, f := range folders {
		names[f.Name] = true
	}
	if !names["Work/New"] || names["Work/Old"] {
		t.Fatalf("folders after rename: %+v", folders)
	}

	var read struct {
		Note Note `json:"note"`
	}
	callTool(t, h, "read_note", `{"id":"`+created.ID+`"}`, &read)
	if read.Note.Folder != "Work/New" {
		t.Fatalf("note folder = %q, want Work/New", read.Note.Folder)
	}

	// renaming a folder that doesn't exist surfaces as a tool error
	res := callTool(t, h, "rename_folder", `{"from":"Nope","to":"Other"}`, nil)
	if !res.IsError {
		t.Error("rename of missing folder should set isError")
	}
	// missing arguments too
	res = callTool(t, h, "rename_folder", `{"from":"Work/New"}`, nil)
	if !res.IsError {
		t.Error("rename without to should set isError")
	}
}

func TestMCPTaskTools(t *testing.T) {
	h := newTestMCP(t)

	var task Task
	callTool(t, h, "add_task", `{"text":"ship the MCP server"}`, &task)
	if task.ID == "" || task.Done {
		t.Fatalf("task = %+v", task)
	}
	callTool(t, h, "update_task", `{"id":"`+task.ID+`","done":true}`, &task)
	if !task.Done {
		t.Fatalf("task not marked done: %+v", task)
	}
	var tasks []Task
	callTool(t, h, "list_tasks", `{}`, &tasks)
	if len(tasks) != 1 || !tasks[0].Done {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestMCPOriginGuard(t *testing.T) {
	h := newTestMCP(t)
	cases := []struct {
		origin string
		want   int
	}{
		{"http://evil.example", http.StatusForbidden}, // DNS-rebinding attempt
		{"http://localhost:8737", http.StatusOK},
		{"http://127.0.0.1:8737", http.StatusOK},
		{"", http.StatusOK}, // native MCP clients send no Origin
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("origin %q: status %d, want %d", c.origin, rec.Code, c.want)
		}
	}
}

func TestMCPMethodNotAllowed(t *testing.T) {
	h := newTestMCP(t)
	for _, method := range []string{"GET", "DELETE"} {
		req := httptest.NewRequest(method, "/mcp", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /mcp: status %d, want 405", method, rec.Code)
		}
	}
}

func TestMCPBatch(t *testing.T) {
	h := newTestMCP(t)
	rec := mcpPost(t, h,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resps []struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resps); err != nil {
		t.Fatalf("batch response not an array: %s", rec.Body.String())
	}
	if len(resps) != 2 { // the notification produces no response
		t.Errorf("got %d responses, want 2", len(resps))
	}
}
