package web

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"serverinspect/internal/inspect"
	"serverinspect/internal/runner"
	"serverinspect/internal/scheduler"
	"serverinspect/internal/secret"
	"serverinspect/internal/store"
)

//go:embed static
var staticFiles embed.FS

const sessionCookie = "si_session"

// Config 是 Web 服务配置。
type Config struct {
	Version          string
	SessionSecret    []byte
	SessionTTL       time.Duration
	CookieSecure     bool
	DialTimeout      time.Duration
	InspectTimeout   time.Duration
	ScanTimeout      time.Duration
	SkipHostKeyCheck bool
	InspectOptions   inspect.Options
}

// Server 承载 HTTP 路由与依赖。
type Server struct {
	store     *store.Store
	cipher    *secret.Cipher
	cfg       Config
	mux       *http.ServeMux
	log       *slog.Logger
	runner    *runner.Runner
	scheduler *scheduler.Scheduler
	scanSem   chan struct{}
}

// New 构造 Web 服务。
func New(st *store.Store, cipher *secret.Cipher, cfg Config, logger *slog.Logger, run *runner.Runner, sched *scheduler.Scheduler) *Server {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.InspectTimeout <= 0 {
		cfg.InspectTimeout = 3 * time.Minute
	}
	if cfg.ScanTimeout <= 0 {
		cfg.ScanTimeout = 2 * time.Minute
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}
	s := &Server{
		store:     st,
		cipher:    cipher,
		cfg:       cfg,
		mux:       http.NewServeMux(),
		log:       logger,
		runner:    run,
		scheduler: sched,
		scanSem:   make(chan struct{}, 1),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/session", s.handleSession)
	s.mux.HandleFunc("POST /api/password", s.authed(s.handlePasswordChange))

	s.mux.HandleFunc("GET /api/dashboard", s.authed(s.handleDashboard))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok"))
	})

	s.mux.HandleFunc("GET /api/servers", s.authed(s.handleServersList))
	s.mux.HandleFunc("GET /api/servers/{id}", s.authed(s.handleServerDetail))
	s.mux.HandleFunc("GET /api/servers/{id}/trends", s.authed(s.handleServerTrends))
	s.mux.HandleFunc("POST /api/servers", s.authed(s.handleServerCreate))
	s.mux.HandleFunc("POST /api/servers/test", s.authed(s.handleServerTest))
	s.mux.HandleFunc("POST /api/servers/bulk", s.authed(s.handleServerBulkCreate))
	s.mux.HandleFunc("POST /api/scan", s.authed(s.handleNetworkScan))
	s.mux.HandleFunc("PUT /api/servers/{id}", s.authed(s.handleServerUpdate))
	s.mux.HandleFunc("DELETE /api/servers/{id}", s.authed(s.handleServerDelete))
	s.mux.HandleFunc("POST /api/servers/{id}/inspect", s.authed(s.handleServerInspect))
	s.mux.HandleFunc("POST /api/servers/{id}/trust", s.authed(s.handleServerTrust))

	s.mux.HandleFunc("GET /api/inspections", s.authed(s.handleInspectionsList))
	s.mux.HandleFunc("GET /api/inspections/{id}", s.authed(s.handleInspectionDetail))
	s.mux.HandleFunc("GET /api/inspections/{id}/compare", s.authed(s.handleInspectionCompare))
	s.mux.HandleFunc("DELETE /api/inspections/{id}", s.authed(s.handleInspectionDelete))

	s.mux.HandleFunc("GET /api/batches", s.authed(s.handleBatchesList))
	s.mux.HandleFunc("POST /api/batches", s.authed(s.handleBatchCreate))
	s.mux.HandleFunc("GET /api/batches/{id}", s.authed(s.handleBatchDetail))

	s.mux.HandleFunc("GET /api/schedules", s.authed(s.handleSchedulesList))
	s.mux.HandleFunc("POST /api/schedules", s.authed(s.handleScheduleCreate))
	s.mux.HandleFunc("PUT /api/schedules/{id}", s.authed(s.handleScheduleUpdate))
	s.mux.HandleFunc("DELETE /api/schedules/{id}", s.authed(s.handleScheduleDelete))
	s.mux.HandleFunc("POST /api/schedules/{id}/run", s.authed(s.handleScheduleRun))

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	// 前端资源随二进制升级而更新，禁止浏览器强缓存，避免升级后仍加载旧 JS/CSS。
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})))
	s.mux.HandleFunc("GET /", s.handleIndex)
}

// Handler 返回带安全响应头、CSRF 校验与访问日志的 http.Handler。
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.securityHeaders(h)
	h = s.accessLog(h)
	return h
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "前端资源缺失", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")

		// 对写操作做同源校验，配合 SameSite Cookie 防御 CSRF。
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeFail(w, http.StatusForbidden, "跨站请求被拒绝")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if strings.HasPrefix(r.URL.Path, "/static/") {
			level = slog.LevelDebug
		}
		s.log.Log(r.Context(), level, "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Millisecond).String(),
			"remote", r.RemoteAddr,
		)
	})
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.readSession(r); !ok {
			writeFail(w, http.StatusUnauthorized, "登录已过期，请重新登录")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func writeFail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(dst)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
