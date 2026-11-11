package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"serverinspect/internal/secret"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := readJSON(r, &req); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeFail(w, http.StatusBadRequest, "请输入用户名和密码")
		return
	}

	username, hash, err := s.store.AdminCredentials()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "读取管理员账号失败")
		return
	}
	// 无论用户名是否匹配都执行一次哈希校验，避免通过响应时间枚举用户名。
	passwordOK := secret.VerifyPassword(hash, req.Password)
	if !strings.EqualFold(req.Username, username) || !passwordOK {
		time.Sleep(400 * time.Millisecond)
		writeFail(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	s.issueSession(w, username)
	writeOK(w, map[string]any{"user": username, "version": s.cfg.Version})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	s.clearSession(w)
	writeOK(w, nil)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if user, ok := s.readSession(r); ok {
		writeOK(w, map[string]any{"authenticated": true, "user": user, "version": s.cfg.Version})
		return
	}
	writeOK(w, map[string]any{"authenticated": false, "version": s.cfg.Version})
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	username, ok := s.readSession(r)
	if !ok {
		writeFail(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	var req passwordChangeRequest
	if err := readJSON(r, &req); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.NewPassword) < 8 {
		writeFail(w, http.StatusBadRequest, "新密码至少需要 8 个字符")
		return
	}
	if len([]byte(req.NewPassword)) > 72 {
		writeFail(w, http.StatusBadRequest, "新密码不能超过 72 字节")
		return
	}
	name, hash, err := s.store.AdminCredentials()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "读取管理员账号失败")
		return
	}
	if !strings.EqualFold(username, name) || !secret.VerifyPassword(hash, req.CurrentPassword) {
		time.Sleep(300 * time.Millisecond)
		writeFail(w, http.StatusBadRequest, "当前密码不正确")
		return
	}
	if err := s.store.SetAdminPassword(name, req.NewPassword); err != nil {
		writeFail(w, http.StatusInternalServerError, "保存新密码失败")
		return
	}
	s.log.Info("管理员密码已修改", "user", name)
	writeOK(w, nil)
}

func (s *Server) issueSession(w http.ResponseWriter, username string) {
	expires := time.Now().Add(s.cfg.SessionTTL)
	payload := username + "|" + strconv.FormatInt(expires.Unix(), 10)
	token := s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.cfg.SessionSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) readSession(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	encoded, sig, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, s.cfg.SessionSecret)
	mac.Write(payload)
	if !hmac.Equal(gotSig, mac.Sum(nil)) {
		return "", false
	}
	username, expText, ok := strings.Cut(string(payload), "|")
	if !ok || username == "" {
		return "", false
	}
	expUnix, err := strconv.ParseInt(expText, 10, 64)
	if err != nil || time.Now().Unix() >= expUnix {
		return "", false
	}
	return username, true
}
