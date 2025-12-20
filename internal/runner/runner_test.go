package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Test that our runner executes the whole sample folder against a mock server.
func TestRunFolderSampledata(t *testing.T) {
	srv := suiteServer()
	defer srv.Close()

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	vars := map[string]string{"baseUrl": srv.URL, "graphqlUrl": srv.URL + "/graphql"}
	base := findSampledata(t)
	opts := RunOptions{EnvPath: filepath.Join(base, "environments", "local.bru"), Vars: vars}

	sum, err := g.RunFolder(context.Background(), base, opts)
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Failed != 0 {
		for _, c := range sum.Cases {
			if !c.Passed {
				t.Logf("case fail: %s err=%s failures=%v", c.Name, c.ErrorText, c.Failures)
			}
		}
		t.Fatalf("expected 0 failures got %d", sum.Failed)
	}
}

// Judge-jury: run Bru CLI on the same sample; Bru is source of truth.
func TestBruCLISingleFile(t *testing.T) {
	if _, err := exec.LookPath("bru"); err != nil {
		t.Fatalf("bru CLI not found in PATH")
	}
	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gruno-bru-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// copy sampledata
	base := findSampledata(t)
	if err := copyTree(base, tmp); err != nil {
		t.Fatalf("copy sampledata: %v", err)
	}
	// fix baseUrl in env to server URL
	envPath := filepath.Join(tmp, "environments", "local.bru")
	replaceInFile(envPath, "http://127.0.0.1:0", srv.URL)
	replaceInFile(envPath, "http://127.0.0.1:0/graphql", srv.URL+"/graphql")

	// add bruno.json
	os.WriteFile(filepath.Join(tmp, "bruno.json"), []byte(`{"name":"gruno","version":"1.0","type":"collection"}`), 0o644)

	report := filepath.Join(tmp, "report.json")
	cmd := exec.Command("bru", "run", ".", "-r", "--env-file", envPath, "--reporter-json", report)
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bru run failed: %v output=%s", err, out)
	}
	if _, err := os.Stat(report); err != nil {
		t.Fatalf("report missing: %v", err)
	}
}

