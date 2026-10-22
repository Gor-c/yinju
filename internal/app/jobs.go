package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

// Job is an asynchronous indexing task. A running job is kept in memory for
// fast polling, while every state change is mirrored to index_jobs so status
// survives a process restart.
// 任务阶段：上传（入队） → 解析 → 索引。失败时 phase 停在出错的阶段，
// 前端据此区分「解析失败」和「索引失败」。
const (
	phasePending = "pending"
	phaseParse   = "parse"
	phaseIndex   = "index"
	phaseDone    = "done"
)

type Job struct {
	mu sync.RWMutex

	ID       string
	UnitID   string
	DocType  string
	Filename string
	Path     string
	Status   string
	Phase    string
	Progress int
	Message  string
	Chunks   int
	Attempts int

	// SourceURL records the web page an imported document came from; it stays
	// empty for ordinary uploads.
	SourceURL string
}

// jobView is the JSON shape returned by the upload and job status endpoints.
type jobView struct {
	ID        string     `json:"job_id"`
	UnitID    string     `json:"unit_id"`
	DocType   string     `json:"doc_type"`
	Filename  string     `json:"filename"`
	Path      string     `json:"file_path,omitempty"`
	Status    string     `json:"status"`
	Phase     string     `json:"phase"`
	Progress  int        `json:"progress"`
	Message   string     `json:"message"`
	Chunks    int        `json:"chunks"`
	Attempts  int        `json:"attempts"`
	SourceURL string     `json:"source_url,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func (j *Job) snapshot() jobView {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return jobView{
		ID:        j.ID,
		UnitID:    j.UnitID,
		DocType:   j.DocType,
		Filename:  j.Filename,
		Path:      j.Path,
		Status:    j.Status,
		Phase:     j.Phase,
		Progress:  j.Progress,
		Message:   j.Message,
		Chunks:    j.Chunks,
		Attempts:  j.Attempts,
		SourceURL: j.SourceURL,
	}
}

func (j *Job) MarshalJSON() ([]byte, error) {
	return json.Marshal(j.snapshot())
}

func (j *Job) set(status string, progress int, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = status
	j.Progress = progress
	j.Message = message
}

func (j *Job) setChunks(n int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Chunks = n
}

// updateJob applies a state change and mirrors it to the database. Persistence
// uses its own timeout so a cancelled indexing context still records the final
// failure state.
func (s *Server) updateJob(j *Job, status string, progress int, message string) {
	s.updateJobPhase(j, status, "", progress, message)
}

// updateJobPhase 在更新状态的同时推进阶段；phase 为空表示保持当前阶段
// （失败时用它保留出错位置）。
func (s *Server) updateJobPhase(j *Job, status, phase string, progress int, message string) {
	j.mu.Lock()
	j.Status = status
	j.Progress = progress
	j.Message = message
	if phase != "" {
		j.Phase = phase
	}
	j.mu.Unlock()
	if err := s.persistJob(j); err != nil {
		view := j.snapshot()
		logging.App().Error("index_job_persist_failed", "job_id", view.ID, "error", err)
	}
}

func (s *Server) failJob(j *Job, message string) {
	view := j.snapshot()
	s.updateJob(j, "failed", view.Progress, message)
	logging.App().Error("index_job_failed",
		"job_id", view.ID,
		"unit_id", view.UnitID,
		"filename", view.Filename,
		"doc_type", view.DocType,
		"error", message,
	)
}

func (s *Server) persistJob(j *Job) error {
	if s.db == nil {
		return errors.New("database is not configured")
	}
	view := j.snapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.db.Exec(ctx, `INSERT INTO index_jobs(job_id,unit_id,doc_type,filename,file_path,status,phase,progress,message,chunks,attempts,source_url,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())
		ON CONFLICT(job_id) DO UPDATE
		SET file_path=EXCLUDED.file_path,status=EXCLUDED.status,phase=EXCLUDED.phase,
			progress=EXCLUDED.progress,message=EXCLUDED.message,chunks=EXCLUDED.chunks,
			attempts=EXCLUDED.attempts,source_url=EXCLUDED.source_url,updated_at=now()`,
		view.ID, view.UnitID, view.DocType, view.Filename, view.Path, view.Status, view.Phase,
		view.Progress, view.Message, view.Chunks, view.Attempts, view.SourceURL)
	return err
}

// recoverInterruptedJobs requeues only stale jobs. This also runs during a
// rolling multi-instance startup, so fresh running rows must remain untouched.
func (s *Server) recoverInterruptedJobs() {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Only recover rows older than the maximum job lifetime. A second live
	// server instance must not steal a task that another worker is processing.
	jobCutoff := time.Now().Add(-(indexTimeout() + time.Minute))
	tag, err := s.db.Exec(ctx, `UPDATE index_jobs
		SET status='queued',phase='pending',progress=0,message='服务重启，重新排队',updated_at=now()
		WHERE status='running' AND attempts < $1 AND updated_at < $2`, maxJobAttempts, jobCutoff)
	if err != nil {
		logging.App().Error("index_jobs_recovery_failed", "error", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		logging.App().Warn("index_jobs_requeued", "count", n)
	}

	// Jobs that already exhausted their attempts must not loop forever.
	if _, err := s.db.Exec(ctx, `UPDATE index_jobs
		SET status='failed',message='重试次数已达上限，请重新上传',updated_at=now()
		WHERE status IN ('running','queued') AND attempts >= $1
		AND (status='queued' OR updated_at < $2)`, maxJobAttempts, jobCutoff); err != nil {
		logging.App().Warn("index_jobs_attempt_limit_failed", "error", err)
	}

	// Agent runs are in-process as well; mark abandoned ones so dashboards do
	// not show a permanently "running" state.
	agentCutoff := time.Now().Add(-(agentRunTimeout + time.Minute))
	if _, err := s.db.Exec(ctx, `UPDATE agent_runs
		SET status='interrupted',error='服务重启，运行中断',finished_at=now()
		WHERE status='running' AND started_at < $1`, agentCutoff); err != nil {
		logging.App().Warn("agent_runs_recovery_failed", "error", err)
	}
}

const (
	jobPollInterval = 2 * time.Second
	// maxJobAttempts 限制同一任务被 worker 领取的次数，避免坏文件无限重试。
	maxJobAttempts = 3
)

// startJobWorker 启动持久化队列消费者：仅恢复超时阈值外的陈旧任务，
// 随后轮询 index_jobs 领取 queued 任务。
func (s *Server) startJobWorker() {
	if s.db == nil {
		return
	}
	s.recoverInterruptedJobs()
	go func() {
		ticker := time.NewTicker(jobPollInterval)
		defer ticker.Stop()
		for {
			s.drainJobQueue()
			select {
			case <-ticker.C:
			case <-s.workerWake:
			}
		}
	}()
}

// wakeWorker 让上传后立即处理，而不必等下一次轮询。
func (s *Server) wakeWorker() {
	if s.workerWake == nil {
		return
	}
	select {
	case s.workerWake <- struct{}{}:
	default: // 已有待处理信号
	}
}

// indexConcurrency 控制同时处理的索引任务数：默认 1（串行），
// 可用 INDEX_CONCURRENCY 调大，上限 4，避免 embedding 并发过高触发限流。
func indexConcurrency() int {
	v, err := strconv.Atoi(env("INDEX_CONCURRENCY", "1"))
	if err != nil || v < 1 {
		return 1
	}
	return min(v, 4)
}

func (s *Server) drainJobQueue() {
	sem := make(chan struct{}, indexConcurrency())
	var wg sync.WaitGroup
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		view, ok, err := s.claimNextJob(ctx)
		cancel()
		if err != nil {
			logging.App().Error("index_job_claim_failed", "error", err)
			break
		}
		if !ok {
			break
		}
		j := &Job{
			ID: view.ID, UnitID: view.UnitID, DocType: view.DocType, Filename: view.Filename,
			Path: view.Path, Status: "running", Phase: phaseParse, Message: "开始解析", Attempts: view.Attempts,
			SourceURL: view.SourceURL,
		}
		s.jobs.Store(j.ID, j)
		logging.App().Info("index_job_claimed",
			"job_id", j.ID, "unit_id", j.UnitID, "filename", j.Filename, "attempt", j.Attempts)

		sem <- struct{}{}
		wg.Add(1)
		go func(job *Job, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runIndexJob(job, path)
		}(j, s.jobSourcePath(view))
	}
	wg.Wait()
}

// claimNextJob 用 FOR UPDATE SKIP LOCKED 抢占一条排队任务，
// 多实例部署时同一任务不会被重复执行。
func (s *Server) claimNextJob(ctx context.Context) (jobView, bool, error) {
	var v jobView
	err := s.db.QueryRow(ctx, `UPDATE index_jobs
		SET status='running',phase='parse',progress=5,message='开始解析',attempts=attempts+1,updated_at=now()
		WHERE job_id=(
			SELECT job_id FROM index_jobs
			WHERE status='queued' AND attempts < $1
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING job_id,unit_id,doc_type,filename,file_path,status,phase,progress,message,chunks,attempts,source_url`,
		maxJobAttempts).
		Scan(&v.ID, &v.UnitID, &v.DocType, &v.Filename, &v.Path, &v.Status, &v.Phase, &v.Progress, &v.Message, &v.Chunks, &v.Attempts, &v.SourceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobView{}, false, nil
	}
	if err != nil {
		return jobView{}, false, err
	}
	return v, true, nil
}

// jobSourcePath 优先使用入库时记录的源文件路径；旧数据没有该字段时按约定回推。
func (s *Server) jobSourcePath(view jobView) string {
	if view.Path != "" {
		return view.Path
	}
	return filepath.Join(env("UPLOAD_DIR", "./data/uploads"),
		filepath.Base(view.UnitID), view.ID+"_"+filepath.Base(view.Filename))
}

func (s *Server) loadJob(ctx context.Context, id string) (*Job, error) {
	view := jobView{}
	err := s.db.QueryRow(ctx, `SELECT job_id,unit_id,doc_type,filename,status,phase,progress,message,chunks
		FROM index_jobs WHERE job_id=$1`, id).
		Scan(&view.ID, &view.UnitID, &view.DocType, &view.Filename, &view.Status, &view.Phase, &view.Progress, &view.Message, &view.Chunks)
	if err != nil {
		return nil, err
	}
	return &Job{ID: view.ID, UnitID: view.UnitID, DocType: view.DocType, Filename: view.Filename,
		Status: view.Status, Phase: view.Phase, Progress: view.Progress, Message: view.Message, Chunks: view.Chunks}, nil
}

// jobList 返回最近的索引任务，供前端在切换页面后仍能看到入库进度。
// 进程内仍在运行的任务优先于数据库快照，避免读到滞后的持久化状态。
func (s *Server) jobList(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 100)
		}
	}

	var statuses []string
	for _, raw := range strings.Split(c.Query("status"), ",") {
		switch st := strings.TrimSpace(raw); st {
		case "queued", "running", "done", "failed":
			statuses = append(statuses, st)
		}
	}

	query := `SELECT job_id,unit_id,doc_type,filename,status,phase,progress,message,chunks,created_at,updated_at
		FROM index_jobs`
	args := []any{}
	conds := []string{}
	if len(statuses) > 0 {
		args = append(args, statuses)
		conds = append(conds, fmt.Sprintf("status = ANY($%d)", len(args)))
	}
	if unit := strings.TrimSpace(c.Query("unit_id")); unit != "" {
		args = append(args, unit)
		conds = append(conds, fmt.Sprintf("unit_id = $%d", len(args)))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))

	rows, err := s.db.Query(c, query, args...)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()

	out := []jobView{}
	for rows.Next() {
		var v jobView
		if err = rows.Scan(&v.ID, &v.UnitID, &v.DocType, &v.Filename, &v.Status, &v.Phase, &v.Progress,
			&v.Message, &v.Chunks, &v.CreatedAt, &v.UpdatedAt); err != nil {
			fail(c, err)
			return
		}
		if live, ok := s.jobs.Load(v.ID); ok {
			if j, ok := live.(*Job); ok {
				view := j.snapshot()
				view.CreatedAt = v.CreatedAt
				view.UpdatedAt = v.UpdatedAt
				out = append(out, view)
				continue
			}
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": out, "count": len(out)})
}

func (s *Server) job(c *gin.Context) {
	if v, ok := s.jobs.Load(c.Param("id")); ok {
		c.JSON(200, v)
		return
	}
	if s.db == nil {
		c.JSON(404, gin.H{"error": "job not found"})
		return
	}
	j, err := s.loadJob(c, c.Param("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(404, gin.H{"error": "job not found"})
			return
		}
		fail(c, err)
		return
	}
	c.JSON(200, j)
}

// retryJob 把失败（或已完成）的任务重新排队。索引失败不会删除源文件，
// 因此修好解析环境（如安装 poppler）或模型恢复后直接重跑即可，不必重新上传。
func (s *Server) retryJob(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	var path, status string
	err := s.db.QueryRow(c, `SELECT file_path,status FROM index_jobs WHERE job_id=$1`, id).Scan(&path, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	if status == "queued" || status == "running" {
		c.JSON(http.StatusConflict, gin.H{"error": "任务正在执行中，请稍后再试"})
		return
	}
	if path == "" || !fileExists(path) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "源文件已不存在，请重新上传"})
		return
	}
	if _, err := s.db.Exec(c, `UPDATE index_jobs
		SET status='queued',phase='pending',progress=0,message='重新排队',attempts=0,updated_at=now()
		WHERE job_id=$1`, id); err != nil {
		fail(c, err)
		return
	}
	// 清掉内存里的旧快照，避免 /jobs/:id 返回上一次的失败状态。
	s.jobs.Delete(id)
	s.wakeWorker()
	logging.App().Info("index_job_retried",
		"request_id", c.GetString(requestIDKey),
		"job_id", id,
		"filename", filepath.Base(path),
	)
	c.JSON(http.StatusOK, gin.H{"job_id": id, "status": "queued", "message": "重新排队"})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ensureJobSchema 为老库补上任务阶段列（可重复执行）。
func (s *Server) ensureJobSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(ctx, `ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS phase text NOT NULL DEFAULT 'pending'`); err != nil {
		logging.App().Warn("job_schema_failed", "step", "phase", "error", err)
		return
	}
	// 历史数据没有阶段：已完成视为 done，失败按 running 兜底。
	if _, err := s.db.Exec(ctx, `UPDATE index_jobs SET phase='done' WHERE phase='pending' AND status='done'`); err != nil {
		logging.App().Warn("job_schema_failed", "step", "backfill_done", "error", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE index_jobs SET phase='index' WHERE phase='pending' AND status='failed'`); err != nil {
		logging.App().Warn("job_schema_failed", "step", "backfill_failed", "error", err)
	}
}
