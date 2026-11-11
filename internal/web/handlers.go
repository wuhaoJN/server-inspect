package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"serverinspect/internal/batch"
	"serverinspect/internal/compare"
	"serverinspect/internal/inspect"
	"serverinspect/internal/netscan"
	"serverinspect/internal/scheduler"
	"serverinspect/internal/sshx"
	"serverinspect/internal/store"
)

// ---------- 服务器 ----------

type serverInput struct {
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Group        string `json:"group"`
	NodeRole     string `json:"nodeRole"`
	Tags         string `json:"tags"`
	Note         string `json:"note"`
	ServerID     int64  `json:"serverId"`
	ResetHostKey bool   `json:"resetHostKey"`
}

func (in *serverInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.Group = strings.TrimSpace(in.Group)
	in.NodeRole = strings.TrimSpace(in.NodeRole)
	in.Tags = strings.TrimSpace(in.Tags)
	in.Note = strings.TrimSpace(in.Note)
	in.Host = strings.TrimPrefix(in.Host, "ssh://")
	if in.Port == 0 {
		in.Port = 22
	}
	switch {
	case in.Name == "":
		return errors.New("服务器名称不能为空")
	case in.Host == "":
		return errors.New("主机地址不能为空")
	case strings.ContainsAny(in.Host, " \t\r\n"):
		return errors.New("主机地址不能包含空白字符")
	case len(in.Host) > 255:
		return errors.New("主机地址过长")
	case in.Port < 1 || in.Port > 65535:
		return errors.New("端口必须在 1-65535 之间")
	case in.Username == "":
		return errors.New("登录用户名不能为空")
	case len(in.Name) > 100:
		return errors.New("服务器名称不能超过 100 个字符")
	case len(in.Group) > 50:
		return errors.New("分组名称不能超过 50 个字符")
	case len(in.NodeRole) > 50:
		return errors.New("节点职责不能超过 50 个字符")
	case len(in.Tags) > 200:
		return errors.New("标签不能超过 200 个字符")
	case len(in.Note) > 500:
		return errors.New("备注不能超过 500 个字符")
	}
	return nil
}

func (s *Server) handleServersList(w http.ResponseWriter, _ *http.Request) {
	views, err := s.store.ListServerViews()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询服务器列表失败: "+err.Error())
		return
	}
	writeOK(w, views)
}

func (s *Server) handleServerCreate(w http.ResponseWriter, r *http.Request) {
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := in.normalize(); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Password == "" {
		writeFail(w, http.StatusBadRequest, "登录密码不能为空")
		return
	}
	enc, err := s.cipher.Encrypt(in.Password)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "加密登录口令失败")
		return
	}
	srv := &store.Server{
		Name: in.Name, Host: in.Host, Port: in.Port, Username: in.Username,
		PasswordEnc: enc, Group: in.Group, NodeRole: in.NodeRole, Tags: in.Tags, Note: in.Note,
	}
	if err := s.store.CreateServer(srv); err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("新增服务器", "id", srv.ID, "name", srv.Name, "host", srv.Host)
	writeOK(w, srv)
}

const maxBulkServers = 4096

type bulkServerItem struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type bulkServerRequest struct {
	Servers []bulkServerItem `json:"servers"`
	Group   string           `json:"group"`
	Tags    string           `json:"tags"`
}

