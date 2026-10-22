package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/agent"
	"knowledge-base/internal/logging"
)

// startAgentRun records a run for observability. Failures are logged but never
// block the chat response (the schema may not have been migrated yet).
func (s *Server) startAgentRun(ctx context.Context, runID string, in agentChatRequest, skills []string) {
	if s.db == nil {
		return
	}
	encoded, err := json.Marshal(skills)
	if err != nil {
		encoded = []byte("[]")
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err = s.db.Exec(writeCtx, `INSERT INTO agent_runs(run_id,unit_id,expert,question,skills)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT (run_id) DO NOTHING`,
		runID, in.UnitID, in.Expert, in.Message, json.RawMessage(encoded))
	if err != nil {
		logging.App().Warn("agent_run_start_failed", "run_id", runID, "error", err)
	}
}

// ensureAgentRunSchema upgrades existing databases without requiring the full
// deploy schema to be reapplied for every local build.
func (s *Server) ensureAgentRunSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(ctx, `ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS question text NOT NULL DEFAULT ''`); err != nil {
		logging.App().Warn("agent_run_schema_failed", "step", "question", "error", err)
	}
}

func (s *Server) finishAgentRun(runID, status string, steps int, usage agent.Usage, errText string) {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.db.Exec(ctx, `UPDATE agent_runs
		SET status=$2, steps=$3, prompt_tokens=$4, completion_tokens=$5, error=$6, finished_at=now()
		WHERE run_id=$1`,
		runID, status, steps, usage.PromptTokens, usage.CompletionTokens, errText)
	if err != nil {
		logging.App().Warn("agent_run_finish_failed", "run_id", runID, "error", err)
	}
}

// agentRunDetail 返回一次运行的元信息与工具调用时间线，用于复盘
// 「模型等了多久、每个工具花了多久、哪一步失败」。
func (s *Server) agentRunDetail(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	runID := strings.TrimSpace(c.Param("id"))
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run_id is required"})
		return
	}
	var run struct {
		RunID            string     `json:"run_id"`
		UnitID           string     `json:"unit_id"`
		Expert           string     `json:"expert"`
		Question         string     `json:"question"`
		Status           string     `json:"status"`
		Steps            int        `json:"steps"`
		PromptTokens     int        `json:"prompt_tokens"`
		CompletionTokens int        `json:"completion_tokens"`
		Error            string     `json:"error"`
		StartedAt        time.Time  `json:"started_at"`
		FinishedAt       *time.Time `json:"finished_at"`
	}
	// Runs created before the question column was introduced can recover the
	// prompt from their persisted user message.
	err := s.db.QueryRow(c, `SELECT r.run_id,r.unit_id,r.expert,
		COALESCE(NULLIF(r.question,''),
			(SELECT m.content FROM agent_messages m
			 WHERE m.run_id=r.run_id AND m.role='user' ORDER BY m.id LIMIT 1),''),
		r.status,r.steps,r.prompt_tokens,r.completion_tokens,r.error,r.started_at,r.finished_at
		FROM agent_runs r WHERE r.run_id=$1`, runID).
		Scan(&run.RunID, &run.UnitID, &run.Expert, &run.Question, &run.Status, &run.Steps,
			&run.PromptTokens, &run.CompletionTokens, &run.Error, &run.StartedAt, &run.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "运行记录不存在"})
		return
	}
	if err != nil {
		fail(c, err)
		return
	}

	rows, err := s.db.Query(c, `SELECT step,kind,tool,arguments,result_preview,latency_ms,error
		FROM agent_steps WHERE run_id=$1 ORDER BY id`, runID)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()

	steps := []gin.H{}
	for rows.Next() {
		var step int
		var kind, tool, arguments, preview, stepErr string
		var latency int64
		if err := rows.Scan(&step, &kind, &tool, &arguments, &preview, &latency, &stepErr); err != nil {
			fail(c, err)
			return
		}
		steps = append(steps, gin.H{
			"step": step, "kind": kind, "tool": tool, "arguments": arguments,
			"result_preview": preview, "latency_ms": latency, "error": stepErr,
		})
	}
	if err := rows.Err(); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"run": run, "steps": steps})
}

func (s *Server) recordAgentStep(runID string, event agent.Event) {
	if s.db == nil {
		return
	}
	arguments := event.Arguments
	if arguments == "" || !json.Valid([]byte(arguments)) {
		arguments = "{}"
	}
	preview := truncateRunes(event.Result, 1000)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.db.Exec(ctx, `INSERT INTO agent_steps(run_id,step,kind,tool,arguments,result_preview,latency_ms,error)
		VALUES($1,$2,'tool',$3,$4,$5,$6,$7)`,
		runID, event.Step, event.Tool, json.RawMessage(arguments), preview, event.LatencyMS, event.Error)
	if err != nil {
		logging.App().Warn("agent_step_record_failed", "run_id", runID, "tool", event.Tool, "error", err)
	}
}
