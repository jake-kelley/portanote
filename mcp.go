package main

// mcp.go — a Model Context Protocol (MCP) server over the Streamable HTTP
// transport, mounted at /mcp on the same listener as the UI. Hand-rolled on
// the stdlib (JSON-RPC 2.0) so the binary stays dependency-free.
//
// Stateless by design: no sessions (no Mcp-Session-Id), no server-initiated
// SSE stream (GET returns 405, which the spec permits). Every POST carries a
// single JSON-RPC message or a batch and gets a plain application/json reply.
// Spec: https://modelcontextprotocol.io (revision 2025-06-18).

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const mcpLatestProtocol = "2025-06-18"

// protocol revisions we can speak; initialize echoes the client's if known
var mcpProtocols = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

const mcpInstructions = "Portanote is a local Markdown notes app. Notes carry a stable `id` " +
	"returned by search_notes / list_notes / create_note — use that id with read_note and " +
	"update_note. Note bodies are GitHub-Flavored Markdown; [[Note Title]] wiki-links connect " +
	"notes. Setting trashed=true in update_note moves a note to the trash (recoverable in the " +
	"UI); permanent deletion is deliberately not exposed over MCP."

type mcpServer struct {
	store *Store
}

func newMCP(store *Store) http.Handler {
	return &mcpServer{store: store}
}

func (m *mcpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// DNS-rebinding defense: browsers always send Origin on cross-origin POSTs,
	// native MCP clients send none. Only loopback origins may pass.
	if o := r.Header.Get("Origin"); o != "" && !loopbackOrigin(o) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusOK, rpcErrorResp(nil, -32700, "could not read request body"))
		return
	}

	// a JSON-RPC batch (pre-2025-06-18 clients may still send them)
	if t := bytes.TrimLeft(body, " \t\r\n"); len(t) > 0 && t[0] == '[' {
		var msgs []json.RawMessage
		if json.Unmarshal(body, &msgs) != nil || len(msgs) == 0 {
			writeJSON(w, http.StatusOK, rpcErrorResp(nil, -32700, "parse error"))
			return
		}
		out := []any{}
		for _, raw := range msgs {
			if resp := m.handleMessage(raw); resp != nil {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted) // batch of notifications
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	resp := m.handleMessage(body)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted) // notification: no body
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// loopbackOrigin reports whether origin points at this machine (localhost/127.x/::1).
func loopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---------------------------------------------------------------- JSON-RPC

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcResult(id json.RawMessage, result any) any {
	return struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{"2.0", id, result}
}

func rpcErrorResp(id json.RawMessage, code int, msg string) any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcError        `json:"error"`
	}{"2.0", id, rpcError{code, msg}}
}