// handleServerBulkCreate 将网络扫描选中的主机批量加入列表，初始不带登录凭据。
func (s *Server) handleServerBulkCreate(w http.ResponseWriter, r *http.Request) {
	var req bulkServerRequest
	if err := readJSON(r, &req); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.Servers) == 0 {
		writeFail(w, http.StatusBadRequest, "请至少选择一台服务器")
		return
	}
	if len(req.Servers) > maxBulkServers {
		writeFail(w, http.StatusBadRequest, fmt.Sprintf("单次最多加入 %d 台服务器", maxBulkServers))
		return
	}
	req.Group = strings.TrimSpace(req.Group)
	req.Tags = strings.TrimSpace(req.Tags)
	if len(req.Group) > 50 {
		writeFail(w, http.StatusBadRequest, "分组名称不能超过 50 个字符")
		return
	}
	if len(req.Tags) > 200 {
		writeFail(w, http.StatusBadRequest, "标签不能超过 200 个字符")
		return
	}

	existing, err := s.store.ListServers()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询服务器列表失败: "+err.Error())
		return
	}
	seen := make(map[string]bool, len(existing)+len(req.Servers))
	for _, srv := range existing {
		seen[hostPortKey(srv.Host, srv.Port)] = true
	}

	toCreate := make([]*store.Server, 0, len(req.Servers))
	skipped := make([]string, 0)
	for i, item := range req.Servers {
		host := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item.Host), "ssh://"))
		name := strings.TrimSpace(item.Name)
		port := item.Port
		if port == 0 {
			port = 22
		}
		switch {
		case host == "":
			writeFail(w, http.StatusBadRequest, fmt.Sprintf("第 %d 项主机地址为空", i+1))
			return
		case strings.ContainsAny(host, " \t\r\n"):
			writeFail(w, http.StatusBadRequest, fmt.Sprintf("第 %d 项主机地址包含空白字符", i+1))
			return
		case len(host) > 255:
			writeFail(w, http.StatusBadRequest, fmt.Sprintf("第 %d 项主机地址过长", i+1))
			return
		case port < 1 || port > 65535:
			writeFail(w, http.StatusBadRequest, fmt.Sprintf("第 %d 项端口无效", i+1))
			return
		case len(name) > 100:
			// 反向解析可能返回较长域名，截断到字段上限。
			runes := []rune(name)
			if len(runes) > 100 {
				runes = runes[:100]
			}
			name = string(runes)
		}
		if name == "" {
			name = host
		}
		key := hostPortKey(host, port)
		if seen[key] {
			skipped = append(skipped, fmt.Sprintf("%s:%d", host, port))
			continue
		}
		seen[key] = true
		toCreate = append(toCreate, &store.Server{
			Name: name,
			Host: host,
			Port: port,
			// 用户名与密码留空，由用户在网络扫描后手动配置登录方式。
			Group: req.Group,
			Tags:  req.Tags,
		})
	}

	if len(toCreate) > 0 {
		if err := s.store.CreateServers(toCreate); err != nil {
			writeFail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.log.Info("批量导入扫描结果", "created", len(toCreate), "skipped", len(skipped))
	payload := map[string]any{
		"created": len(toCreate),
		"skipped": skipped,
	}
	if len(toCreate) > 0 {
		// 仅返回第一台，便于前端立即打开配置弹窗；避免大批量导入时响应体过大。
		payload["firstServer"] = toCreate[0]
	}
	writeOK(w, payload)
}

func hostPortKey(host string, port int) string {
	return strings.ToLower(strings.TrimSpace(host)) + ":" + strconv.Itoa(port)
}

// handleNetworkScan 对指定网段进行只读 TCP 探测，返回开放端口与 SSH 标识。
func (s *Server) handleNetworkScan(w http.ResponseWriter, r *http.Request) {
	select {
	case s.scanSem <- struct{}{}:
		defer func() { <-s.scanSem }()
	default:
		writeFail(w, http.StatusConflict, "已有扫描任务正在进行，请稍后再试")
		return
	}

	var req struct {
		CIDR  string `json:"cidr"`
		Ports []int  `json:"ports"`
	}
	if err := readJSON(r, &req); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ScanTimeout)
	defer cancel()
	result, err := netscan.Scan(ctx, req.CIDR, req.Ports, netscan.Options{ResolveNames: true})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeFail(w, http.StatusGatewayTimeout, "扫描超时，请缩小网段或减少端口后重试")
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("网络扫描完成", "cidr", result.CIDR, "hosts", len(result.Hosts),
		"durationMs", result.DurationMS)
	writeOK(w, result)
}

