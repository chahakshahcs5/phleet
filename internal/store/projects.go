package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

func (s *Store) CreateProject(p model.Project) error {
	if p.CreatedAt == 0 {
		p.CreatedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO projects
(id,name,slug,repo,branch,runtime,build_cmd,start_cmd,health_path,hostname,env_enc,selector,replicas,pinned_server,source_type,source_size,source_updated_at,parent_id,kind,stack_order,created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Slug, p.Repo, p.Branch, p.Runtime, p.BuildCmd, p.StartCmd,
		p.HealthPath, p.Hostname, p.EnvEnc, p.Selector, p.Replicas, p.PinnedServer,
		sourceType(p.SourceType), p.SourceSize, p.SourceUpdatedAt, p.ParentID, kindOrDefault(p.Kind), p.StackOrder, p.CreatedAt)
	return err
}

func kindOrDefault(v string) string {
	if v == "stack" {
		return "stack"
	}
	return "app"
}

func sourceType(v string) string {
	if v == "upload" {
		return "upload"
	}
	return "git"
}

func (s *Store) UpdateProject(p model.Project) error {
	_, err := s.db.Exec(`UPDATE projects SET name=?, repo=?, branch=?, runtime=?, build_cmd=?,
start_cmd=?, health_path=?, hostname=?, env_enc=?, selector=?, replicas=?, pinned_server=?,
source_type=?, source_size=?, source_updated_at=?, parent_id=?, kind=?, stack_order=? WHERE id=?`,
		p.Name, p.Repo, p.Branch, p.Runtime, p.BuildCmd, p.StartCmd, p.HealthPath, p.Hostname,
		p.EnvEnc, p.Selector, p.Replicas, p.PinnedServer, sourceType(p.SourceType),
		p.SourceSize, p.SourceUpdatedAt, p.ParentID, kindOrDefault(p.Kind), p.StackOrder, p.ID)
	return err
}

func (s *Store) UpdateProjectSource(id, sourceType string, size, updatedAt int64) error {
	_, err := s.db.Exec(`UPDATE projects SET source_type=?, source_size=?, source_updated_at=? WHERE id=?`,
		sourceType, size, updatedAt, id)
	return err
}

func (s *Store) scanProject(row interface{ Scan(...any) error }) (model.Project, error) {
	var p model.Project
	err := row.Scan(&p.ID, &p.Name, &p.Slug, &p.Repo, &p.Branch, &p.Runtime, &p.BuildCmd,
		&p.StartCmd, &p.HealthPath, &p.Hostname, &p.EnvEnc, &p.Selector, &p.Replicas, &p.PinnedServer,
		&p.SourceType, &p.SourceSize, &p.SourceUpdatedAt, &p.ParentID, &p.Kind, &p.StackOrder, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

const projectCols = `id,name,slug,repo,branch,runtime,build_cmd,start_cmd,health_path,hostname,env_enc,selector,replicas,pinned_server,source_type,source_size,source_updated_at,parent_id,kind,stack_order,created_at`

func (s *Store) GetProject(id string) (model.Project, error) {
	return s.scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE id=?`, id))
}

func (s *Store) GetProjectBySlug(slug string) (model.Project, error) {
	return s.scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE slug=?`, slug))
}

func (s *Store) GetProjectByHostname(host string) (model.Project, error) {
	return s.scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE hostname=?`, host))
}

func (s *Store) ListProjects() ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT ` + projectCols + ` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		p, err := s.scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListChildProjects(parentID string) ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT `+projectCols+` FROM projects WHERE parent_id=? ORDER BY stack_order, name`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Project{}
	for rows.Next() {
		p, err := s.scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM projects WHERE parent_id=?`, id)
	if err != nil {
		return err
	}
	childIDs := []string{}
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, child)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, projectID := range append(childIDs, id) {
		stmts := []struct {
			q string
			a string
		}{
			{`DELETE FROM deploy_logs WHERE deployment_id IN (SELECT id FROM deployments WHERE project_id=?)`, projectID},
			{`DELETE FROM deployments WHERE project_id=?`, projectID},
			{`DELETE FROM placements WHERE project_id=?`, projectID},
		}
		for _, st := range stmts {
			if _, err := tx.Exec(st.q, st.a); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE parent_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertPlacement(p model.Placement) error {
	if p.UpdatedAt == 0 {
		p.UpdatedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO placements
(project_id,server_id,status,desired,port,pid,restarts,release,last_error,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(project_id,server_id) DO UPDATE SET
  status=excluded.status, desired=excluded.desired, port=excluded.port, pid=excluded.pid,
  restarts=excluded.restarts, release=excluded.release, last_error=excluded.last_error, updated_at=excluded.updated_at`,
		p.ProjectID, p.ServerID, p.Status, desiredOrDefault(p.Desired), p.Port, p.PID, p.Restarts, p.Release, p.LastError, p.UpdatedAt)
	return err
}

func desiredOrDefault(v string) string {
	if v == "" {
		return "running"
	}
	return v
}

func (s *Store) GetPlacement(projectID string) (model.Placement, error) {
	var p model.Placement
	err := s.db.QueryRow(`SELECT project_id,server_id,status,desired,port,pid,restarts,release,last_error,updated_at
FROM placements WHERE project_id=? ORDER BY updated_at DESC LIMIT 1`, projectID).
		Scan(&p.ProjectID, &p.ServerID, &p.Status, &p.Desired, &p.Port, &p.PID, &p.Restarts, &p.Release, &p.LastError, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProjectPlacements(projectID string) ([]model.Placement, error) {
	rows, err := s.db.Query(`SELECT project_id,server_id,status,desired,port,pid,restarts,release,last_error,updated_at
FROM placements WHERE project_id=? ORDER BY server_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Placement{}
	for rows.Next() {
		var p model.Placement
		if err := rows.Scan(&p.ProjectID, &p.ServerID, &p.Status, &p.Desired, &p.Port, &p.PID, &p.Restarts,
			&p.Release, &p.LastError, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePlacement(projectID, serverID string) error {
	_, err := s.db.Exec(`DELETE FROM placements WHERE project_id=? AND server_id=?`, projectID, serverID)
	return err
}

func (s *Store) SetProjectDesired(projectID, desired string) error {
	_, err := s.db.Exec(`UPDATE placements SET desired=?, updated_at=? WHERE project_id=?`, desired, now(), projectID)
	return err
}

func (s *Store) ListPlacementsForServer(serverID string) ([]model.Placement, error) {
	rows, err := s.db.Query(`SELECT project_id,server_id,status,desired,port,pid,restarts,release,last_error,updated_at
FROM placements WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Placement{}
	for rows.Next() {
		var p model.Placement
		if err := rows.Scan(&p.ProjectID, &p.ServerID, &p.Status, &p.Desired, &p.Port, &p.PID, &p.Restarts,
			&p.Release, &p.LastError, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListPlacements() ([]model.Placement, error) {
	rows, err := s.db.Query(`SELECT project_id,server_id,status,desired,port,pid,restarts,release,last_error,updated_at FROM placements`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Placement{}
	for rows.Next() {
		var p model.Placement
		if err := rows.Scan(&p.ProjectID, &p.ServerID, &p.Status, &p.Desired, &p.Port, &p.PID, &p.Restarts,
			&p.Release, &p.LastError, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePlacementStatus(projectID, status string, pid, restarts int, lastErr, release string) error {
	_, err := s.db.Exec(`UPDATE placements SET status=?, pid=?, restarts=?, last_error=?, release=?, updated_at=?
WHERE project_id=?`, status, pid, restarts, lastErr, release, now(), projectID)
	return err
}

func (s *Store) UpdatePlacementStatusFor(projectID, serverID, status string, pid, restarts int, lastErr, release string) error {
	_, err := s.db.Exec(`UPDATE placements SET status=?, pid=?, restarts=?, last_error=?, release=?, updated_at=?
WHERE project_id=? AND server_id=?`, status, pid, restarts, lastErr, release, now(), projectID, serverID)
	return err
}

func (s *Store) UsedPorts(serverID string) (map[int]bool, error) {
	out := map[int]bool{}
	rows, err := s.db.Query(`SELECT port FROM placements WHERE server_id=? AND port>0`, serverID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		out[p] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// Addon ports share the app range so a database and an app can never be
	// allocated the same port on one phone.
	rows, err = s.db.Query(`SELECT port FROM addon_placements WHERE server_id=? AND port>0`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

func (s *Store) CreateDeployment(d model.Deployment) error {
	if d.StartedAt == 0 {
		d.StartedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO deployments
(id,project_id,server_id,commit_sha,status,release,started_at,finished_at,detail)
VALUES (?,?,?,?,?,?,?,?,?)`,
		d.ID, d.ProjectID, d.ServerID, d.CommitSHA, d.Status, d.Release, d.StartedAt, d.FinishedAt, d.Detail)
	return err
}

func (s *Store) SetDeploymentCommit(id, commit string) error {
	_, err := s.db.Exec(`UPDATE deployments SET commit_sha=? WHERE id=?`, commit, id)
	return err
}

func (s *Store) UpdateDeploymentStatus(id, status, release, detail string) error {
	_, err := s.db.Exec(`UPDATE deployments SET status=?, release=?, detail=? WHERE id=?`, status, release, detail, id)
	return err
}

func (s *Store) FinishDeployment(id, status, release, detail string) error {
	_, err := s.db.Exec(`UPDATE deployments SET status=?, release=?, detail=?, finished_at=? WHERE id=?`,
		status, release, detail, now(), id)
	return err
}

func (s *Store) GetDeployment(id string) (model.Deployment, error) {
	var d model.Deployment
	err := s.db.QueryRow(`SELECT id,project_id,server_id,commit_sha,status,release,started_at,finished_at,detail
FROM deployments WHERE id=?`, id).
		Scan(&d.ID, &d.ProjectID, &d.ServerID, &d.CommitSHA, &d.Status, &d.Release, &d.StartedAt, &d.FinishedAt, &d.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Store) ListDeployments(projectID string, limit int) ([]model.Deployment, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.db.Query(`SELECT id,project_id,server_id,commit_sha,status,release,started_at,finished_at,detail
FROM deployments WHERE project_id=? ORDER BY started_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Deployment{}
	for rows.Next() {
		var d model.Deployment
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.ServerID, &d.CommitSHA, &d.Status, &d.Release,
			&d.StartedAt, &d.FinishedAt, &d.Detail); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LatestDeployment returns the most recent deployment for a project.
func (s *Store) LatestDeployment(projectID string) (model.Deployment, error) {
	list, err := s.ListDeployments(projectID, 1)
	if err != nil {
		return model.Deployment{}, err
	}
	if len(list) == 0 {
		return model.Deployment{}, ErrNotFound
	}
	return list[0], nil
}

func (s *Store) AddDeployLog(deploymentID, line string) error {
	_, err := s.db.Exec(`INSERT INTO deploy_logs (deployment_id,ts,line) VALUES (?,?,?)`, deploymentID, now(), line)
	return err
}

func (s *Store) ListDeployLogs(deploymentID string, limit int) ([]model.DeployLog, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT ts,line FROM deploy_logs WHERE deployment_id=? ORDER BY id DESC LIMIT ?`, deploymentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.DeployLog{}
	for rows.Next() {
		var l model.DeployLog
		if err := rows.Scan(&l.TS, &l.Line); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	// reverse into chronological order
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// PruneProjectHistory removes deployments (and their logs) older than cutoff.
func (s *Store) PruneProjectHistory(cutoff int64) error {
	if _, err := s.db.Exec(`DELETE FROM deploy_logs WHERE deployment_id IN
(SELECT id FROM deployments WHERE started_at < ?)`, cutoff); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM deployments WHERE started_at < ?`, cutoff)
	return err
}
