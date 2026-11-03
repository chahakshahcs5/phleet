package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"phleet/internal/model"
)

// startJob runs fn in the background and persists enough state for the UI to
// poll it. It is deliberately small: the job record is the source of truth and
// a control-plane restart simply leaves a stale running row that the janitor
// can prune.
func (s *Server) startJob(kind, target string, timeout time.Duration, fn func(context.Context, func(string)) error) string {
	id := uuid.NewString()
	job := model.Job{ID: id, Kind: kind, Target: target, Status: "running", CreatedAt: time.Now().Unix()}
	if err := s.st.CreateJob(job); err != nil {
		s.log.Warn("create job", "kind", kind, "err", err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		logf := func(line string) {
			_ = s.st.AppendJobOutput(id, line+"\n")
		}
		err := fn(ctx, logf)
		status := "succeeded"
		errMsg := ""
		if err != nil {
			status = "failed"
			errMsg = err.Error()
			logf("error: " + errMsg)
		}
		_ = s.st.FinishJob(id, status, errMsg)
		action := kind + "." + status
		_ = s.st.AddEvent("job", action, target, errMsg)
	}()
	return id
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.st.GetJob(r.PathValue("id"))
	if err != nil {
		apiErr(w, http.StatusNotFound, fmt.Errorf("job not found"))
		return
	}
	writeJSON(w, 200, map[string]any{"job": j})
}