func (s *Server) handleServerUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "服务器 ID 无效")
		return
	}
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := in.normalize(); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.store.GetServer(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}

	passwordEnc := existing.PasswordEnc
	targetChanged := existing.Host != in.Host || existing.Port != in.Port
	if in.Password != "" {
		passwordEnc, err = s.cipher.Encrypt(in.Password)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "加密登录口令失败")
			return
		}
	} else if existing.PasswordEnc == "" {
		writeFail(w, http.StatusBadRequest, "请填写登录密码（当前仅支持用户名 + 密码认证）")
		return
	} else if targetChanged {
		// 避免把已保存的旧凭据静默沿用到一个新的主机/端口。
		writeFail(w, http.StatusBadRequest, "修改了主机地址或端口，请重新输入登录密码")
		return
	}
	hostKey := existing.HostKey
	if in.ResetHostKey || existing.Host != in.Host || existing.Port != in.Port {
		hostKey = ""
	}

	updated := &store.Server{
		ID: id, Name: in.Name, Host: in.Host, Port: in.Port, Username: in.Username,
		PasswordEnc: passwordEnc, HostKey: hostKey, Group: in.Group, NodeRole: in.NodeRole, Tags: in.Tags, Note: in.Note,
	}
	if err := s.store.UpdateServer(updated); err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("更新服务器", "id", id, "name", updated.Name)
	writeOK(w, updated)
}

func (s *Server) handleServerDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "服务器 ID 无效")
		return
	}
	err := s.store.DeleteServer(id)
	if errors.Is(err, store.ErrNotFound) {
		writeFail(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("删除服务器", "id", id)
	writeOK(w, nil)
}

func (s *Server) handleServerTest(w http.ResponseWriter, r *http.Request) {
	var in serverInput
	if err := readJSON(r, &in); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := in.normalize(); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}

	var existing *store.Server
	if in.ServerID > 0 {
		if srv, err := s.store.GetServer(in.ServerID); err == nil {
			existing = srv
		}
	}
	sameTarget := existing != nil && existing.Host == in.Host && existing.Port == in.Port
	if in.Password == "" {
		if existing == nil {
			writeFail(w, http.StatusBadRequest, "请输入登录密码")
			return
		}
		if !sameTarget {
			writeFail(w, http.StatusBadRequest, "修改了主机地址或端口，请重新输入登录密码")
			return
		}
		password, err := s.cipher.Decrypt(existing.PasswordEnc)
		if err != nil {
			writeFail(w, http.StatusInternalServerError, "解密已保存的登录口令失败")
			return
		}
		in.Password = password
	}
	if in.Password == "" {
		writeFail(w, http.StatusBadRequest, "该服务器尚未配置登录方式，请先填写用户名和密码")
		return
	}

	knownHostKey := ""
	if sameTarget {
		knownHostKey = existing.HostKey
	}
	if s.cfg.SkipHostKeyCheck {
		knownHostKey = ""
	}

	started := time.Now()
	res, err := sshx.Dial(sshx.DialOptions{
		Host: in.Host, Port: in.Port, Username: in.Username, Password: in.Password,
		Timeout:      s.cfg.DialTimeout,
		KnownHostKey: knownHostKey,
		TrustHostKey: in.ResetHostKey || s.cfg.SkipHostKeyCheck,
	})
	if err != nil {
		writeFail(w, http.StatusBadRequest, "连接失败："+err.Error())
		return
	}
	defer res.Client.Close()
	latency := time.Since(started)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	output, probeErr := res.Client.Probe(ctx)
	if probeErr != nil {
		writeFail(w, http.StatusBadRequest, "已连接但无法执行命令："+probeErr.Error())
		return
	}

	saved := false
	if sameTarget && res.Fingerprint != "" && (existing.HostKey == "" || in.ResetHostKey) {
		if err := s.store.SetHostKey(existing.ID, res.Fingerprint); err == nil {
			saved = true
		}
	}
	writeOK(w, map[string]any{
		"message":      "连接成功",
		"latencyMs":    latency.Milliseconds(),
		"fingerprint":  res.Fingerprint,
		"output":       output,
		"hostKeySaved": saved,
	})
}

func (s *Server) handleServerTrust(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "服务器 ID 无效")
		return
	}
	srv, err := s.store.GetServer(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	password, err := s.cipher.Decrypt(srv.PasswordEnc)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "解密登录口令失败")
		return
	}
	if srv.Username == "" || password == "" {
		writeFail(w, http.StatusBadRequest, "该服务器尚未配置登录方式，请先填写用户名和密码")
		return
	}
	res, err := sshx.Dial(sshx.DialOptions{
		Host: srv.Host, Port: srv.Port, Username: srv.Username,
		Password: password, Timeout: s.cfg.DialTimeout, TrustHostKey: true,
	})
	if err != nil {
		writeFail(w, http.StatusBadRequest, "连接失败："+err.Error())
		return
	}
	defer res.Client.Close()
	if res.Fingerprint == "" {
		writeFail(w, http.StatusInternalServerError, "未能获取主机密钥指纹")
		return
	}
	if err := s.store.SetHostKey(srv.ID, res.Fingerprint); err != nil {
		writeFail(w, http.StatusInternalServerError, "保存主机密钥指纹失败: "+err.Error())
		return
	}
	s.log.Info("重新信任主机密钥", "id", srv.ID, "fingerprint", res.Fingerprint)
	writeOK(w, map[string]any{"fingerprint": res.Fingerprint})
}

