package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/jimlambrt/gldap"
)

const (
	readerDN     = "cn=reader,dc=example,dc=org"
	readerSecret = "reader-secret"
	adminGroupDN = "cn=admins,ou=groups,dc=example,dc=org"
	peopleBase   = "dc=example,dc=org"
)

type dirPerson struct {
	uid      string
	dn       string
	password string
	memberOf []string
}

func testPeople() []dirPerson {
	return []dirPerson{
		{uid: "alice", dn: "uid=alice,ou=people,dc=example,dc=org", password: "alice-secret"},
		{uid: "bob", dn: "uid=bob,ou=people,dc=example,dc=org", password: "bob-secret"},
		{uid: "cara", dn: "uid=cara,ou=people,dc=example,dc=org", password: "cara-secret", memberOf: []string{adminGroupDN}},
		{uid: "Casey", dn: "uid=Casey,ou=people,dc=example,dc=org", password: "casey-secret"},
		{uid: "admin", dn: "uid=admin,ou=people,dc=example,dc=org", password: "directory-admin-secret"},
	}
}

type testDirectory struct {
	url    string
	people []dirPerson
	mu     sync.Mutex
	admins map[string]bool
	binds  int
}

func startDirectory(t *testing.T) *testDirectory {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatal(err)
	}
	srv, err := gldap.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	mux, err := gldap.NewMux()
	if err != nil {
		t.Fatal(err)
	}
	dir := &testDirectory{
		url:    "ldap://" + addr,
		people: testPeople(),
		admins: map[string]bool{"bob": true},
	}
	if err := mux.Bind(dir.onBind); err != nil {
		t.Fatal(err)
	}
	if err := mux.Search(dir.onSearch); err != nil {
		t.Fatal(err)
	}
	if err := srv.Router(mux); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(addr) }()
	t.Cleanup(func() { _ = srv.Stop() })
	deadline := time.Now().Add(2 * time.Second)
	for !srv.Ready() {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatal(err)
			}
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("directory did not listen")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return dir
}

func (d *testDirectory) setAdmin(uid string, on bool) {
	d.mu.Lock()
	d.admins[uid] = on
	d.mu.Unlock()
}

func (d *testDirectory) bindCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.binds
}

func (d *testDirectory) onBind(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewBindResponse(gldap.WithResponseCode(gldap.ResultInvalidCredentials))
	defer w.Write(resp)
	msg, err := r.GetSimpleBindMessage()
	if err != nil || string(msg.Password) == "" {
		return
	}
	d.mu.Lock()
	d.binds++
	d.mu.Unlock()
	if strings.EqualFold(msg.UserName, readerDN) && string(msg.Password) == readerSecret {
		resp.SetResultCode(gldap.ResultSuccess)
		return
	}
	for _, person := range d.people {
		if strings.EqualFold(msg.UserName, person.dn) && string(msg.Password) == person.password {
			resp.SetResultCode(gldap.ResultSuccess)
			return
		}
	}
}

func (d *testDirectory) onSearch(w *gldap.ResponseWriter, r *gldap.Request) {
	resp := r.NewSearchDoneResponse(gldap.WithResponseCode(gldap.ResultNoSuchObject))
	defer w.Write(resp)
	msg, err := r.GetSearchMessage()
	if err != nil {
		return
	}
	if msg.Scope == gldap.BaseObject && strings.EqualFold(msg.BaseDN, adminGroupDN) {
		resp.SetResultCode(gldap.ResultSuccess)
		d.mu.Lock()
		defer d.mu.Unlock()
		for uid, on := range d.admins {
			if !on {
				continue
			}
			needle := strings.ToLower(msg.Filter)
			member := strings.ToLower("uid=" + uid + ",ou=people,dc=example,dc=org")
			if strings.Contains(needle, "memberuid="+strings.ToLower(uid)) || strings.Contains(needle, "member="+member) {
				w.Write(r.NewSearchResponseEntry(adminGroupDN, gldap.WithAttributes(map[string][]string{"cn": {"admins"}})))
				return
			}
		}
		return
	}
	var hits []dirPerson
	for _, person := range d.people {
		want := "(uid=" + strings.ToLower(ldap.EscapeFilter(person.uid)) + ")"
		if strings.Contains(strings.ToLower(msg.Filter), want) {
			hits = append(hits, person)
		}
	}
	resp.SetResultCode(gldap.ResultSuccess)
	for _, person := range hits {
		attrs := map[string][]string{"uid": {person.uid}}
		if len(person.memberOf) > 0 {
			attrs["memberOf"] = append([]string(nil), person.memberOf...)
		}
		w.Write(r.NewSearchResponseEntry(person.dn, gldap.WithAttributes(attrs)))
	}
}

