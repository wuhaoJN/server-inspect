package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"serverinspect/internal/inspect"
	"serverinspect/internal/runner"
	"serverinspect/internal/scheduler"
	"serverinspect/internal/secret"
	"serverinspect/internal/store"
	"serverinspect/internal/web"
)

type testEnv struct {
	http *httptest.Server
	st   *store.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	key := bytes.Repeat([]byte{7}, secret.KeySize)
	cipher, err := secret.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	if _, err := st.EnsureAdmin("admin", "Admin@12345"); err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := runner.New(st, cipher, runner.Config{
		DialTimeout:    2 * time.Second,
		InspectOptions: inspect.DefaultOptions(),
		Concurrency:    2,
	}, logger)
	sched := scheduler.New(st, run, logger, time.Hour)
	handler := web.New(st, cipher, web.Config{
		Version:        "test",
		SessionSecret:  secret.DeriveKey(key, "session-token"),
		SessionTTL:     time.Hour,
		DialTimeout:    2 * time.Second,
		InspectTimeout: 5 * time.Second,
		InspectOptions: inspect.DefaultOptions(),
	}, logger, run, sched).Handler()
	hs := httptest.NewServer(handler)
	t.Cleanup(func() {
		hs.Close()
		st.Close()
	})
	return &testEnv{http: hs, st: st}
}

func newClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

type apiResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

func call(t *testing.T, client *http.Client, method, rawURL string, body any, origin string) (int, apiResponse) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, rawURL, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer res.Body.Close()
	var payload apiResponse
	_ = json.NewDecoder(res.Body).Decode(&payload)
	return res.StatusCode, payload
}

func TestAuthAndCSRF(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)

	base := env.http.URL

	status, resp := call(t, client, http.MethodGet, base+"/api/session", nil, "")
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("session: status=%d ok=%v", status, resp.OK)
	}
	var session struct {
		Authenticated bool `json:"authenticated"`
	}
	_ = json.Unmarshal(resp.Data, &session)
	if session.Authenticated {
		t.Fatal("should not be authenticated")
	}

	if status, _ := call(t, client, http.MethodGet, base+"/api/servers", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 before login, got %d", status)
	}
	if status, _ := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "wrong"}, ""); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", status)
	}
	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login failed: status=%d %s", status, resp.Error)
	}

	status, resp = call(t, client, http.MethodGet, base+"/api/servers", nil, "")
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("servers after login: status=%d %s", status, resp.Error)
	}

	// 跨站 Origin 的写请求必须被拒绝
	evil := "http://evil.example"
	if status, _ := call(t, client, http.MethodPost, base+"/api/servers", map[string]any{
		"name": "x", "host": "127.0.0.1", "username": "root", "password": "p",
	}, evil); status != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin write, got %d", status)
	}

	// 同源写请求成功，且响应中不得包含密码
	status, resp = call(t, client, http.MethodPost, base+"/api/servers", map[string]any{
		"name": "测试节点", "host": "127.0.0.1", "port": 22, "username": "root",
		"password": "super-secret", "group": "g", "nodeRole": "Web", "tags": "t", "note": "n",
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("create server: status=%d %s", status, resp.Error)
	}
	if strings.Contains(string(resp.Data), "super-secret") {
		t.Fatal("response must not contain plaintext password")
	}
	if strings.Contains(string(resp.Data), "password") {
		t.Fatalf("response should not expose password field: %s", string(resp.Data))
	}

	// 数据库中保存的应是密文
	srv, err := env.st.GetServer(1)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv.PasswordEnc == "" || srv.PasswordEnc == "super-secret" {
		t.Fatalf("password not encrypted: %q", srv.PasswordEnc)
	}

	// 更新时留空密码应保持原密文
	status, resp = call(t, client, http.MethodPut, base+"/api/servers/1", map[string]any{
		"name": "测试节点-改", "host": "127.0.0.1", "port": 22, "username": "root", "nodeRole": "Web",
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("update server: status=%d %s", status, resp.Error)
	}
	updated, _ := env.st.GetServer(1)
	if updated.PasswordEnc != srv.PasswordEnc {
		t.Fatal("empty password should keep existing ciphertext")
	}
	if updated.Name != "测试节点-改" {
		t.Fatalf("name not updated: %q", updated.Name)
	}

	var list []map[string]any
	_, resp = call(t, client, http.MethodGet, base+"/api/servers", nil, "")
	if err := json.Unmarshal(resp.Data, &list); err != nil {
		t.Fatalf("decode servers: %v", err)
	}
	if len(list) != 1 || list[0]["name"] != "测试节点-改" {
		t.Fatalf("unexpected list: %#v", list)
	}
	if list[0]["nodeRole"] != "Web" {
		t.Fatalf("node role not persisted: %#v", list[0])
	}

	if status, resp := call(t, client, http.MethodGet, base+"/api/dashboard", nil, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("dashboard: status=%d %s", status, resp.Error)
	}

	if status, _ := call(t, client, http.MethodDelete, base+"/api/servers/1", nil, base); status != http.StatusOK {
		t.Fatalf("delete server: status=%d", status)
	}

	// 修改管理员密码
	if status, _ := call(t, client, http.MethodPost, base+"/api/password",
		map[string]string{"currentPassword": "wrong", "newPassword": "NewPass123"}, base); status != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong current password, got %d", status)
	}
	if status, _ := call(t, client, http.MethodPost, base+"/api/password",
		map[string]string{"currentPassword": "Admin@12345", "newPassword": "short"}, base); status != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d", status)
	}
	if status, resp := call(t, client, http.MethodPost, base+"/api/password",
		map[string]string{"currentPassword": "Admin@12345", "newPassword": "NewPass123"}, base); status != http.StatusOK || !resp.OK {
		t.Fatalf("change password: status=%d %s", status, resp.Error)
	}

	// 退出后受保护接口应返回 401
	if status, _ := call(t, client, http.MethodPost, base+"/api/logout", nil, base); status != http.StatusOK {
		t.Fatalf("logout: status=%d", status)
	}
	if status, _ := call(t, client, http.MethodGet, base+"/api/servers", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", status)
	}

	// 旧密码不可再登录，新密码可以登录
	if status, _ := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusUnauthorized {
		t.Fatalf("old password should fail, got %d", status)
	}
	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "NewPass123"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login with new password failed: status=%d %s", status, resp.Error)
	}
}

