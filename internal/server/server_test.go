package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/protocol"
)

const testAdminPassword = "admin-test-password"

func testServer(t *testing.T) (*httptest.Server, *Store, *agentcred.Key) {
	t.Helper()
	st := testStore(t)
	if _, err := st.CreateUser("admin", testAdminPassword, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	key, err := agentcred.Generate()
	if err != nil {
		t.Fatal(err)
	}
	h := New(st, key.Certificate(), false, false).Handler()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, st, key
}

func noRedirect(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func do(t *testing.T, client *http.Client, method, url, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if strings.HasPrefix(token, "ses_") {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	} else if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func login(t *testing.T, client *http.Client, base, username, password string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, client, http.MethodPost, base+"/api/v1/login", "", string(body))
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("login %s: %d %s", username, res.StatusCode, b)
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("missing session cookie")
	return ""
}

func TestHTTPAuthIngestAndUI(t *testing.T) {
	ts, st, _ := testServer(t)
	client := noRedirect(t)

	res := do(t, client, http.MethodGet, ts.URL+"/api/v1/health", "", "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "ok") {
		t.Fatalf("health: %d %s", res.StatusCode, body)
	}

	res = do(t, client, http.MethodGet, ts.URL+"/", "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login") {
		t.Fatalf("index redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	res = do(t, client, http.MethodGet, ts.URL+"/login", "", "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), "Username") || !strings.Contains(string(page), "Password") || !strings.Contains(string(page), "/bootstrap.min.css") {
		t.Fatalf("login page: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodGet, ts.URL+"/app.css", "", "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("css: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/bootstrap.min.css", "", "")
	css, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(css), "Bootstrap") {
		t.Fatalf("bootstrap css: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/bootstrap.bundle.min.js", "", "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("bootstrap js: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"admin","password":"wrong-password"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"admin","password":"`+testAdminPassword+`"}`)
	if res.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("login: %d %s", res.StatusCode, b)
	}
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	res.Body.Close()
	if session == nil {
		t.Fatal("missing session cookie")
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	req.AddCookie(session)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), "Dlog") || !strings.Contains(string(page), "/app.js") || !strings.Contains(string(page), "/bootstrap.min.css") || strings.Contains(string(page), `id="drawer"`) || strings.Contains(string(page), `id="manage"`) {
		t.Fatalf("console: %d", res.StatusCode)
	}

	_, hostToken, err := st.CreateHost("edge-1")
	if err != nil || !strings.HasPrefix(hostToken, "ht_") {
		t.Fatalf("create host: %v %s", err, hostToken)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/app.js", "", "")
	js, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(string(js), "enroll-tokens") || !strings.Contains(string(js), "certificate") {
		t.Fatalf("console script: %d", res.StatusCode)
	}

	tsNow := time.Now().UTC().Format(time.RFC3339Nano)
	ingest := `{"agent_version":"0.1.0","records":[` +
		`{"container_id":"abc123abc123","container_name":"nginx","image":"nginx:1","stream":"stdout","timestamp":"` + tsNow + `","message":"hello-dlog"},` +
		`{"container_id":"bad id","container_name":"nginx","image":"nginx:1","stream":"stdout","timestamp":"` + tsNow + `","message":"nope"}]}`
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/ingest", "", ingest)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ingest anon: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/ingest", hostToken, ingest)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"accepted":1`) || !strings.Contains(string(b), `"rejected":1`) {
		t.Fatalf("ingest: %d %s", res.StatusCode, b)
	}
}

func TestHTTPLogsAndDisable(t *testing.T) {
	ts, st, key := testServer(t)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	host, hostToken, err := st.CreateHost("edge-2")
	if err != nil {
		t.Fatal(err)
	}
	hostID := host.ID
	tsNow := time.Now().UTC().Format(time.RFC3339Nano)
	body := `{"agent_version":"0.1.0","records":[{"container_id":"abc123abc123","container_name":"nginx","image":"nginx:1","stream":"stderr","timestamp":"` + tsNow + `","message":"boom"}]}`
	res := do(t, client, http.MethodPost, ts.URL+"/api/v1/ingest", hostToken, body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("ingest status %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/logs?host_id="+hostID+"&stream=stderr", admin, "")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "boom") {
		t.Fatalf("logs: %d %s", res.StatusCode, b)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/logs?host_id="+hostID+"&stream=stdout", admin, "")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "boom") {
		t.Fatalf("stdout filter leaked stderr: %s", b)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/hosts/"+hostID+"/disable", admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("disable: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/ingest", hostToken, body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled ingest: %d", res.StatusCode)
	}

	res = register(t, client, ts.URL, key, "node-9", time.Now())
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(b), "ht_") {
		t.Fatalf("register: %d %s", res.StatusCode, b)
	}
	var first protocol.RegisterResponse
	if err := json.Unmarshal(b, &first); err != nil {
		t.Fatal(err)
	}
	res = register(t, client, ts.URL, key, "node-9", time.Now())
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("re-register: %d %s", res.StatusCode, b)
	}
	var second protocol.RegisterResponse
	if err := json.Unmarshal(b, &second); err != nil || second.HostToken == first.HostToken || second.HostID != first.HostID {
		t.Fatalf("re-register body: %s %v", b, err)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/ingest", first.HostToken, `{"agent_version":"0.1.0","records":[]}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token after re-register: %d", res.StatusCode)
	}
	res = register(t, client, ts.URL, key, "node-9", time.Now().Add(-time.Hour))
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale register: %d", res.StatusCode)
	}
	other, err := agentcred.Generate()
	if err != nil {
		t.Fatal(err)
	}
	res = register(t, client, ts.URL, other, "node-10", time.Now())
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong certificate: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/hosts/"+first.HostID+"/disable", admin, "")
	res.Body.Close()
	res = register(t, client, ts.URL, key, "node-9", time.Now())
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled register: %d", res.StatusCode)
	}
}

