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

	"serverinspect/internal/inspect"
	"serverinspect/internal/netscan"
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
		PasswordEnc: enc, Group: in.Group, Tags: in.Tags, Note: in.Note,
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
		PasswordEnc: passwordEnc, HostKey: hostKey, Group: in.Group, Tags: in.Tags, Note: in.Note,
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
	rec, report, err := s.executeInspection(ctx, srv)
	if err != nil {
		writeFail(w, http.StatusInternalServerError, "巡检执行失败: "+err.Error())
		return
	}
	writeOK(w, map[string]any{"inspection": rec, "report": report})
}

func (s *Server) executeInspection(ctx context.Context, srv *store.Server) (*store.Inspection, *inspect.Report, error) {
	started := time.Now().UTC()
	password, err := s.cipher.Decrypt(srv.PasswordEnc)
	if err != nil {
		return nil, nil, fmt.Errorf("解密登录口令失败: %w", err)
	}
	knownHostKey := srv.HostKey
	if s.cfg.SkipHostKeyCheck {
		knownHostKey = ""
	}
	res, err := sshx.Dial(sshx.DialOptions{
		Host: srv.Host, Port: srv.Port, Username: srv.Username, Password: password,
		Timeout:      s.cfg.DialTimeout,
		KnownHostKey: knownHostKey,
		TrustHostKey: s.cfg.SkipHostKeyCheck,
	})
	if err != nil {
		rec, saveErr := s.recordFailedInspection(srv, started, err.Error())
		if saveErr != nil {
			return nil, nil, saveErr
		}
		return rec, nil, nil
	}
	defer res.Client.Close()

	if res.Fingerprint != "" && srv.HostKey == "" {
		if err := s.store.SetHostKey(srv.ID, res.Fingerprint); err == nil {
			s.log.Info("记录主机密钥指纹", "server", srv.Name, "fingerprint", res.Fingerprint)
		}
	}

	report, runErr := inspect.Run(ctx, res.Client, s.cfg.InspectOptions)
	if runErr != nil {
		rec, saveErr := s.recordFailedInspection(srv, started, runErr.Error())
		if saveErr != nil {
			return nil, nil, saveErr
		}
		return rec, nil, nil
	}
	report.ServerName = srv.Name
	report.Host = srv.Host

	metricsJSON, _ := json.Marshal(report.Metrics)
	reportJSON, err := json.Marshal(report)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化巡检报告失败: %w", err)
	}
	rec := &store.Inspection{
		ServerID:    srv.ID,
		ServerName:  srv.Name,
		Host:        srv.Host,
		Status:      string(report.Overall),
		Score:       report.Score,
		MetricsJSON: string(metricsJSON),
		ReportJSON:  string(reportJSON),
		StartedAt:   report.StartedAt,
		FinishedAt:  report.FinishedAt,
		DurationMS:  report.DurationMS,
	}
	if err := s.store.CreateInspection(rec); err != nil {
		return nil, nil, err
	}
	s.log.Info("巡检完成", "server", srv.Name, "status", rec.Status, "score", rec.Score,
		"durationMs", rec.DurationMS)
	return rec, report, nil
}

func (s *Server) recordFailedInspection(srv *store.Server, started time.Time, message string) (*store.Inspection, error) {
	finished := time.Now().UTC()
	rec := &store.Inspection{
		ServerID:    srv.ID,
		ServerName:  srv.Name,
		Host:        srv.Host,
		Status:      "error",
		Error:       message,
		MetricsJSON: "{}",
		StartedAt:   started,
		FinishedAt:  finished,
		DurationMS:  finished.Sub(started).Milliseconds(),
	}
	if err := s.store.CreateInspection(rec); err != nil {
		s.log.Error("保存失败巡检记录出错", "server", srv.Name, "error", err)
		return nil, fmt.Errorf("保存失败巡检记录出错: %w", err)
	}
	s.log.Warn("巡检失败", "server", srv.Name, "error", message)
	return rec, nil
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