func TestStaticAndSecurityHeaders(t *testing.T) {
	env := newTestEnv(t)
	res, err := http.Get(env.http.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing security headers")
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "/static/app.js") {
		t.Fatalf("unexpected index body: %s", body)
	}

	staticRes, err := http.Get(env.http.URL + "/static/app.js")
	if err != nil {
		t.Fatalf("GET static: %v", err)
	}
	defer staticRes.Body.Close()
	if staticRes.StatusCode != http.StatusOK {
		t.Fatalf("static status = %d", staticRes.StatusCode)
	}

	healthRes, err := http.Get(env.http.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer healthRes.Body.Close()
	healthBody, _ := io.ReadAll(healthRes.Body)
	if healthRes.StatusCode != http.StatusOK || string(healthBody) != "ok" {
		t.Fatalf("healthz: status=%d body=%q", healthRes.StatusCode, healthBody)
	}
}

func TestNetworkScanBulkImportAndConfigure(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)
	base := env.http.URL

	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login: status=%d %s", status, resp.Error)
	}

	// 本地起一个带回 SSH 标识的端口，验证扫描接口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = c.Write([]byte("SSH-2.0-WebTest\r\n"))
				time.Sleep(100 * time.Millisecond)
			}(conn)
		}
	}()
	sshPort := ln.Addr().(*net.TCPAddr).Port

	status, resp := call(t, client, http.MethodPost, base+"/api/scan", map[string]any{
		"cidr": "127.0.0.1/32", "ports": []int{sshPort},
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("scan: status=%d %s", status, resp.Error)
	}
	var scanResult struct {
		Hosts []struct {
			IP        string `json:"ip"`
			Ports     []int  `json:"ports"`
			SSH       bool   `json:"ssh"`
			SSHBanner string `json:"sshBanner"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(resp.Data, &scanResult); err != nil {
		t.Fatalf("decode scan: %v", err)
	}
	if len(scanResult.Hosts) != 1 || !scanResult.Hosts[0].SSH ||
		!strings.Contains(scanResult.Hosts[0].SSHBanner, "WebTest") {
		t.Fatalf("unexpected scan hosts: %#v", scanResult.Hosts)
	}

	// 超范围网段应被拒绝
	if status, _ := call(t, client, http.MethodPost, base+"/api/scan", map[string]any{
		"cidr": "10.0.0.0/8", "ports": []int{22},
	}, base); status != http.StatusBadRequest {
		t.Fatalf("large cidr should be rejected, got %d", status)
	}

	// 批量导入：重复项应被跳过，初始为待配置
	status, resp = call(t, client, http.MethodPost, base+"/api/servers/bulk", map[string]any{
		"group": "扫描发现",
		"tags":  "auto",
		"servers": []map[string]any{
			{"name": "", "host": "10.0.0.1", "port": 22},
			{"name": "node-2", "host": "10.0.0.2", "port": 2222},
			{"name": "", "host": "10.0.0.1", "port": 22},
		},
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("bulk import: status=%d %s", status, resp.Error)
	}
	var bulk struct {
		Created     int      `json:"created"`
		Skipped     []string `json:"skipped"`
		FirstServer struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firstServer"`
	}
	if err := json.Unmarshal(resp.Data, &bulk); err != nil {
		t.Fatalf("decode bulk: %v", err)
	}
	if bulk.Created != 2 || len(bulk.Skipped) != 1 || bulk.FirstServer.ID == 0 {
		t.Fatalf("unexpected bulk result: %#v", bulk)
	}
	if bulk.FirstServer.Name != "10.0.0.1" {
		t.Fatalf("empty name should default to host, got %q", bulk.FirstServer.Name)
	}

	var views []struct {
		ID         int64 `json:"id"`
		Configured bool  `json:"configured"`
	}
	_, resp = call(t, client, http.MethodGet, base+"/api/servers", nil, "")
	if err := json.Unmarshal(resp.Data, &views); err != nil {
		t.Fatalf("decode servers: %v", err)
	}
	if len(views) != 2 || views[0].Configured || views[1].Configured {
		t.Fatalf("bulk servers should be pending: %#v", views)
	}

	pendingID := bulk.FirstServer.ID
	if status, resp := call(t, client, http.MethodPost,
		base+"/api/servers/"+strconv.FormatInt(pendingID, 10)+"/inspect", nil, base); status != http.StatusBadRequest {
		t.Fatalf("inspect pending server should be 400, got %d (%s)", status, resp.Error)
	}

	// 未配置密码时不允许保存
	if status, _ := call(t, client, http.MethodPut,
		base+"/api/servers/"+strconv.FormatInt(pendingID, 10), map[string]any{
			"name": "10.0.0.1", "host": "10.0.0.1", "port": 22, "username": "root",
		}, base); status != http.StatusBadRequest {
		t.Fatalf("update pending without password should be 400, got %d", status)
	}

	// 手动配置登录方式
	status, resp = call(t, client, http.MethodPut,
		base+"/api/servers/"+strconv.FormatInt(pendingID, 10), map[string]any{
			"name": "10.0.0.1", "host": "10.0.0.1", "port": 22,
			"username": "root", "password": "secret", "group": "扫描发现",
		}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("configure pending server: status=%d %s", status, resp.Error)
	}

	_, resp = call(t, client, http.MethodGet, base+"/api/servers", nil, "")
	views = nil
	if err := json.Unmarshal(resp.Data, &views); err != nil {
		t.Fatalf("decode servers: %v", err)
	}
	configured := 0
	for _, v := range views {
		if v.Configured {
			configured++
		}
	}
	if configured != 1 {
		t.Fatalf("configured count = %d, views = %#v", configured, views)
	}

	// 修改主机地址或端口时必须重新输入密码，禁止把旧凭据发往新目标。
	if status, _ := call(t, client, http.MethodPost, base+"/api/servers/test", map[string]any{
		"name": "10.0.0.1", "host": "10.0.0.2", "port": 22, "username": "root", "serverId": pendingID,
	}, base); status != http.StatusBadRequest {
		t.Fatalf("test with changed host should require password, got %d", status)
	}
	if status, _ := call(t, client, http.MethodPut,
		base+"/api/servers/"+strconv.FormatInt(pendingID, 10), map[string]any{
			"name": "10.0.0.1", "host": "10.0.0.2", "port": 22, "username": "root",
		}, base); status != http.StatusBadRequest {
		t.Fatalf("update with changed host should require password, got %d", status)
	}

	// 再次导入相同主机应全部跳过
	status, resp = call(t, client, http.MethodPost, base+"/api/servers/bulk", map[string]any{
		"servers": []map[string]any{{"host": "10.0.0.1", "port": 22}},
	}, base)
	if status != http.StatusOK || !resp.OK {
		t.Fatalf("bulk duplicate: status=%d %s", status, resp.Error)
	}
	bulk = struct {
		Created     int      `json:"created"`
		Skipped     []string `json:"skipped"`
		FirstServer struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"firstServer"`
	}{}
	_ = json.Unmarshal(resp.Data, &bulk)
	if bulk.Created != 0 || len(bulk.Skipped) != 1 {
		t.Fatalf("duplicate import should be skipped: %#v", bulk)
	}
}

func TestLoginPageRejectsBadOrigin(t *testing.T) {
	env := newTestEnv(t)
	// 模拟与 Host 不同的 Origin
	u, _ := url.Parse(env.http.URL)
	evil := "http://" + u.Host + ".evil.example"
	client := newClient(t)
	if status, _ := call(t, client, http.MethodPost, env.http.URL+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, evil); status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", status)
	}
}
