// Package runner 负责执行单台或批量 SSH 巡检，并持久化结果。
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"serverinspect/internal/batch"
	"serverinspect/internal/inspect"
	"serverinspect/internal/secret"
	"serverinspect/internal/sshx"
	"serverinspect/internal/store"
)

// Config 是巡检执行参数。
type Config struct {
	DialTimeout      time.Duration
	InspectOptions   inspect.Options
	SkipHostKeyCheck bool
	Concurrency      int
}

// Runner 复用 Web 与定时任务的巡检执行逻辑。
type Runner struct {
	store  *store.Store
	cipher *secret.Cipher
	cfg    Config
	log    *slog.Logger
}

// New 创建巡检执行器。
func New(st *store.Store, cipher *secret.Cipher, cfg Config, log *slog.Logger) *Runner {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{store: st, cipher: cipher, cfg: cfg, log: log}
}

// Run 执行单台服务器巡检。连接/采集失败会保存一条 error 记录并返回 nil error；
// 解密或数据库写入失败才返回 error。
func (r *Runner) Run(ctx context.Context, srv *store.Server) (*store.Inspection, *inspect.Report, error) {
	started := time.Now().UTC()
	password, err := r.cipher.Decrypt(srv.PasswordEnc)
	if err != nil {
		return nil, nil, fmt.Errorf("解密登录口令失败: %w", err)
	}
	knownHostKey := srv.HostKey
	if r.cfg.SkipHostKeyCheck {
		knownHostKey = ""
	}
	res, err := sshx.Dial(sshx.DialOptions{
		Host: srv.Host, Port: srv.Port, Username: srv.Username, Password: password,
		Timeout:      r.cfg.DialTimeout,
		KnownHostKey: knownHostKey,
		TrustHostKey: r.cfg.SkipHostKeyCheck,
	})
	if err != nil {
		rec, saveErr := r.recordFailure(srv, started, err.Error())
		if saveErr != nil {
			return nil, nil, saveErr
		}
		return rec, nil, nil
	}
	defer res.Client.Close()

	if res.Fingerprint != "" && srv.HostKey == "" {
		if err := r.store.SetHostKey(srv.ID, res.Fingerprint); err == nil {
			r.log.Info("记录主机密钥指纹", "server", srv.Name, "fingerprint", res.Fingerprint)
		}
	}

	report, runErr := inspect.Run(ctx, res.Client, r.cfg.InspectOptions)
	if runErr != nil {
		rec, saveErr := r.recordFailure(srv, started, runErr.Error())
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
	if err := r.store.CreateInspection(rec); err != nil {
		return nil, nil, err
	}
	r.log.Info("巡检完成", "server", srv.Name, "status", rec.Status, "score", rec.Score, "durationMs", rec.DurationMS)
	return rec, report, nil
}

func (r *Runner) recordFailure(srv *store.Server, started time.Time, message string) (*store.Inspection, error) {
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
	if err := r.store.CreateInspection(rec); err != nil {
		r.log.Error("保存失败巡检记录出错", "server", srv.Name, "error", err)
		return nil, fmt.Errorf("保存失败巡检记录出错: %w", err)
	}
	r.log.Warn("巡检失败", "server", srv.Name, "error", message)
	return rec, nil
}

// RunBatch 并发执行一组服务器的巡检并写入批量记录。
func (r *Runner) RunBatch(ctx context.Context, servers []*store.Server, trigger string, scheduleID int64, name string) (*store.Batch, error) {
	started := time.Now()
	serverMap := make(map[int64]*store.Server, len(servers))
	for _, srv := range servers {
		serverMap[srv.ID] = srv
	}

	var (
		mu       sync.Mutex
		records  []*store.Inspection
		failures []batch.Failure
		cursor   int
	)
	workerCount := r.cfg.Concurrency
	if workerCount > len(servers) {
		workerCount = len(servers)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if cursor >= len(servers) {
					mu.Unlock()
					return
				}
				srv := servers[cursor]
				cursor++
				mu.Unlock()

				rec, _, err := r.Run(ctx, srv)
				mu.Lock()
				if err != nil {
					failures = append(failures, batch.Failure{
						ServerID: srv.ID, Name: srv.Name, Host: srv.Host,
						NodeRole: srv.NodeRole, Group: srv.Group, Error: err.Error(),
					})
				} else if rec != nil {
					records = append(records, rec)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	finished := time.Now()
	summary := batch.Build(records, serverMap, failures, 0, finished.Sub(started).Milliseconds())
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("序列化批量分析失败: %w", err)
	}
	rec := &store.Batch{
		Trigger:       trigger,
		ScheduleID:    scheduleID,
		Name:          name,
		StartedAt:     started.UTC(),
		FinishedAt:    finished.UTC(),
		ServerCount:   len(servers),
		OKCount:       summary.Counts["ok"],
		WarningCount:  summary.Counts["warning"],
		CriticalCount: summary.Counts["critical"],
		ErrorCount:    summary.Counts["error"],
		IssueCount:    summary.IssueCount,
		AvgScore:      summary.AvgScore,
		ElapsedMS:     finished.Sub(started).Milliseconds(),
		SummaryJSON:   string(summaryJSON),
	}
	ids := make([]int64, 0, len(records))
	for _, item := range records {
		ids = append(ids, item.ID)
	}
	if err := r.store.CreateBatch(rec, ids); err != nil {
		return nil, err
	}
	return rec, nil
}