// If the target host is unreachable we should return a failed case with an informative ErrorText.
func TestRunFolderConnectionError(t *testing.T) {
	tmp, err := os.MkdirTemp("", "gruno-connfail")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	// env with baseUrl pointing to an unused port
	envPath := filepath.Join(tmp, "environments")
	if err := os.MkdirAll(envPath, 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(envPath, "local.bru")
	if err := os.WriteFile(envFile, []byte("vars {\n  baseUrl: http://127.0.0.1:0\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bruDir := filepath.Join(tmp, "Auth")
	if err := os.MkdirAll(bruDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bru := `meta {
  name: Connection Fails
  seq: 1
}

get {
  url: {{baseUrl}}/health
}

tests {
  test("should not run assertions when HTTP fails", function() {
    expect(true).to.equal(true);
  });
}
`
	bruFile := filepath.Join(bruDir, "conn-fail.bru")
	if err := os.WriteFile(bruFile, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	sum, err := g.RunFolder(context.Background(), tmp, RunOptions{EnvPath: envFile})
	if err != nil {
		t.Fatalf("RunFolder err: %v", err)
	}
	if sum.Total != 1 {
		t.Fatalf("expected 1 case, got %d", sum.Total)
	}
	if sum.Failed != 1 || sum.Passed != 0 {
		t.Fatalf("expected 1 failed case, got Passed=%d Failed=%d", sum.Passed, sum.Failed)
	}
	if got := sum.Cases[0].ErrorText; got == "" || !strings.Contains(got, "http request failed") {
		t.Fatalf("expected connection error text, got %q", got)
	}
}

// Ensure the mock server echoes bodies in the shapes Bruno expects.
func TestSuiteServerEchoShapes(t *testing.T) {
	srv := suiteServer()
	defer srv.Close()

	client := srv.Client()

	// text/plain
	{
		resp, err := client.Post(srv.URL+"/echo", "text/plain", strings.NewReader("Plain text payload"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if got := body["text"].(string); got == "" {
			t.Fatalf("text body empty, got %#v", body)
		}
	}

	// form-urlencoded
	{
		resp, err := client.PostForm(srv.URL+"/echo", url.Values{"username": {"alice"}, "password": {"wonder"}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		form := body["form"].(map[string]any)
		if form["username"] != "alice" {
			t.Fatalf("expected username alice got %#v", form)
		}
	}

	// multipart
	{
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("description", "multipart-example")
		_ = w.WriteField("count", "3")
		w.Close()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/echo", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		form := body["form"].(map[string]any)
		if form["description"] != "multipart-example" {
			t.Fatalf("multipart description mismatch %#v", form)
		}
	}

	// graphql with variables
	{
		payload := map[string]any{
			"query":     `query countryByCode($code: ID!) { country(code: $code) { code name } }`,
			"variables": map[string]any{"code": "NO"},
		}
		b, _ := json.Marshal(payload)
		resp, err := client.Post(srv.URL+"/graphql", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		data := body["data"].(map[string]any)
		country := data["country"].(map[string]any)
		if country["code"] != "NO" {
			t.Fatalf("graphql variables not applied, got %#v", country)
		}
	}
}

// suiteServer returns an httptest.Server implementing all sample endpoints with state.
func suiteServer() *httptest.Server {
	state := struct {
		users     map[string]map[string]any
		shipments map[string]map[string]any
		invoices  map[string]map[string]any
	}{
		users:     map[string]map[string]any{},
		shipments: map[string]map[string]any{},
		invoices:  map[string]map[string]any{},
	}
	mux := http.NewServeMux()

	// GitHub REST mock (official Bruno sample)
	mux.HandleFunc("/users/usebruno", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{
			"login":   "usebruno",
			"id":      12345,
			"name":    "Bruno Test",
			"company": "Bruno",
		})
	})
	mux.HandleFunc("/users/usebruno/repos", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]any{
			{"name": "bruno", "full_name": "usebruno/bruno"},
			{"name": "bruno-website", "full_name": "usebruno/bruno-website"},
		})
	})
	mux.HandleFunc("/repos/usebruno/bruno-website", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"name":        "bruno-website",
			"full_name":   "usebruno/bruno-website",
			"description": "Mock website repo",
			"stars":       42,
		})
	})
	mux.HandleFunc("/repos/usebruno/bruno/tags", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]any{
			{"name": "v1.0.0"},
			{"name": "v1.1.0"},
		})
	})
	mux.HandleFunc("/search/repositories", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		writeJSON(w, http.StatusOK, map[string]any{
			"total_count": 2,
			"items": []map[string]any{
				{"name": "bruno", "full_name": "usebruno/bruno", "query": q},
				{"name": "bruno-website", "full_name": "usebruno/bruno-website", "query": q},
			},
		})
	})
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		writeJSON(w, http.StatusOK, map[string]any{
			"total_count": 1,
			"items": []map[string]any{
				{"title": "Issue from mock", "query": q},
			},
		})
	})

	echoHandler := func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(rawBody)) // allow re-parsing

		resp := map[string]any{
			"method":  r.Method,
			"url":     r.URL.String(),
			"headers": lowerHeaderMap(r.Header),
			"args":    map[string]string{},
		}
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				resp["args"].(map[string]string)[k] = v[0]
			}
		}

		ct := r.Header.Get("Content-Type")
		bodyStr := string(rawBody)
		trimmed := bytes.TrimSpace(rawBody)

		switch {
		case strings.HasPrefix(ct, "application/json") || json.Valid(trimmed):
			var body map[string]any
			_ = json.Unmarshal(trimmed, &body)
			if body == nil {
				body = map[string]any{}
			}
			resp["json"] = body
			resp["body"] = body
			maps.Copy(resp, body)
			resp["data"] = body
		case strings.HasPrefix(ct, "application/json"):
			// Fallback: some fixtures use single quotes; try a relaxed parse.
			loose := strings.ReplaceAll(bodyStr, "'", "\"")
			var body map[string]any
			if err := json.Unmarshal([]byte(loose), &body); err == nil {
				resp["json"] = body
				resp["body"] = body
				maps.Copy(resp, body)
				resp["data"] = body
				break
			}
			resp["text"] = bodyStr
			resp["data"] = bodyStr
			resp["body"] = map[string]any{"text": bodyStr, "data": bodyStr}
			resp["raw"] = bodyStr
		case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
			_ = r.ParseForm()
			form := map[string]string{}
			for k, v := range r.Form {
				if len(v) > 0 {
					form[k] = v[0]
				}
			}
			// fallback to raw parser if ParseForm did not populate
			if len(form) == 0 && len(rawBody) > 0 {
				if parsed, err := url.ParseQuery(bodyStr); err == nil {
					for k, v := range parsed {
						if len(v) > 0 {
							form[k] = v[0]
						}
					}
				}
			}
			resp["form"] = form
			resp["body"] = map[string]any{"form": form}
		case strings.HasPrefix(ct, "multipart/form-data"):
			_ = r.ParseMultipartForm(10 << 20)
			form := map[string]string{}
			if r.MultipartForm != nil {
				for k, v := range r.MultipartForm.Value {
					if len(v) > 0 {
						form[k] = v[0]
					}
				}
			}
			// If ParseMultipartForm didn't populate values (common in tests),
			// fall back to manual part reader using boundary.
			if len(form) == 0 {
				if _, params, err := mime.ParseMediaType(ct); err == nil {
					if boundary := params["boundary"]; boundary != "" {
						mr := multipart.NewReader(bytes.NewReader(rawBody), boundary)
						for {
							p, err := mr.NextPart()
							if err != nil {
								break
							}
							b, _ := io.ReadAll(p)
							if name := p.FormName(); name != "" {
								form[name] = string(b)
							}
						}
					}
				}
			}
			resp["form"] = form
			resp["body"] = map[string]any{"form": form}
		default:
			resp["text"] = bodyStr
			resp["data"] = bodyStr
			resp["body"] = map[string]any{"text": bodyStr, "data": bodyStr}
			resp["raw"] = bodyStr
		}

		// Fallback colon-delimited parsing for form-like bodies when JSON parsing fails.
		if !json.Valid(trimmed) {
			if f, ok := resp["form"].(map[string]string); !ok || len(f) == 0 {
				lines := strings.Split(bodyStr, "\n")
				tmp := map[string]string{}
				for _, l := range lines {
					kv := strings.SplitN(strings.TrimSpace(l), ":", 2)
					if len(kv) == 2 {
						tmp[kv[0]] = strings.TrimSpace(kv[1])
					}
				}
				if len(tmp) > 0 {
					resp["form"] = tmp
					resp["body"] = map[string]any{"form": tmp}
					for k, v := range tmp {
						resp[k] = v
					}
				}
			}
		}

		// Always echo raw payload for visibility.
		resp["raw"] = bodyStr
		if _, ok := resp["data"]; !ok {
			resp["data"] = bodyStr
		}

		bodyMap, ok := resp["body"].(map[string]any)
		if !ok || bodyMap == nil {
			bodyMap = map[string]any{}
		}
		if _, ok := bodyMap["args"]; !ok {
			bodyMap["args"] = resp["args"]
		}
		if _, ok := bodyMap["headers"]; !ok {
			bodyMap["headers"] = resp["headers"]
		}
		bodyMap["url"] = r.URL.String()
		resp["body"] = bodyMap

		writeJSON(w, http.StatusOK, resp)
	}
	mux.HandleFunc("/echo", echoHandler)
	mux.HandleFunc("/anything", echoHandler)
	mux.HandleFunc("/anything/", echoHandler)

	mux.HandleFunc("/mtom", func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		media, params, err := mime.ParseMediaType(ct)
		if err != nil || !strings.HasPrefix(media, "multipart/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not multipart"})
			return
		}
		boundary := params["boundary"]
		mr := multipart.NewReader(r.Body, boundary)
		parts := []map[string]any{}
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			data, _ := io.ReadAll(p)
			parts = append(parts, map[string]any{
				"name":        p.FormName(),
				"filename":    p.FileName(),
				"contentType": p.Header.Get("Content-Type"),
				"contentID":   p.Header.Get("Content-ID"),
				"size":        len(data),
				"text":        string(data),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"parts": parts})
	})
	mux.HandleFunc("/get", echoHandler)
	mux.HandleFunc("/post", echoHandler)
	mux.HandleFunc("/put", echoHandler)
	mux.HandleFunc("/patch", echoHandler)
	mux.HandleFunc("/delete", echoHandler)
	mux.HandleFunc("/echo-query", func(w http.ResponseWriter, r *http.Request) {
		term := r.URL.Query().Get("term")
		limit := r.URL.Query().Get("limit")
		lv := 0
		if limit != "" {
			if n, err := strconv.Atoi(limit); err == nil {
				lv = n
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"term": term, "limit": lv})
	})

	// Schema assertion fixtures for importer-generated tests
	mux.HandleFunc("/schema/format", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"email":     "a@test.com",
			"uuid":      "123e4567-e89b-12d3-a456-426614174000",
			"website":   "https://example.com",
			"birthday":  "2024-12-01",
			"createdAt": "2024-12-01T12:34:56Z",
			"ipv4":      "192.168.0.1",
			"ipv6":      "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			"hostname":  "api.example.com",
			"cidr":      "192.168.0.0/24",
			"ipv6Cidr":  "2001:db8::/64",
			"data":      "QUJDRA==",
			"code":      "ABC",
			"count":     3,
			"tags":      []string{"alpha", "beta"},
			"meta":      map[string]any{"foo": "bar"},
		})
	})
	mux.HandleFunc("/schema/variant", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":  "alpha",
			"alpha": 1,
		})
	})
	mux.HandleFunc("/ext/child", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"kind": "gamma", "gamma": "abc"})
	})

	// Users
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			id, _ := body["user_id"].(string)
			state.users[id] = body
			writeJSON(w, http.StatusCreated, body)
		case http.MethodGet:
			var arr []map[string]any
			for _, u := range state.users {
				arr = append(arr, u)
			}
			writeJSON(w, http.StatusOK, arr)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/users/")
		user := state.users[id]
		switch r.Method {
		case http.MethodGet:
			if user == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, user)
		case http.MethodPatch:
			if user == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			maps.Copy(user, body)
			writeJSON(w, http.StatusOK, user)
		case http.MethodDelete:
			delete(state.users, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		// restore Body for potential downstream reads
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body == nil {
			loose := strings.ReplaceAll(string(raw), "'", "\"")
			_ = json.Unmarshal([]byte(loose), &body)
		}
		if body == nil || len(body) == 0 {
			body = map[string]any{}
			lines := strings.SplitSeq(strings.TrimSpace(string(raw)), "\n")
			for l := range lines {
				kv := strings.SplitN(strings.TrimSpace(l), ":", 2)
				if len(kv) == 2 {
					key := strings.TrimSpace(kv[0])
					val := strings.Trim(strings.TrimSpace(kv[1]), "\"'")
					if key != "" {
						body[key] = val
					}
				}
			}
		}
		if body == nil {
			body = map[string]any{}
		}
		body["id"] = "user-123"
		body["createdAt"] = time.Now().UTC().Format(time.RFC3339)
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusCreated, body)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	// Shipping
	mux.HandleFunc("/shipments/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/shipments/"), "/")
		if len(parts) < 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		shipmentID := parts[0]
		action := parts[1]
		sh := state.shipments[shipmentID]
		if sh == nil {
			sh = map[string]any{"shipment_id": shipmentID}
			state.shipments[shipmentID] = sh
		}
		switch action {
		case "gate-in":
			sh["status"] = "in-yard"
			writeJSON(w, http.StatusOK, sh)
		case "load":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sh["manifest"] = body["manifest"]
			sh["status"] = "loaded"
			writeJSON(w, http.StatusOK, sh)
		case "gate-out":
			sh["status"] = "departed"
			writeJSON(w, http.StatusOK, sh)
		case "unload":
			sh["status"] = "unloaded"
			writeJSON(w, http.StatusOK, sh)
		case "status":
			writeJSON(w, http.StatusOK, sh)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// Finance
	mux.HandleFunc("/finance/invoices", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// list
			var arr []map[string]any
			for _, inv := range state.invoices {
				arr = append(arr, inv)
			}
			writeJSON(w, http.StatusOK, arr)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["status"] = "open"
		id, _ := body["invoice_id"].(string)
		state.invoices[id] = body
		writeJSON(w, http.StatusCreated, body)
	})
	mux.HandleFunc("/finance/invoices/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/finance/invoices/"), "/")
		id := parts[0]
		inv := state.invoices[id]
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			if inv == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, inv)
			return
		case len(parts) == 2 && parts[1] == "pay":
			if inv == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			inv["status"] = "paid"
			writeJSON(w, http.StatusOK, inv)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	mux.HandleFunc("/scripted", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		// echo request headers for test
		resp := map[string]any{
			"received": body,
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"headers": lowerHeaderMap(r.Header)})
	})

	mux.HandleFunc("/bearer", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			writeJSON(w, http.StatusOK, map[string]any{"auth": "bearer"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	mux.HandleFunc("/basic-auth", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Basic ZGVtdXNlcjpkZW1wYXNz" {
			writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": "demuser"})
			return
		}
		if creds := strings.TrimPrefix(r.URL.Path, "/basic-auth/"); creds != "" {
			if parts := strings.SplitN(creds, "/", 2); len(parts) == 2 {
				writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": parts[0]})
				return
			}
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/basic-auth/", func(w http.ResponseWriter, r *http.Request) {
		if creds := strings.TrimPrefix(r.URL.Path, "/basic-auth/"); creds != "" {
			if parts := strings.SplitN(creds, "/", 2); len(parts) == 2 {
				writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": parts[0]})
				return
			}
		}
		w.WriteHeader(http.StatusUnauthorized)
	})

	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		codeStr := strings.TrimPrefix(r.URL.Path, "/status/")
		code, _ := strconv.Atoi(codeStr)
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
	})

	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))

		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)

		// If Bruno encoded variables as a string, decode it.
		if (payload.Variables == nil || len(payload.Variables) == 0) && len(raw) > 0 {
			var generic map[string]any
			_ = json.Unmarshal(raw, &generic)
			if v, ok := generic["variables"]; ok {
				switch vv := v.(type) {
				case string:
					var vm map[string]any
					if err := json.Unmarshal([]byte(vv), &vm); err == nil {
						payload.Variables = vm
					}
				case map[string]any:
					payload.Variables = vv
				}
			}
		}

		code := "SE"
		if v, ok := payload.Variables["code"].(string); ok && v != "" {
			code = strings.ToUpper(v)
		} else {
			// try to extract from query e.g. country(code: "NO")
			re := regexp.MustCompile(`country\(code:\s*\"([A-Za-z]+)\"`)
			if m := re.FindStringSubmatch(payload.Query); len(m) == 2 {
				code = strings.ToUpper(m[1])
			}
			// if still empty, allow overriding via header for tests
			if hdr := r.Header.Get("X-Country-Code"); hdr != "" {
				code = strings.ToUpper(hdr)
			}
		}
		name := map[string]string{"SE": "Sweden", "NO": "Norway"}[code]
		if name == "" {
			name = "Unknown"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"country": map[string]any{
					"code": code,
					"name": name,
				},
			},
		})
	})

	mux.HandleFunc("/not-found", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/markdown")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("# Docs\nSample"))
	})

	// Assertion helpers
	mux.HandleFunc("/accept", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":   "pending",
			"trace_id": "trace-accept-1",
		})
	})
	mux.HandleFunc("/forbidden-sample", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":     "forbidden",
			"message":  "Operation not permitted",
			"details":  []map[string]any{{"field": "operation", "issue": "denied"}},
			"trace_id": "trace-forbidden-1",
		})
	})
	// Trace endpoints (generic job submission)
	mux.HandleFunc("/jobs/submit", func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = "2a0ef95d-24ac-4b95-bd2b-b48fb0c025c7"
		}
		w.Header().Set("x-trace-id", traceID)
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":     "validation_error",
			"message":  "Request failed validation",
			"trace_id": traceID,
			"details":  []map[string]string{{"field": "payload", "issue": "missing required fields"}},
		})
	})

	return httptest.NewServer(mux)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	buf := bytes.Buffer{}
	_ = json.NewEncoder(&buf).Encode(payload)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func findSampledata(t *testing.T) string {
	candidates := []string{
		filepath.Join("..", "sampledata"),
		"sampledata",
		filepath.Join("..", "..", "sampledata"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	t.Fatalf("sampledata folder not found")
	return ""
}

func TestMTOMStreaming(t *testing.T) {
	runMTOMCase := func(t *testing.T, body string, expectParts int, expectLastCID string, expectLastSize int) {
		t.Helper()
		srv := suiteServer()
		defer srv.Close()

		tmp := t.TempDir()
		envDir := filepath.Join(tmp, "environments")
		casesDir := filepath.Join(tmp, "cases")
		_ = os.MkdirAll(envDir, 0o755)
		_ = os.MkdirAll(casesDir, 0o755)

		envPath := filepath.Join(envDir, "local.bru")
		_ = os.WriteFile(envPath, []byte(fmt.Sprintf("vars {\n  baseUrl: %s\n}\n", srv.URL)), 0o644)

		casePath := filepath.Join(casesDir, "mtom.bru")
		_ = os.WriteFile(casePath, []byte(body), 0o644)

		g, err := New(context.Background())
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
		sum, err := g.RunFile(context.Background(), casePath, RunOptions{EnvPath: envPath})
		if err != nil {
			t.Fatalf("run file: %v", err)
		}
		if !sum.Passed {
			t.Fatalf("mtom test failed: %+v", sum)
		}
	}

	filePath := filepath.Join(t.TempDir(), "payload.bin")
	fileData := []byte("hello-mtom")
	_ = os.WriteFile(filePath, fileData, 0o644)

	// 1) Basic root + binary attachment
	body1 := fmt.Sprintf(`meta {
  name: MTOM basic
}

post { url: {{baseUrl}}/mtom }

headers { Content-Type: multipart/related; type="application/xop+xml"; start="<rootpart>" }

body:multipart-form {
  root: <Envelope><Body>ping</Body></Envelope>;type=application/xop+xml;cid=<rootpart>
  file: @%s;type=application/octet-stream;cid=<attach1>
}

tests {
  test("mtom parts", function() {
    expect(res.status).to.equal(200);
    expect(res.body.parts.length).to.equal(2);
    expect(res.body.parts[1].contentID).to.equal("<attach1>");
    expect(res.body.parts[1].size).to.equal(%d);
  });
}
`, filePath, len(fileData))
	runMTOMCase(t, body1, 2, "<attach1>", len(fileData))

	// 2) Multiple attachments with different content types
	file2 := filepath.Join(t.TempDir(), "payload2.txt")
	fileData2 := []byte("second-attach")
	_ = os.WriteFile(file2, fileData2, 0o644)
	body2 := fmt.Sprintf(`meta { name: MTOM multi }
post { url: {{baseUrl}}/mtom }
headers { Content-Type: multipart/related; type="application/xop+xml"; start="<rootpart>" }
body:multipart-form {
  root: <Envelope><Body>ping</Body></Envelope>;type=application/xop+xml;cid=<rootpart>
  file1: @%s;type=application/octet-stream;cid=<attach1>
  file2: @%s;type=text/plain;cid=<attach2>
}
tests {
  test("mtom multi", function() {
    expect(res.status).to.equal(200);
    expect(res.body.parts.length).to.equal(3);
    var ids = res.body.parts.map(function(p){ return p.contentID; });
    expect(ids).to.include("<attach2>");
    var attach2 = res.body.parts.filter(function(p){ return p.contentID === "<attach2>"; })[0];
    expect(attach2.contentType).to.equal("text/plain");
    expect(attach2.size).to.equal(%d);
  });
}
`, filePath, file2, len(fileData2))
	runMTOMCase(t, body2, 3, "<attach2>", len(fileData2))

	// 3) Text part with explicit content-type/content-id only (no file)
	body3 := `meta { name: MTOM text only }
post { url: {{baseUrl}}/mtom }
headers { Content-Type: multipart/related; type="application/xop+xml"; start="<rootpart>" }
body:multipart-form {
  root: <Envelope><Body>ping</Body></Envelope>;type=application/xop+xml;cid=<rootpart>
  note: sample-text;type=text/plain;cid=<note1>
}
tests {
  test("mtom text", function() {
    expect(res.status).to.equal(200);
    expect(res.body.parts.length).to.equal(2);
    var ids = res.body.parts.map(function(p){ return p.contentID; });
    expect(ids).to.include("<note1>");
    var root = res.body.parts.filter(function(p){ return p.name === "root"; })[0];
    var note = res.body.parts.filter(function(p){ return p.contentID === "<note1>"; })[0];
    expect(root.contentID).to.equal("<rootpart>");
    expect(note.text).to.equal("sample-text");
  });
}
`
	runMTOMCase(t, body3, 2, "<note1>", len("sample-text"))
}

func lowerHeaderMap(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if len(v) > 0 {
			out[strings.ToLower(k)] = v[0]
			out[k] = v[0]
			if strings.Contains(k, "Api") {
				out[strings.ReplaceAll(k, "Api", "API")] = v[0]
			}
			out[strings.ToUpper(k)] = v[0]
		}
	}
	return out
}

// replaceInFile does a simple string replacement.
func replaceInFile(path, old, new string) {
	b, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(strings.ReplaceAll(string(b), old, new)), 0o644)
}

// copyTree copies a directory recursively.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