// handleMessage processes one JSON-RPC message; nil means "no response"
// (notifications).
func (m *mcpServer) handleMessage(raw json.RawMessage) any {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcErrorResp(nil, -32700, "parse error")
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if req.JSONRPC != "2.0" || req.Method == "" {
		if isNotification {
			return nil
		}
		return rpcErrorResp(req.ID, -32600, "invalid request")
	}
	if isNotification {
		return nil // notifications/initialized, notifications/cancelled, …
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		ver := mcpLatestProtocol
		if mcpProtocols[p.ProtocolVersion] {
			ver = p.ProtocolVersion
		}
		return rpcResult(req.ID, map[string]any{
			"protocolVersion": ver,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "portanote",
				"title":   "Portanote",
				"version": version,
			},
			"instructions": mcpInstructions,
		})

	case "ping":
		return rpcResult(req.ID, map[string]any{})

	case "tools/list":
		defs := make([]map[string]any, 0, len(mcpTools))
		for _, t := range mcpTools {
			defs = append(defs, map[string]any{
				"name":        t.name,
				"description": t.description,
				"inputSchema": t.inputSchema,
			})
		}
		return rpcResult(req.ID, map[string]any{"tools": defs})

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return rpcErrorResp(req.ID, -32602, "invalid params: tool name required")
		}
		for _, t := range mcpTools {
			if t.name != p.Name {
				continue
			}
			result, err := t.run(m.store, p.Arguments)
			if err != nil {
				return rpcResult(req.ID, toolText(err.Error(), true))
			}
			b, _ := json.MarshalIndent(result, "", "  ")
			return rpcResult(req.ID, toolText(string(b), false))
		}
		return rpcErrorResp(req.ID, -32602, "unknown tool: "+p.Name)

	default:
		return rpcErrorResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func toolText(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

// ---------------------------------------------------------------- tools

type mcpTool struct {
	name        string
	description string
	inputSchema map[string]any
	run         func(s *Store, args json.RawMessage) (any, error)
}

func schema(props map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func strArrayProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

var mcpTools = []mcpTool{
	{
		name: "search_notes",
		description: "Full-text search across all notes (BM25 over title, tags, and body; the last " +
			"query word also matches as a prefix). Call this when looking for notes about a topic. " +
			"Returns matches with id, metadata, a snippet, and a relevance score.",
		inputSchema: schema(map[string]any{
			"query":           prop("string", "Search terms."),
			"include_trashed": prop("boolean", "Also search trashed notes (default false)."),
			"limit":           prop("integer", "Maximum results to return (default 20, max 200)."),
		}, "query"),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				Query          string `json:"query"`
				IncludeTrashed bool   `json:"include_trashed"`
				Limit          int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, errors.New("invalid arguments: " + err.Error())
			}
			if strings.TrimSpace(a.Query) == "" {
				return nil, errors.New("query is required")
			}
			if a.Limit <= 0 {
				a.Limit = 20
			} else if a.Limit > 200 {
				a.Limit = 200
			}
			results := s.Search(a.Query, a.IncludeTrashed)
			if len(results) > a.Limit {
				results = results[:a.Limit]
			}
			return results, nil
		},
	},
	{
		name: "list_notes",
		description: "List notes (newest-updated first) with metadata and a short snippet, optionally " +
			"filtered by folder (includes subfolders), tag, or starred. Use search_notes instead when " +
			"looking for content about a topic.",
		inputSchema: schema(map[string]any{
			"folder":          prop("string", "Only notes in this folder or its subfolders, e.g. \"Work/Runbooks\"."),
			"tag":             prop("string", "Only notes carrying this tag (case-insensitive)."),
			"starred":         prop("boolean", "If true, only starred notes."),
			"include_trashed": prop("boolean", "Also include trashed notes (default false)."),
			"limit":           prop("integer", "Maximum results to return (default 50, max 500)."),
		}),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				Folder         string `json:"folder"`
				Tag            string `json:"tag"`
				Starred        bool   `json:"starred"`
				IncludeTrashed bool   `json:"include_trashed"`
				Limit          int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, errors.New("invalid arguments: " + err.Error())
			}
			if a.Limit <= 0 {
				a.Limit = 50
			} else if a.Limit > 500 {
				a.Limit = 500
			}
			out := []ListItem{}
			for _, it := range s.List() {
				if it.Trashed && !a.IncludeTrashed {
					continue
				}
				if a.Starred && !it.Starred {
					continue
				}
				if a.Folder != "" && !underFolder(it.Folder, cleanFolderPath(a.Folder)) {
					continue
				}
				if a.Tag != "" && !hasTag(it.Tags, a.Tag) {
					continue
				}
				out = append(out, it)
				if len(out) >= a.Limit {
					break
				}
			}
			return out, nil
		},
	},
	{
		name: "read_note",
		description: "Read one note in full — metadata plus the complete Markdown body — along with " +
			"the notes that [[wiki-link]] to it (backlinks). Get the id from search_notes or list_notes.",
		inputSchema: schema(map[string]any{
			"id": prop("string", "The note's id."),
		}, "id"),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
				return nil, errors.New("id is required")
			}
			n, err := s.Get(a.ID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"note": n, "backlinks": s.Backlinks(a.ID)}, nil
		},
	},
	{
		name: "create_note",
		description: "Create a new note. The body is GitHub-Flavored Markdown; use [[Note Title]] to " +
			"link to other notes. Folders are \"/\"-separated paths and are created on demand.",
		inputSchema: schema(map[string]any{
			"title":  prop("string", "The note title."),
			"body":   prop("string", "Markdown body (optional)."),
			"folder": prop("string", "Folder path such as \"Work/Runbooks\" (optional)."),
			"tags":   strArrayProp("Tags to apply (optional)."),
		}, "title"),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				Title  string   `json:"title"`
				Body   string   `json:"body"`
				Folder string   `json:"folder"`
				Tags   []string `json:"tags"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, errors.New("invalid arguments: " + err.Error())
			}
			if strings.TrimSpace(a.Title) == "" {
				return nil, errors.New("title is required")
			}
			n, err := s.Create(a.Title)
			if err != nil {
				return nil, err
			}
			if a.Body != "" || a.Folder != "" || len(a.Tags) > 0 {
				req := UpdateReq{}
				if a.Body != "" {
					req.Body = &a.Body
				}
				if a.Folder != "" {
					req.Folder = &a.Folder
				}
				if len(a.Tags) > 0 {
					req.Tags = &a.Tags
				}
				if n, err = s.Update(n.ID, req); err != nil {
					return nil, err
				}
			}
			return n, nil
		},
	},
	{
		name: "update_note",
		description: "Update a note. Only the fields provided are changed; body replaces the whole " +
			"Markdown body (read_note first to edit part of it). Set trashed=true to move a note to " +
			"the trash, trashed=false to restore it. There is no permanent-delete tool.",
		inputSchema: schema(map[string]any{
			"id":      prop("string", "The note's id."),
			"title":   prop("string", "New title (optional)."),
			"body":    prop("string", "New Markdown body, replacing the current one (optional)."),
			"folder":  prop("string", "New folder path; \"\" moves the note out of any folder (optional)."),
			"tags":    strArrayProp("Full replacement tag list (optional)."),
			"starred": prop("boolean", "Star or unstar the note (optional)."),
			"trashed": prop("boolean", "Move to / restore from the trash (optional)."),
		}, "id"),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				ID string `json:"id"`
				UpdateReq
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, errors.New("invalid arguments: " + err.Error())
			}
			if a.ID == "" {
				return nil, errors.New("id is required")
			}
			return s.Update(a.ID, a.UpdateReq)
		},
	},
	{
		name:        "list_folders",
		description: "List all folders with their live note counts (trashed notes excluded).",
		inputSchema: schema(map[string]any{}),
		run: func(s *Store, args json.RawMessage) (any, error) {
			return s.Folders(), nil
		},
	},
	{
		name: "rescan_notes",
		description: "Rescan the notes directory and rebuild the index — picks up Markdown files and " +
			"folders added, edited, or removed outside Portanote (file explorer, git, another editor). " +
			"Returns counts of added/changed/removed notes.",
		inputSchema: schema(map[string]any{}),
		run: func(s *Store, args json.RawMessage) (any, error) {
			return s.Rescan(), nil
		},
	},
	{
		name:        "list_tags",
		description: "List every tag in use with the number of (non-trashed) notes carrying it.",
		inputSchema: schema(map[string]any{}),
		run: func(s *Store, args json.RawMessage) (any, error) {
			counts := map[string]int{}
			names := map[string]string{} // lowercase -> first-seen casing
			for _, it := range s.List() {
				if it.Trashed {
					continue
				}
				for _, t := range it.Tags {
					k := strings.ToLower(t)
					if _, ok := names[k]; !ok {
						names[k] = t
					}
					counts[k]++
				}
			}
			type tagInfo struct {
				Name  string `json:"name"`
				Count int    `json:"count"`
			}
			out := make([]tagInfo, 0, len(counts))
			for k, c := range counts {
				out = append(out, tagInfo{Name: names[k], Count: c})
			}
			sort.Slice(out, func(i, j int) bool {
				if out[i].Count != out[j].Count {
					return out[i].Count > out[j].Count
				}
				return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
			})
			return out, nil
		},
	},
	{
		name:        "list_tasks",
		description: "List the standalone to-do tasks in display order, including completed ones.",
		inputSchema: schema(map[string]any{}),
		run: func(s *Store, args json.RawMessage) (any, error) {
			return s.Tasks(), nil
		},
	},
	{
		name:        "add_task",
		description: "Add a to-do task, optionally linked to the note it came from.",
		inputSchema: schema(map[string]any{
			"text":    prop("string", "The task text."),
			"note_id": prop("string", "Id of a note to link the task back to (optional)."),
		}, "text"),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				Text   string `json:"text"`
				NoteID string `json:"note_id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, errors.New("invalid arguments: " + err.Error())
			}
			return s.CreateTask(a.Text, a.NoteID)
		},
	},
	{
		name:        "update_task",
		description: "Update a to-do task: mark it done/undone and/or change its text.",
		inputSchema: schema(map[string]any{
			"id":   prop("string", "The task's id."),
			"done": prop("boolean", "Mark done (true) or not done (false) (optional)."),
			"text": prop("string", "New task text (optional)."),
		}, "id"),
		run: func(s *Store, args json.RawMessage) (any, error) {
			var a struct {
				ID   string  `json:"id"`
				Done *bool   `json:"done"`
				Text *string `json:"text"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, errors.New("invalid arguments: " + err.Error())
			}
			if a.ID == "" {
				return nil, errors.New("id is required")
			}
			t, err := s.UpdateTask(a.ID, a.Done, a.Text)
			if errors.Is(err, ErrNotFound) {
				return nil, errors.New("task not found")
			}
			return t, err
		},
	},
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}