func TestAdminGroups(t *testing.T) {
	ts, st, _ := testServer(t)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	host, _, err := st.CreateHost("web-01")
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", admin, `{"name":"-bad"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid name: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", admin, `{"name":"  Edge Nodes  ","extra":1}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", admin, `{"name":"  Edge Nodes  "}`)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var edge Group
	if err := json.Unmarshal(body, &edge); err != nil || edge.Name != "Edge Nodes" || edge.HostCount != 0 {
		t.Fatalf("group body: %s %v", body, err)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", admin, `{"name":"edge nodes"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/groups/"+edge.ID, admin, `{"name":"Edge"}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"name":"Edge"`) {
		t.Fatalf("rename: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID+"/group", admin, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing group_id: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID+"/group", admin, `{"group_id":"grp_missing"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing group: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID+"/group", admin, `{"group_id":"`+edge.ID+`"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("assign: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	var listed struct {
		Hosts  []Host  `json:"hosts"`
		Groups []Group `json:"groups"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Hosts) != 1 || listed.Hosts[0].GroupID != edge.ID || len(listed.Groups) != 1 || listed.Groups[0].HostCount != 1 {
		t.Fatalf("list: %s", body)
	}
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/groups/"+edge.ID, admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	got, err := st.Host(host.ID)
	if err != nil || got.GroupID != "" {
		t.Fatalf("ungrouped: %+v %v", got, err)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID+"/group", admin, `{"group_id":""}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("clear: %d", res.StatusCode)
	}
}

func TestAccounts(t *testing.T) {
	ts, _, _ := testServer(t)
	client := noRedirect(t)

	res := do(t, client, http.MethodGet, ts.URL+"/admin", "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login") {
		t.Fatalf("admin redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", "admin-token-is-gone", "")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bearer token: %d", res.StatusCode)
	}

	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", admin, "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"role":"admin"`) {
		t.Fatalf("me: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/admin", admin, "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), "New account") || !strings.Contains(string(page), `id="admin-hosts"`) || !strings.Contains(string(page), `id="panel-groups"`) || strings.Contains(string(page), "offcanvas") {
		t.Fatalf("admin page: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"ada","password":"short","role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"-ada","password":"reader-password","role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad username: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"ada","password":"reader-password","role":"owner"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad role: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"ada","password":"reader-password","role":"user","extra":1}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"ada","password":"reader-password","role":"user"}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"username":"ada"`) {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var ada Account
	if err := json.Unmarshal(body, &ada); err != nil || !strings.HasPrefix(ada.ID, "usr_") || ada.Role != RoleUser {
		t.Fatalf("account: %s %v", body, err)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"Ada","password":"reader-password","role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}

	user := login(t, client, ts.URL, "ada", "reader-password")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("user hosts: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", user, `{"name":"Nope"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("user create group: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/users", user, "")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("user list accounts: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/admin", user, "")
	page, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(page), "Admins only") {
		t.Fatalf("user admin page: %d", res.StatusCode)
	}

	var me Account
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", admin, "")
	meBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(meBody, &me); err != nil {
		t.Fatal(err)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+me.ID, admin, `{"role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("demote last admin: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/users/"+me.ID, admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "signed-in") {
		t.Fatalf("delete self: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete user: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("deleted user session: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"ada","password":"reader-password","role":"admin"}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("recreate: %d %s", res.StatusCode, body)
	}
	if err := json.Unmarshal(body, &ada); err != nil {
		t.Fatal(err)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"password":"reader-password-2"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("set password: %d", res.StatusCode)
	}
	if got := login(t, client, ts.URL, "ada", "reader-password-2"); !strings.HasPrefix(got, "ses_") {
		t.Fatalf("new password session: %s", got)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+me.ID, admin, `{"role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("demote with another admin: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/users", admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("demoted admin: %d", res.StatusCode)
	}
}

func register(t *testing.T, client *http.Client, base string, key *agentcred.Key, hostname string, at time.Time) *http.Response {
	t.Helper()
	body, err := json.Marshal(protocol.RegisterRequest{Hostname: hostname, AgentVersion: "0.1.0", Timestamp: at})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := agentcred.Sign(key.Pair(), body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/register", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(agentcred.Header, sig)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