func patchSettings(t *testing.T, client *http.Client, base, token, body string) *http.Response {
	t.Helper()
	return do(t, client, http.MethodPatch, base+"/api/v1/admin/settings", token, body)
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustAccount(t *testing.T, client *http.Client, base, token, username string) Account {
	t.Helper()
	res := do(t, client, http.MethodGet, base+"/api/v1/admin/users", token, "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("users: %d %s", res.StatusCode, body)
	}
	var payload struct {
		Users []Account `json:"users"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	for _, acct := range payload.Users {
		if strings.EqualFold(acct.Username, username) {
			return acct
		}
	}
	t.Fatalf("missing account %s in %s", username, body)
	return Account{}
}

func TestDirectoryLogin(t *testing.T) {
	dir := startDirectory(t)
	ts, _, _ := testServer(t)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)

	res := do(t, client, http.MethodGet, ts.URL+"/api/v1/admin/settings", admin, "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"ldap_enabled":false`) || !strings.Contains(body, `"ldap_user_filter":"(uid=%s)"`) || !strings.Contains(body, `"ldap_bind_password_set":false`) || strings.Contains(body, `"ldap_bind_password"`) {
		t.Fatalf("default directory settings: %d %s", res.StatusCode, body)
	}
	for _, bad := range []string{
		`{"ldap_enabled":true}`,
		`{"ldap_url":"https://example.com"}`,
		`{"ldap_url":"ldap://cn=a:secret@127.0.0.1:9"}`,
		`{"ldap_user_filter":"(uid=*)"}`,
		`{"ldap_user_filter":"(uid=%s)(cn=%s)"}`,
		`{"ldap_username_attr":"uid!"}`,
		`{"ldap_bind_dn":"cn=<admins>"}`,
		`{"ldap_starttls":true,"ldap_url":"ldaps://127.0.0.1:9"}`,
		`{"ldap_bind_password":"new-secret","ldap_clear_bind_password":true}`,
	} {
		res = patchSettings(t, client, ts.URL, admin, bad)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("accepted %s: %d", bad, res.StatusCode)
		}
	}

	raw, err := json.Marshal(map[string]any{
		"ldap_enabled":       true,
		"ldap_url":           dir.url,
		"ldap_bind_dn":       readerDN,
		"ldap_bind_password": readerSecret,
		"ldap_base_dn":       peopleBase,
		"ldap_user_filter":   "(uid=%s)",
		"ldap_username_attr": "uid",
		"ldap_admin_group":   adminGroupDN,
	})
	if err != nil {
		t.Fatal(err)
	}
	res = patchSettings(t, client, ts.URL, admin, string(raw))
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || strings.Contains(body, readerSecret) || !strings.Contains(body, `"ldap_bind_password_set":true`) || strings.Contains(body, `"ldap_bind_password"`) {
		t.Fatalf("save directory: %d %s", res.StatusCode, body)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"admin","password":"directory-admin-secret"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusUnauthorized || dir.bindCount() != 0 {
		t.Fatalf("local admin fell through: %d binds=%d %s", res.StatusCode, dir.bindCount(), body)
	}
	adminAcct := mustAccount(t, client, ts.URL, admin, "admin")
	if adminAcct.Source != SourceLocal || adminAcct.Role != RoleAdmin {
		t.Fatalf("admin changed: %+v", adminAcct)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/users", admin, `{"username":"alice","password":"local-alice-password","role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("local alice: %d", res.StatusCode)
	}
	binds := dir.bindCount()
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"alice","password":"alice-secret"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || dir.bindCount() != binds {
		t.Fatalf("local alice used the directory password: %d binds=%d", res.StatusCode, dir.bindCount())
	}
	localAlice := login(t, client, ts.URL, "alice", "local-alice-password")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", localAlice, "")
	body = readBody(t, res)
	if !strings.Contains(body, `"source":"local"`) {
		t.Fatalf("local alice source: %s", body)
	}
	alice := mustAccount(t, client, ts.URL, admin, "alice")
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/users/"+alice.ID, admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete local alice: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"alice","password":""}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("empty password: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"bad name","password":"alice-secret"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("odd username: %d", res.StatusCode)
	}

	aliceSession := login(t, client, ts.URL, "alice", "alice-secret")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", aliceSession, "")
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"source":"directory"`) || !strings.Contains(body, `"role":"user"`) || !strings.Contains(body, `"all_hosts":true`) {
		t.Fatalf("alice me: %d %s", res.StatusCode, body)
	}
	alice = mustAccount(t, client, ts.URL, admin, "alice")
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/admin/groups", admin, `{"name":"Edge"}`)
	gb := readBody(t, res)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("group: %d %s", res.StatusCode, gb)
	}
	var edge Group
	if err := json.Unmarshal([]byte(gb), &edge); err != nil {
		t.Fatal(err)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+alice.ID, admin, `{"all_hosts":false,"group_ids":["`+edge.ID+`"]}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("scope: %d", res.StatusCode)
	}
	login(t, client, ts.URL, "alice", "alice-secret")
	alice = mustAccount(t, client, ts.URL, admin, "alice")
	if alice.AllHosts || alice.Role != RoleUser || len(alice.GroupIDs) != 1 || alice.GroupIDs[0] != edge.ID || alice.Source != SourceDirectory {
		t.Fatalf("second login reset scope: %+v", alice)
	}

	bobSession := login(t, client, ts.URL, "bob", "bob-secret")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", bobSession, "")
	body = readBody(t, res)
	if !strings.Contains(body, `"role":"admin"`) || !strings.Contains(body, `"source":"directory"`) {
		t.Fatalf("bob: %s", body)
	}
	caraSession := login(t, client, ts.URL, "cara", "cara-secret")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", caraSession, "")
	body = readBody(t, res)
	if !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("cara memberOf: %s", body)
	}
	caseySession := login(t, client, ts.URL, "casey", "casey-secret")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", caseySession, "")
	body = readBody(t, res)
	if !strings.Contains(body, `"username":"Casey"`) {
		t.Fatalf("casey spelling: %s", body)
	}

	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"alice","password":"wrong-password"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "unauthorized") {
		t.Fatalf("bad directory password: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"nobody","password":"nobody-secret"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown directory name: %d", res.StatusCode)
	}

	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/me", aliceSession, `{"current_password":"alice-secret","password":"another-secret-1"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "directory account") {
		t.Fatalf("alice password: %d %s", res.StatusCode, body)
	}
	bob := mustAccount(t, client, ts.URL, admin, "bob")
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+bob.ID, admin, `{"role":"user"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "directory account") {
		t.Fatalf("bob role: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+bob.ID, admin, `{"password":"another-secret-1"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "directory account") {
		t.Fatalf("bob password: %d %s", res.StatusCode, body)
	}

	cara := mustAccount(t, client, ts.URL, admin, "cara")
	res = do(t, client, http.MethodDelete, ts.URL+"/api/v1/admin/users/"+cara.ID, admin, "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete cara: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPatch, ts.URL+"/api/v1/admin/users/"+adminAcct.ID, admin, `{"role":"user"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("demote local admin: %d", res.StatusCode)
	}
	dir.setAdmin("bob", false)
	bobSession = login(t, client, ts.URL, "bob", "bob-secret")
	res = do(t, client, http.MethodGet, ts.URL+"/api/v1/me", bobSession, "")
	body = readBody(t, res)
	if !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("last admin was demoted: %s", body)
	}

	res = patchSettings(t, client, ts.URL, bobSession, `{"brand_name":"Lab Logs"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"ldap_bind_password_set":true`) || strings.Contains(body, readerSecret) {
		t.Fatalf("brand save dropped the bind password: %d %s", res.StatusCode, body)
	}
	login(t, client, ts.URL, "alice", "alice-secret")

	res = patchSettings(t, client, ts.URL, bobSession, `{"ldap_bind_password":"other-secret","ldap_clear_bind_password":true}`)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("set and clear: %d", res.StatusCode)
	}
	login(t, client, ts.URL, "alice", "alice-secret")

	res = patchSettings(t, client, ts.URL, bobSession, `{"ldap_bind_password":""}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"ldap_bind_password_set":true`) {
		t.Fatalf("blank password cleared the secret: %d %s", res.StatusCode, body)
	}

	form := url.Values{"username": {"alice"}, "password": {"wrong-password"}}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?error=1" {
		t.Fatalf("form failure: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	res = patchSettings(t, client, ts.URL, bobSession, `{"ldap_url":"ldap://127.0.0.1:1"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("point at a closed port: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"alice","password":"alice-secret"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "directory unavailable") {
		t.Fatalf("directory down: %d %s", res.StatusCode, body)
	}
	form.Set("password", "alice-secret")
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?error=directory" {
		t.Fatalf("form directory down: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	res = patchSettings(t, client, ts.URL, bobSession, `{"ldap_clear_bind_password":true,"ldap_url":"`+dir.url+`"}`)
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"ldap_bind_password_set":false`) {
		t.Fatalf("clear bind password: %d %s", res.StatusCode, body)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"alice","password":"alice-secret"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("cleared bind password still signed in: %d", res.StatusCode)
	}
}

func TestNobodyLoginDoesNotCreate(t *testing.T) {
	dir := startDirectory(t)
	ts, st, _ := testServer(t)
	client := noRedirect(t)
	admin := login(t, client, ts.URL, "admin", testAdminPassword)
	raw, err := json.Marshal(map[string]any{
		"ldap_enabled":       true,
		"ldap_url":           dir.url,
		"ldap_bind_dn":       readerDN,
		"ldap_bind_password": readerSecret,
		"ldap_base_dn":       peopleBase,
		"ldap_admin_group":   adminGroupDN,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := patchSettings(t, client, ts.URL, admin, string(raw))
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("enable: %d", res.StatusCode)
	}
	res = do(t, client, http.MethodPost, ts.URL+"/api/v1/login", "", `{"username":"nobody","password":"nobody-secret"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown directory user: %d", res.StatusCode)
	}
	if _, err := st.User("usr_missing"); err != ErrNotFound {
		t.Fatal(err)
	}
	users, err := st.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	for _, acct := range users {
		if acct.Username == "nobody" {
			t.Fatalf("created nobody: %+v", acct)
		}
	}
}
