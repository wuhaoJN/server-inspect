package web_test

import (
	"net/http"
	"strconv"
	"testing"

	"serverinspect/internal/store"
)

func TestServerUsernameChangeRequiresPassword(t *testing.T) {
	env := newTestEnv(t)
	client := newClient(t)
	base := env.http.URL

	if status, resp := call(t, client, http.MethodPost, base+"/api/login",
		map[string]string{"username": "admin", "password": "Admin@12345"}, ""); status != http.StatusOK || !resp.OK {
		t.Fatalf("login: status=%d %s", status, resp.Error)
	}

	srv := &store.Server{
		Name: "user-change", Host: "10.1.1.1", Port: 22,
		Username: "root", PasswordEnc: "cipher",
	}
	if err := env.st.CreateServer(srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	path := base + "/api/servers/" + strconv.FormatInt(srv.ID, 10)

	payload := map[string]any{
		"name": "user-change", "host": "10.1.1.1", "port": 22,
		"username": "vendor-nyy-inspect", "password": "",
		"group": "", "nodeRole": "", "tags": "", "note": "",
	}
	if status, resp := call(t, client, http.MethodPut, path, payload, base); status != http.StatusBadRequest {
		t.Fatalf("username change without password status=%d %s, want 400", status, resp.Error)
	}

	payload["serverId"] = srv.ID
	if status, resp := call(t, client, http.MethodPost, base+"/api/servers/test", payload, base); status != http.StatusBadRequest {
		t.Fatalf("test username change without password status=%d %s, want 400", status, resp.Error)
	}

	payload["password"] = "new-password"
	if status, resp := call(t, client, http.MethodPut, path, payload, base); status != http.StatusOK || !resp.OK {
		t.Fatalf("username change with password: status=%d %s", status, resp.Error)
	}
	got, err := env.st.GetServer(srv.ID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got.Username != "vendor-nyy-inspect" {
		t.Fatalf("username = %q", got.Username)
	}
	if got.PasswordEnc == "cipher" || got.PasswordEnc == "" {
		t.Fatalf("password was not re-encrypted: %q", got.PasswordEnc)
	}
}
