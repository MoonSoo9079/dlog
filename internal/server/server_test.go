package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	if res.StatusCode != 200 || !strings.Contains(string(page), "Dlog") || !strings.Contains(string(page), "/app.js") || !strings.Contains(string(page), "/bootstrap.min.css") || !strings.Contains(string(page), `href="/account"`) || !strings.Contains(string(page), `id="range"`) || strings.Contains(string(page), `id="drawer"`) || strings.Contains(string(page), `id="manage"`) {
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
	if res.StatusCode != 200 || !strings.Contains(string(page), "New account") || !strings.Contains(string(page), `id="admin-hosts"`) || !strings.Contains(string(page), `id="settings-groups"`) || !strings.Contains(string(page), "Save settings") || strings.Contains(string(page), "offcanvas") {
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
	adaSession := login(t, client, ts.URL, "ada", "reader-password")
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"password":"reader-password-2"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("set password: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", adaSession, "")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reset session still signed in: %d", res.StatusCode)
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

func TestChangePassword(t *testing.T) {
	ts, _, _ := testServer(t)
	client := noRedirect(t)

	res := do(t, client, http.MethodGet, ts.URL+"/account", "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login") {
		t.Fatalf("account redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", "", `{"current_password":"admin-test-password","password":"admin-test-password-2"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous change: %d", res.StatusCode)
	}

	user := login(t, client, ts.URL, "admin", testAdminPassword)
	res = do(t, client, http.MethodGet, ts.URL+"/account", user, "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), "Current password") || !strings.Contains(string(page), "Change password") || !strings.Contains(string(page), "Sign out other sessions") {
		t.Fatalf("account page: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", user, `{"current_password":"wrong-password","password":"admin-test-password-2"}`)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "wrong password") {
		t.Fatalf("wrong current: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", user, `{"current_password":"`+testAdminPassword+`","password":"short"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", user, `{"current_password":"`+testAdminPassword+`","password":"`+testAdminPassword+`"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("same password: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", user, `{"current_password":"`+testAdminPassword+`","password":"admin-test-password-2","extra":1}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", user, `{"current_password":"`+testAdminPassword+`","password":"admin-test-password-2"}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"username":"admin"`) {
		t.Fatalf("change: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", user, "")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("old session: %d", res.StatusCode)
	}
	if got := login(t, client, ts.URL, "admin", "admin-test-password-2"); !strings.HasPrefix(got, "ses_") {
		t.Fatalf("new password: %s", got)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"admin","password":"`+testAdminPassword+`"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still works: %d", res.StatusCode)
	}

	other := login(t, client, ts.URL, "admin", "admin-test-password-2")
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", user, `{"current_password":"admin-test-password-2","password":"admin-test-password-3","sign_out_others":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sign out others: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", user, "")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("current session: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", other, "")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("other session: %d", res.StatusCode)
	}
}

func TestHostDisplayName(t *testing.T) {
	ts, st, key := testServer(t)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	host, _, err := st.CreateHost("web-01")
	if err != nil {
		t.Fatal(err)
	}
	res := do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID, admin, `{}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty patch: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID, admin, `{"display_name":"!!"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad name: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID, admin, `{"display_name":"Web 01"}`)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"display_name":"Web 01"`) || !strings.Contains(string(body), `"hostname":"web-01"`) {
		t.Fatalf("display name: %d %s", res.StatusCode, body)
	}
	res = register(t, client, ts.URL, key, "web-01", time.Now().UTC())
	regBody, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register: %d %s", res.StatusCode, regBody)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"display_name":"Web 01"`) || !strings.Contains(string(body), `"hostname":"web-01"`) {
		t.Fatalf("name survived register: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/hosts/"+host.ID, admin, `{"display_name":""}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"display_name":""`) {
		t.Fatalf("clear name: %d %s", res.StatusCode, body)
	}
}

func TestUserGroupAccess(t *testing.T) {
	ts, st, _ := testServer(t)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	edge, err := st.CreateGroup("Edge")
	if err != nil {
		t.Fatal(err)
	}
	lab, err := st.CreateGroup("Lab")
	if err != nil {
		t.Fatal(err)
	}
	edgeHost, _, err := st.CreateHost("edge-1")
	if err != nil {
		t.Fatal(err)
	}
	labHost, _, err := st.CreateHost("lab-1")
	if err != nil {
		t.Fatal(err)
	}
	loose, _, err := st.CreateHost("loose-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetHostGroup(edgeHost.ID, edge.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHostGroup(labHost.ID, lab.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := st.Ingest(edgeHost.ID, []protocol.Record{sample(now, "edge-line")}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ingest(labHost.ID, []protocol.Record{sample(now, "lab-line")}); err != nil {
		t.Fatal(err)
	}

	res := do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"ada","password":"reader-password","role":"user"}`)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"all_hosts":true`) || !strings.Contains(string(body), `"group_ids":[]`) {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var ada Account
	if err := json.Unmarshal(body, &ada); err != nil {
		t.Fatal(err)
	}
	user := login(t, client, ts.URL, "ada", "reader-password")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "edge-1") || !strings.Contains(string(body), "lab-1") || !strings.Contains(string(body), "loose-1") {
		t.Fatalf("all hosts: %d %s", res.StatusCode, body)
	}

	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"all_hosts":false,"group_ids":["`+edge.ID+`","`+edge.ID+`"]}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"all_hosts":false`) || !strings.Contains(string(body), edge.ID) {
		t.Fatalf("scope: %d %s", res.StatusCode, body)
	}
	if strings.Count(string(body), edge.ID) != 1 {
		t.Fatalf("duplicate group stored: %s", body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "edge-1") || strings.Contains(string(body), "lab-1") || strings.Contains(string(body), "loose-1") || strings.Contains(string(body), "Lab") {
		t.Fatalf("scoped hosts: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/logs?host_id="+edgeHost.ID, user, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "edge-line") {
		t.Fatalf("edge logs: %d %s", res.StatusCode, body)
	}
	for _, id := range []string{labHost.ID, loose.ID} {
		res = do(t, client, http.MethodGet, ts.URL+"/api/v1/logs?host_id="+id, user, "")
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("hidden logs %s: %d", id, res.StatusCode)
		}
		res = do(t, client, http.MethodGet, ts.URL+"/api/v1/containers?host_id="+id, user, "")
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("hidden containers %s: %d", id, res.StatusCode)
		}
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "loose-1") || !strings.Contains(string(body), "lab-1") {
		t.Fatalf("admin still sees all: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, user, `{"all_hosts":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("user sets own scope: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"group_ids":["grp_missing"]}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing group: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"all_hosts":false,"group_ids":[]}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear groups: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"hosts":[]`) || !strings.Contains(string(body), `"groups":[]`) {
		t.Fatalf("no groups: %d %s", res.StatusCode, body)
	}

	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"all_hosts":false,"group_ids":["`+edge.ID+`"]}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("restore edge: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/groups/"+edge.ID, admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete group: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || strings.Contains(string(body), "edge-1") || strings.Contains(string(body), edge.ID) {
		t.Fatalf("deleted group still visible: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+ada.ID, admin, `{"all_hosts":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("all hosts again: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/hosts", user, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "edge-1") || !strings.Contains(string(body), "loose-1") || !strings.Contains(string(body), "lab-1") {
		t.Fatalf("all hosts after: %d %s", res.StatusCode, body)
	}
}

func TestSettingsRegistrationAndCertificate(t *testing.T) {
	dir := t.TempDir()
	st := testStore(t)
	if _, err := st.CreateUser("admin", testAdminPassword, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser("ada", "reader-password", RoleUser); err != nil {
		t.Fatal(err)
	}
	key, err := agentcred.Generate()
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")
	if err := key.Write(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	api := New(st, key.Certificate(), false, false)
	api.SetCertFiles(certPath, keyPath)
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	user := login(t, client, ts.URL, "ada", "reader-password")

	res := do(t, client, http.MethodGet, ts.URL+"/api/v1/brand", "", "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"name":"Dlog"`) {
		t.Fatalf("brand: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/settings", user, "")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("user settings: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/admin", admin, "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(page), "Save settings") || !strings.Contains(string(page), "Automatic registration") || !strings.Contains(string(page), `id="accounts"`) || !strings.Contains(string(page), `id="create-group"`) {
		t.Fatalf("settings page: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", admin, `{"name":"Edge"}`)
	gb, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("group: %d %s", res.StatusCode, gb)
	}
	var edge Group
	if err := json.Unmarshal(gb, &edge); err != nil {
		t.Fatal(err)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/settings", admin, `{"brand_name":"Lab Logs","brand_tagline":"Lab console","auto_register":false,"default_group_id":"`+edge.ID+`","retention":"30d","session":"4h","offline":"2m"}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"brand_name":"Lab Logs"`) || !strings.Contains(string(body), `"auto_register":false`) || !strings.Contains(string(body), edge.ID) {
		t.Fatalf("save settings: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/brand", "", "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "Lab Logs") || !strings.Contains(string(body), "Lab console") {
		t.Fatalf("brand after save: %s", body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/settings", admin, `{"brand_name":"<script>","session":"1m","retention":"-1h"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid settings: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/settings", admin, `{"default_group_id":"grp_missing"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing default group: %d", res.StatusCode)
	}

	res = register(t, client, ts.URL, key, "edge-host", time.Now())
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "registration closed") {
		t.Fatalf("closed register: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/settings", admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "edge-host") {
		t.Fatalf("pending missing: %s", body)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/pending/edge-host/approve", admin, "{}")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"group_id":"`+edge.ID+`"`) {
		t.Fatalf("approve: %d %s", res.StatusCode, body)
	}
	res = register(t, client, ts.URL, key, "edge-host", time.Now())
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "ht_") {
		t.Fatalf("register after approve: %d %s", res.StatusCode, body)
	}
	res = register(t, client, ts.URL, key, "lab-host", time.Now())
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("second new host: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/pending/lab-host", admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("dismiss: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/groups/"+edge.ID, admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete default group: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/settings", admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(body), edge.ID) {
		t.Fatalf("default group remained: %s", body)
	}

	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/certificate", admin, "")
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	oldPrint := agentcred.Fingerprint(key.Certificate())
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), oldPrint) || strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatalf("certificate info: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/certificate", admin, `{"action":"generate"}`)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "PRIVATE KEY") || strings.Contains(string(body), oldPrint) {
		t.Fatalf("generate: %d %s", res.StatusCode, body)
	}
	var issued struct {
		Fingerprint string `json:"fingerprint"`
		CertPEM     string `json:"cert_pem"`
		KeyPEM      string `json:"key_pem"`
	}
	if err := json.Unmarshal(body, &issued); err != nil {
		t.Fatal(err)
	}
	rotated, err := agentcred.ParsePair([]byte(issued.CertPEM), []byte(issued.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	if agentcred.Fingerprint(rotated.Certificate()) != issued.Fingerprint {
		t.Fatalf("fingerprint mismatch")
	}
	res = register(t, client, ts.URL, key, "old-cert-host", time.Now())
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old certificate still trusted: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/settings", admin, `{"auto_register":true}`)
	res.Body.Close()
	res = register(t, client, ts.URL, rotated, "new-cert-host", time.Now())
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || !strings.Contains(string(body), "ht_") {
		t.Fatalf("new certificate: %d %s", res.StatusCode, body)
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