// ---------- 巡检 ----------

func (s *Server) handleServerInspect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "服务器 ID 无效")
		return
	}
	srv, err := s.store.GetServer(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if srv.Username == "" || srv.PasswordEnc == "" {
		writeFail(w, http.StatusBadRequest, "该服务器尚未配置登录方式，请先点击「配置登录」填写用户名和密码")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.InspectTimeout)
	defer cancel()
	rec, report, err := s.runner.Run(ctx, srv)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "巡检执行失败: "+err.Error())
		return
	}
	writeOK(w, map[string]any{"inspection": rec, "report": report})
}

// ---------- 巡检记录 / 概览 ----------

func (s *Server) handleInspectionsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.InspectionFilter{
		Status: strings.TrimSpace(q.Get("status")),
	}
	if v := q.Get("serverId"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.ServerID = id
		}
	}
	if v := q.Get("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}
	if v := q.Get("offset"); v != "" {
		filter.Offset, _ = strconv.Atoi(v)
	}
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 20
	}

	items, err := s.store.ListInspections(filter)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询巡检记录失败: "+err.Error())
		return
	}
	total, err := s.store.CountInspections(filter)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "统计巡检记录失败: "+err.Error())
		return
	}
	writeOK(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (s *Server) handleInspectionDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "巡检记录 ID 无效")
		return
	}
	rec, err := s.store.GetInspection(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "巡检记录不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{"inspection": rec}
	if rec.ReportJSON != "" {
		resp["report"] = json.RawMessage(rec.ReportJSON)
	}
	writeOK(w, resp)
}

func (s *Server) handleInspectionDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "巡检记录 ID 无效")
		return
	}
	err := s.store.DeleteInspection(id)
	if errors.Is(err, store.ErrNotFound) {
		writeFail(w, http.StatusNotFound, "巡检记录不存在")
		return
	}
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, nil)
}

func (s *Server) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	views, err := s.store.ListServerViews()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询服务器失败: "+err.Error())
		return
	}
	data := store.DashboardData{
		ServerCount:  len(views),
		Servers:      views,
		StatusCounts: map[string]int{},
	}
	for _, v := range views {
		switch v.LastStatus {
		case "warning", "critical", "error":
			data.IssueCount++
		case "":
			data.UninspectedCount++
		}
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if data.TodayCount, err = s.store.CountInspectionsSince(today); err != nil {
		s.log.Warn("统计今日巡检次数失败", "error", err)
	}
	weekAgo := now.Add(-7 * 24 * time.Hour)
	if data.AvgScore, err = s.store.AvgScoreSince(weekAgo); err != nil {
		s.log.Warn("统计平均健康分失败", "error", err)
	}
	if data.StatusCounts, err = s.store.StatusCountsSince(weekAgo); err != nil {
		s.log.Warn("统计巡检状态分布失败", "error", err)
	}
	if data.Recent, err = s.store.RecentInspections(10); err != nil {
		s.log.Warn("查询最近巡检记录失败", "error", err)
	}
	writeOK(w, data)
}

// ---------- 服务器详情 / 趋势 ----------

func (s *Server) handleServerDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "服务器 ID 无效")
		return
	}
	view, err := s.store.GetServerView(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, view)
}

type trendPoint struct {
	ID          int64     `json:"id"`
	StartedAt   time.Time `json:"startedAt"`
	Status      string    `json:"status"`
	Score       int       `json:"score"`
	CPUPercent  float64   `json:"cpuPercent"`
	MemPercent  float64   `json:"memPercent"`
	DiskPercent float64   `json:"diskPercent"`
	Load1       float64   `json:"load1"`
}

func (s *Server) handleServerTrends(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "服务器 ID 无效")
		return
	}
	if _, err := s.store.GetServer(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "服务器不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	points, err := s.store.ListTrendPoints(id, since, 1000)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询趋势失败: "+err.Error())
		return
	}
	out := make([]trendPoint, 0, len(points))
	for _, p := range points {
		var metrics inspect.Metrics
		if p.MetricsJSON != "" {
			_ = json.Unmarshal([]byte(p.MetricsJSON), &metrics)
		}
		out = append(out, trendPoint{
			ID: p.ID, StartedAt: p.StartedAt, Status: p.Status, Score: p.Score,
			CPUPercent: metrics.CPUPercent, MemPercent: metrics.MemPercent,
			DiskPercent: metrics.DiskPercent, Load1: metrics.Load1,
		})
	}
	writeOK(w, map[string]any{"days": days, "points": out})
}

func (s *Server) handleInspectionCompare(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "巡检记录 ID 无效")
		return
	}
	current, err := s.store.GetInspection(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "巡检记录不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current.ReportJSON == "" {
		writeOK(w, map[string]any{
			"available": false, "currentId": current.ID,
			"currentStatus": current.Status, "currentScore": current.Score,
		})
		return
	}
	var currentReport inspect.Report
	if err := json.Unmarshal([]byte(current.ReportJSON), &currentReport); err != nil {
		writeFail(w, http.StatusInternalServerError, "解析当前巡检报告失败")
		return
	}
	previous, err := s.store.PreviousInspection(current.ServerID, current.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeOK(w, map[string]any{
				"available": false, "currentId": current.ID,
				"currentStatus": current.Status, "currentScore": current.Score,
			})
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	var previousReport inspect.Report
	if err := json.Unmarshal([]byte(previous.ReportJSON), &previousReport); err != nil {
		writeOK(w, map[string]any{
			"available": false, "currentId": current.ID,
			"currentStatus": current.Status, "currentScore": current.Score,
		})
		return
	}
	writeOK(w, compare.Build(current, currentReport, previous, previousReport))
}

// ---------- 批量记录 ----------

const maxBatchItems = 4096

type batchCreateInput struct {
	InspectionIDs []int64         `json:"inspectionIds"`
	Failures      []batch.Failure `json:"failures"`
	Skipped       int             `json:"skipped"`
	ElapsedMS     int64           `json:"elapsedMs"`
}

func (s *Server) handleBatchesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	items, err := s.store.ListBatches(limit, offset)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询批量记录失败: "+err.Error())
		return
	}
	total, err := s.store.CountBatches()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "统计批量记录失败: "+err.Error())
		return
	}
	writeOK(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleBatchCreate(w http.ResponseWriter, r *http.Request) {
	var req batchCreateInput
	if err := readJSON(r, &req); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(req.InspectionIDs)+len(req.Failures) == 0 {
		writeFail(w, http.StatusBadRequest, "批量记录不能为空")
		return
	}
	if len(req.InspectionIDs)+len(req.Failures) > maxBatchItems {
		writeFail(w, http.StatusBadRequest, fmt.Sprintf("单次批量记录最多 %d 台", maxBatchItems))
		return
	}
	if req.Skipped < 0 {
		req.Skipped = 0
	}
	if req.ElapsedMS < 0 {
		req.ElapsedMS = 0
	}

	records := make([]*store.Inspection, 0, len(req.InspectionIDs))
	ids := make([]int64, 0, len(req.InspectionIDs))
	seen := map[int64]bool{}
	for _, id := range req.InspectionIDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		rec, err := s.store.GetInspection(id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeFail(w, http.StatusBadRequest, fmt.Sprintf("巡检记录 %d 不存在", id))
				return
			}
			writeFail(w, http.StatusInternalServerError, err.Error())
			return
		}
		records = append(records, rec)
		ids = append(ids, id)
	}

	servers, err := s.store.ListServers()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	serverMap := make(map[int64]*store.Server, len(servers))
	for i := range servers {
		serverMap[servers[i].ID] = &servers[i]
	}

	started := time.Now().UTC()
	finished := time.Now().UTC()
	if len(records) > 0 {
		started = records[0].StartedAt
		finished = records[0].FinishedAt
		for _, rec := range records[1:] {
			if rec.StartedAt.Before(started) {
				started = rec.StartedAt
			}
			if rec.FinishedAt.After(finished) {
				finished = rec.FinishedAt
			}
		}
	}
	if req.ElapsedMS == 0 {
		req.ElapsedMS = finished.Sub(started).Milliseconds()
		if req.ElapsedMS < 0 {
			req.ElapsedMS = 0
		}
	}

	summary := batch.Build(records, serverMap, req.Failures, req.Skipped, req.ElapsedMS)
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "生成批量分析失败")
		return
	}
	b := &store.Batch{
		Trigger:       "manual",
		Name:          "手动批量",
		StartedAt:     started,
		FinishedAt:    finished,
		ServerCount:   len(records) + len(req.Failures),
		OKCount:       summary.Counts["ok"],
		WarningCount:  summary.Counts["warning"],
		CriticalCount: summary.Counts["critical"],
		ErrorCount:    summary.Counts["error"],
		IssueCount:    summary.IssueCount,
		AvgScore:      summary.AvgScore,
		Skipped:       req.Skipped,
		ElapsedMS:     req.ElapsedMS,
		SummaryJSON:   string(summaryJSON),
	}
	if err := s.store.CreateBatch(b, ids); err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("保存批量巡检记录", "batch", b.ID, "servers", b.ServerCount, "issues", b.IssueCount)
	writeOK(w, map[string]any{"batch": b, "summary": json.RawMessage(summaryJSON)})
}

func (s *Server) handleBatchDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "批量记录 ID 无效")
		return
	}
	b, err := s.store.GetBatch(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "批量记录不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	summary := json.RawMessage(b.SummaryJSON)
	if len(summary) == 0 {
		summary = json.RawMessage("{}")
	}
	writeOK(w, map[string]any{"batch": b, "summary": summary})
}

// ---------- 定时计划 ----------

type scheduleInput struct {
	Name            string  `json:"name"`
	Enabled         bool    `json:"enabled"`
	ScopeType       string  `json:"scopeType"`
	ScopeValue      string  `json:"scopeValue"`
	FreqType        string  `json:"freqType"`
	IntervalMinutes int     `json:"intervalMinutes"`
	AtHour          int     `json:"atHour"`
	AtMinute        int     `json:"atMinute"`
	Weekday         int     `json:"weekday"`
	ServerIDs       []int64 `json:"serverIds"`
}

func (in *scheduleInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.ScopeType = strings.ToLower(strings.TrimSpace(in.ScopeType))
	in.ScopeValue = strings.TrimSpace(in.ScopeValue)
	in.FreqType = strings.ToLower(strings.TrimSpace(in.FreqType))
	switch {
	case in.Name == "":
		return errors.New("计划名称不能为空")
	case len(in.Name) > 100:
		return errors.New("计划名称不能超过 100 个字符")
	}
	switch in.ScopeType {
	case "all":
		in.ScopeValue = ""
	case "group":
		if in.ScopeValue == "" {
			return errors.New("请选择或填写分组")
		}
		if len(in.ScopeValue) > 50 {
			return errors.New("分组名称不能超过 50 个字符")
		}
	case "custom":
		if len(in.ServerIDs) == 0 {
			return errors.New("请选择至少一台服务器")
		}
		if len(in.ServerIDs) > maxBatchItems {
			return errors.New("自选服务器数量过多")
		}
		in.ScopeValue = ""
	default:
		return errors.New("计划范围无效")
	}
	switch in.FreqType {
	case "interval":
		if in.IntervalMinutes < 5 || in.IntervalMinutes > 10080 {
			return errors.New("执行间隔必须在 5-10080 分钟之间")
		}
	case "daily":
		if in.AtHour < 0 || in.AtHour > 23 || in.AtMinute < 0 || in.AtMinute > 59 {
			return errors.New("每天执行时间无效")
		}
	case "weekly":
		if in.Weekday < 0 || in.Weekday > 6 || in.AtHour < 0 || in.AtHour > 23 || in.AtMinute < 0 || in.AtMinute > 59 {
			return errors.New("每周执行时间无效")
		}
	default:
		return errors.New("执行频率无效")
	}
	return nil
}

func (s *Server) scheduleTargetCount(sch *store.Schedule) int {
	if s.scheduler == nil {
		return 0
	}
	targets, err := s.scheduler.Targets(sch)
	if err != nil {
		return 0
	}
	return len(targets)
}

func (s *Server) handleSchedulesList(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.ListSchedules()
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "查询定时计划失败: "+err.Error())
		return
	}
	for i := range items {
		if items[i].ScopeType == "custom" {
			ids, err := s.store.GetScheduleServerIDs(items[i].ID)
			if err == nil {
				items[i].ServerIDs = ids
			}
		}
		items[i].TargetCount = s.scheduleTargetCount(&items[i])
	}
	writeOK(w, items)
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var in scheduleInput
	if err := readJSON(r, &in); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := in.normalize(); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateScheduleServers(in.ServerIDs); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	sch := &store.Schedule{
		Name: in.Name, Enabled: in.Enabled, ScopeType: in.ScopeType, ScopeValue: in.ScopeValue,
		FreqType: in.FreqType, IntervalMinutes: in.IntervalMinutes, AtHour: in.AtHour,
		AtMinute: in.AtMinute, Weekday: in.Weekday, ServerIDs: in.ServerIDs,
	}
	next := scheduler.NextRun(sch, time.Now())
	sch.NextRunAt = &next
	if err := s.store.CreateSchedule(sch); err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if in.ScopeType == "custom" {
		if err := s.store.SetScheduleServers(sch.ID, in.ServerIDs); err != nil {
			_ = s.store.DeleteSchedule(sch.ID)
			writeFail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	sch.TargetCount = s.scheduleTargetCount(sch)
	s.log.Info("创建定时计划", "id", sch.ID, "name", sch.Name, "next", sch.NextRunAt)
	writeOK(w, sch)
}

func (s *Server) handleScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "计划 ID 无效")
		return
	}
	var in scheduleInput
	if err := readJSON(r, &in); err != nil {
		writeFail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if err := in.normalize(); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateScheduleServers(in.ServerIDs); err != nil {
		writeFail(w, http.StatusBadRequest, err.Error())
		return
	}
	sch, err := s.store.GetSchedule(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "定时计划不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	sch.Name = in.Name
	sch.Enabled = in.Enabled
	sch.ScopeType = in.ScopeType
	sch.ScopeValue = in.ScopeValue
	sch.FreqType = in.FreqType
	sch.IntervalMinutes = in.IntervalMinutes
	sch.AtHour = in.AtHour
	sch.AtMinute = in.AtMinute
	sch.Weekday = in.Weekday
	sch.ServerIDs = in.ServerIDs
	next := scheduler.NextRun(sch, time.Now())
	sch.NextRunAt = &next
	if err := s.store.UpdateSchedule(sch); err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.SetScheduleServers(sch.ID, in.ServerIDs); err != nil {
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	sch.TargetCount = s.scheduleTargetCount(sch)
	writeOK(w, sch)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "计划 ID 无效")
		return
	}
	if err := s.store.DeleteSchedule(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeFail(w, http.StatusNotFound, "定时计划不存在")
			return
		}
		writeFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("删除定时计划", "id", id)
	writeOK(w, nil)
}

func (s *Server) handleScheduleRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeFail(w, http.StatusBadRequest, "计划 ID 无效")
		return
	}
	if s.scheduler == nil {
		writeFail(w, http.StatusInternalServerError, "定时任务服务未初始化")
		return
	}
	rec, err := s.scheduler.RunNow(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, scheduler.ErrAlreadyRunning):
			writeFail(w, http.StatusConflict, err.Error())
		case errors.Is(err, scheduler.ErrNoTargets):
			writeFail(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeFail(w, http.StatusNotFound, "定时计划不存在")
		default:
			writeFail(w, http.StatusInternalServerError, "执行定时计划失败: "+err.Error())
		}
		return
	}
	summary := json.RawMessage(rec.SummaryJSON)
	if len(summary) == 0 {
		summary = json.RawMessage("{}")
	}
	writeOK(w, map[string]any{"batch": rec, "summary": summary})
}

func (s *Server) validateScheduleServers(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	servers, err := s.store.ListServers()
	if err != nil {
		return err
	}
	exists := make(map[int64]bool, len(servers))
	for i := range servers {
		exists[servers[i].ID] = true
	}
	for _, id := range ids {
		if id <= 0 || !exists[id] {
			return fmt.Errorf("服务器 %d 不存在", id)
		}
	}
	return nil
}
