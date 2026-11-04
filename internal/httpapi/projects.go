package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/auth"
	"phleet/internal/engines"
	"phleet/internal/model"
	"phleet/internal/placement"
	"phleet/internal/secrets"
	"phleet/internal/store"
)

const (
	portRangeStart = 4100
	portRangeEnd   = 4999
	maxReplicas    = 5
)

var (
	slugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	hostnamePattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	deployRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

var reservedSlugs = map[string]bool{
	"fleet": true, "www": true, "api": true, "admin": true, "mail": true,
	"smtp": true, "imap": true, "ns1": true, "ns2": true, "localhost": true,
}

type placementView struct {
	model.Placement
	ServerName string `json:"serverName"`
	Online     bool   `json:"online"`
	Drained    bool   `json:"drained"`
}

type projectView struct {
	model.Project
	Env        map[string]string `json:"env,omitempty"`
	Placements []placementView   `json:"placements"`
	Services   []projectView     `json:"services,omitempty"`
	Addons     []addonView       `json:"addons,omitempty"`
	Latest     *model.Deployment `json:"latest,omitempty"`
	URL        string            `json:"url"`
	Webhook    bool              `json:"webhook"`
	BasicAuth  bool              `json:"basicAuth"`
}

func isTerminalDeploy(s string) bool {
	switch s {
	case "active", "failed", "rolled_back":
		return true
	}
	return false
}

func (s *Server) appSuffix() string {
	_, fleetHost := s.Edge()
	if fleetHost != "" && strings.HasPrefix(fleetHost, "fleet.") {
		return strings.TrimPrefix(fleetHost, "fleet.")
	}
	return "localhost"
}

func (s *Server) buildProjectView(p model.Project, withEnv bool) projectView {
	v := projectView{Project: p, Env: nil, Placements: []placementView{}, Services: []projectView{}}
	v.Webhook = p.WebhookSecretEnc != ""
	v.BasicAuth = p.BasicAuthUser != "" && p.BasicAuthHash != ""
	v.Addons = s.projectAddons(p.ID)
	if p.Kind == "stack" {
		children, _ := s.st.ListChildProjects(p.ID)
		for _, child := range children {
			v.Services = append(v.Services, s.buildProjectView(child, false))
		}
		if len(v.Services) > 0 {
			for _, svc := range v.Services {
				if svc.URL != "" {
					v.URL = svc.URL
					break
				}
			}
		}
		if withEnv {
			if env, err := s.decryptEnv(p); err == nil {
				v.Env = env
			}
		}
		return v
	}
	host := p.Hostname
	if p.CustomHostname != "" {
		host = p.CustomHostname
	}
	if p.Kind != "worker" && host != "" {
		if _, fleetHost := s.Edge(); fleetHost != "" {
			v.URL = "https://" + host
		} else {
			v.URL = "http://" + host
		}
	}
	placements, _ := s.st.ListProjectPlacements(p.ID)
	sort.Slice(placements, func(i, j int) bool { return placements[i].ServerID < placements[j].ServerID })
	for _, pl := range placements {
		pv := placementView{Placement: pl}
		if srv, err := s.st.GetServer(pl.ServerID); err == nil {
			pv.ServerName = srv.Name
			pv.Drained = srv.Drain
			pv.Online = s.hub.Online(srv.ID)
		}
		v.Placements = append(v.Placements, pv)
	}
	if dep, err := s.st.LatestDeployment(p.ID); err == nil {
		v.Latest = &dep
	}
	if withEnv {
		if env, err := s.decryptEnv(p); err == nil {
			v.Env = env
		}
	}
	return v
}

func (s *Server) decryptEnv(p model.Project) (map[string]string, error) {
	out := map[string]string{}
	if p.EnvEnc == "" {
		return out, nil
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, p.EnvEnc)
	if err != nil {
		return out, err
	}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}, err
	}
	return out, nil
}

func (s *Server) encryptEnv(env map[string]string) (string, error) {
	if env == nil {
		env = map[string]string{}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return secrets.Encrypt(s.cfg.SessionKey, raw)
}

func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// workloadSlugExists reports whether a slug is already used by any workload.
// Projects, services and addons share the phone's ~/apps/<slug> namespace and
// the agent's supervisor map, so uniqueness must span both tables.
func (s *Server) workloadSlugExists(slug string) bool {
	if _, err := s.st.GetProjectBySlug(slug); err == nil {
		return true
	}
	if _, err := s.st.GetAddonBySlug(slug); err == nil {
		return true
	}
	return false
}

func (s *Server) uniqueSlug(base string) (string, error) {
	base = strings.Trim(base, "-")
	if base == "" {
		return "", errors.New("name must contain letters or numbers")
	}
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if reservedSlugs[candidate] {
			continue
		}
		if !s.workloadSlugExists(candidate) {
			return candidate, nil
		}
	}
	return "", errors.New("could not find a free slug")
}

// hostnameTaken checks both projects and addons. Object storage uses an
// "s3-<name>" hostname, so checking only projects is not enough.
func (s *Server) hostnameTaken(host string) bool {
	if _, err := s.st.GetProjectByHostname(host); err == nil {
		return true
	}
	if _, err := s.st.GetAddonByHostname(host); err == nil {
		return true
	}
	return false
}

// warnSlugCollisions surfaces databases created before slug uniqueness was
// enforced across projects and addons. They cannot be renamed automatically,
// but the operator should know before the supervisor starts confusing them.
func (s *Server) warnSlugCollisions() {
	projects, err := s.st.ListProjects()
	if err != nil {
		return
	}
	addons, err := s.st.ListAddons()
	if err != nil {
		return
	}
	used := make(map[string]string, len(projects))
	for _, p := range projects {
		used[p.Slug] = p.Name
	}
	for _, a := range addons {
		if name, ok := used[a.Slug]; ok {
			s.log.Warn("workload slug collision; rename one of them",
				"slug", a.Slug, "project", name, "addon", a.Name)
			continue
		}
		used[a.Slug] = a.Name
	}
}

// projectSourceOnServer gates agent source downloads. A stack source belongs
// to the parent row, while placements belong to its child services, so both
// the project itself and its children are checked.
func (s *Server) projectSourceOnServer(p model.Project, serverID string) bool {
	if serverID == "" {
		return false
	}
	if placements, err := s.st.ListProjectPlacements(p.ID); err == nil {
		for _, pl := range placements {
			if pl.ServerID == serverID {
				return true
			}
		}
	}
	if children, err := s.st.ListChildProjects(p.ID); err == nil {
		for _, child := range children {
			placements, _ := s.st.ListProjectPlacements(child.ID)
			for _, pl := range placements {
				if pl.ServerID == serverID {
					return true
				}
			}
		}
	}
	return false
}

// normalizeCustomHostname validates an externally managed hostname and checks
// it is not already used by another project or addon.
func (s *Server) normalizeCustomHostname(v, projectID string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(v, ".")))
	if v == "" {
		return "", nil
	}
	if len(v) > 253 || !hostnamePattern.MatchString(v) {
		return "", errors.New("custom domain must be a valid lowercase hostname")
	}
	if v == "localhost" || isPrivateHost(v) {
		return "", errors.New("custom domain must be a public hostname")
	}
	if _, fleetHost := s.Edge(); fleetHost != "" && strings.EqualFold(v, fleetHost) {
		return "", errors.New("custom domain cannot be the panel hostname")
	}
	if p, err := s.st.GetProjectByHostname(v); err == nil && p.ID != projectID {
		return "", errors.New("custom domain is already used by another project")
	}
	if a, err := s.st.GetAddonByHostname(v); err == nil && a.ID != "" {
		return "", errors.New("custom domain is already used by a service")
	}
	return v, nil
}

func (s *Server) allocatePort(serverID string) (int, error) {
	used, err := s.st.UsedPorts(serverID)
	if err != nil {
		return 0, err
	}
	for p := portRangeStart; p <= portRangeEnd; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, errors.New("no free ports on this phone")
}

// serverStates builds the scheduler's view of the fleet.
func (s *Server) serverStates() ([]placement.Server, map[string]model.Server, error) {
	servers, err := s.st.ListServers()
	if err != nil {
		return nil, nil, err
	}
	counts := map[string]int{}
	if pls, err := s.st.ListPlacements(); err == nil {
		for _, p := range pls {
			counts[p.ServerID]++
		}
	}
	byID := make(map[string]model.Server, len(servers))
	out := make([]placement.Server, 0, len(servers))
	for _, srv := range servers {
		byID[srv.ID] = srv
		st := placement.Server{
			ID: srv.ID, Online: s.hub.Online(srv.ID), Drained: srv.Drain,
			NoUserland: !serverHasUserland(srv),
			Labels:     srv.Labels, Placements: counts[srv.ID], Score: 1000,
		}
		if latest, err := s.st.LatestMetrics(srv.ID); err == nil {
			score := latest.TempC*2 + latest.CPUPercent*0.5
			if latest.BatteryPct >= 0 && latest.BatteryPct < 20 {
				score += 200
			}
			if latest.Charging {
				score -= 50
			}
			st.Score = score
		}
		out = append(out, st)
	}
	return out, byID, nil
}

func selectorFor(p model.Project) map[string]string {
	sel, err := placement.ParseSelector(p.Selector)
	if err != nil {
		return nil
	}
	return sel
}

// ensurePlacements adds replicas until the project has its desired count.
func (s *Server) ensurePlacements(p model.Project) ([]model.Placement, error) {
	if p.Kind == "stack" {
		return nil, nil
	}
	existing, err := s.st.ListProjectPlacements(p.ID)
	if err != nil {
		return nil, err
	}
	desired := p.Replicas
	if desired < 1 {
		desired = 1
	}
	if len(existing) >= desired {
		return existing, nil
	}
	states, byID, err := s.serverStates()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, pl := range existing {
		have[pl.ServerID] = true
	}
	plan := placement.Plan(states, placement.Need{
		Selector: selectorFor(p), Pinned: p.PinnedServer, Replicas: desired, Existing: have,
	})
	// A pinned phone gets a placement even while offline: the project waits for
	// that specific device instead of landing somewhere else. A monitoring-only
	// phone does not: it can never run the app.
	if p.PinnedServer != "" && !have[p.PinnedServer] {
		if srv, ok := byID[p.PinnedServer]; !ok || serverHasUserland(srv) {
			plan = append([]string{p.PinnedServer}, plan...)
		}
	}
	for _, serverID := range plan {
		// Keep the read-alloc-insert sequence atomic. The single SQLite
		// connection protects each statement, not the gap between them.
		// Workers have no listener, so they reserve no port at all.
		port := 0
		locked := p.Kind != "worker"
		if locked {
			s.portMu.Lock()
			var err error
			port, err = s.allocatePort(serverID)
			if err != nil {
				s.portMu.Unlock()
				return existing, err
			}
		}
		pl := model.Placement{
			ProjectID: p.ID, ServerID: serverID, Port: port, Status: "pending", Desired: "running",
			UpdatedAt: time.Now().Unix(),
		}
		err = s.st.UpsertPlacement(pl)
		if locked {
			s.portMu.Unlock()
		}
		if err != nil {
			return existing, err
		}
		existing = append(existing, pl)
		_ = s.st.AddEvent("scheduler", "placement.add", p.ID, p.Name+" → "+serverID)
	}
	return existing, nil
}

func (s *Server) hasInflight(projectID string) bool {
	list, err := s.st.ListDeployments(projectID, 20)
	if err != nil {
		return false
	}
	for _, d := range list {
		if !isTerminalDeploy(d.Status) {
			return true
		}
	}
	return false
}

// startDeployment creates a deployment row and sends the plan to one phone.
func (s *Server) startDeployment(p model.Project, pl model.Placement, ref string) (model.Deployment, error) {
	if !s.hub.Online(pl.ServerID) {
		return model.Deployment{}, errors.New("phone is offline")
	}
	if m, err := s.st.LatestMetrics(pl.ServerID); err == nil {
		if m.TempC >= 70 {
			return model.Deployment{}, fmt.Errorf("phone is at %.0f°C; let it cool before deploying", m.TempC)
		}
		if m.BatteryPct >= 0 && m.BatteryPct < 10 && !m.Charging {
			return model.Deployment{}, errors.New("phone battery is below 10% and not charging")
		}
	}
	sourceID := p.ID
	sourceType := p.SourceType
	repo := p.Repo
	if p.ParentID != "" {
		if parent, err := s.st.GetProject(p.ParentID); err == nil {
			sourceID = parent.ID
			if strings.TrimSpace(repo) == "" {
				repo = parent.Repo
			}
			if parent.SourceType == "upload" {
				sourceType = "upload"
			}
		}
	}
	sourcePath := ""
	if sourceType == "upload" {
		if !s.hasSourceBundle(sourceID) {
			return model.Deployment{}, errors.New("upload a source folder before deploying")
		}
		sourcePath = "/api/projects/" + sourceID + "/source"
	} else if strings.TrimSpace(repo) == "" {
		return model.Deployment{}, errors.New("repository is not set")
	}
	if p.Kind != "static" && strings.TrimSpace(p.StartCmd) == "" {
		return model.Deployment{}, errors.New("start command is not set")
	}
	env, err := s.decryptEnv(p)
	if err != nil {
		return model.Deployment{}, fmt.Errorf("cannot decrypt environment: %w", err)
	}
	secretFiles, err := s.projectSecrets(p.ID)
	if err != nil {
		return model.Deployment{}, fmt.Errorf("cannot decrypt secret files: %w", err)
	}
	if p.ParentID != "" {
		// Shared stack env is inherited; per-service values win.
		if parent, err := s.st.GetProject(p.ParentID); err == nil {
			if shared, err := s.decryptEnv(parent); err == nil {
				for k, v := range env {
					shared[k] = v
				}
				env = shared
			}
		}
		// Same-phone services get private loopback URLs, e.g.
		// PHLEET_SERVICE_API_URL=http://127.0.0.1:4102
		if siblings, err := s.st.ListChildProjects(p.ParentID); err == nil {
			for _, sibling := range siblings {
				if sibling.ID == p.ID {
					continue
				}
				placements, _ := s.st.ListProjectPlacements(sibling.ID)
				for _, sp := range placements {
					if sp.ServerID != pl.ServerID || sp.Port <= 0 {
						continue
					}
					key := "PHLEET_SERVICE_" + strings.ToUpper(strings.ReplaceAll(sibling.Slug, "-", "_")) + "_URL"
					env[key] = fmt.Sprintf("http://127.0.0.1:%d", sp.Port)
				}
			}
		}
	}
	if err := s.injectAddonEnv(p, pl.ServerID, env); err != nil {
		return model.Deployment{}, err
	}
	dep := model.Deployment{
		ID:        uuid.NewString(),
		ProjectID: p.ID,
		ServerID:  pl.ServerID,
		Status:    "queued",
		StartedAt: time.Now().Unix(),
	}
	if err := s.st.CreateDeployment(dep); err != nil {
		return dep, err
	}
	_ = s.st.UpdatePlacementStatusFor(p.ID, pl.ServerID, "deploying", pl.PID, pl.Restarts, "", pl.Release)
	req := agentproto.DeployRequest{
		DeploymentID: dep.ID, ProjectID: p.ID, Slug: p.Slug, Name: p.Name,
		Kind: p.Kind, Ref: ref, Repo: repo, Branch: p.Branch, BuildCmd: p.BuildCmd, StartCmd: p.StartCmd,
		StaticDir: p.StaticDir, HealthPath: p.HealthPath, Port: pl.Port, SourcePath: sourcePath,
		Env: env, Secrets: secretFiles, KeepReleases: 5, RestartPolicy: "always",
	}
	if p.Kind == "worker" {
		// A worker has no listener; an HTTP health path would always fail.
		req.HealthPath = ""
	}
	if err := s.hub.Send(pl.ServerID, agentproto.TypeDeployRequest, req); err != nil {
		_ = s.st.FinishDeployment(dep.ID, "failed", "", err.Error())
		_ = s.st.UpdatePlacementStatusFor(p.ID, pl.ServerID, "failed", 0, 0, err.Error(), "")
		return dep, err
	}
	return dep, nil
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.st.ListProjects()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	admin := s.panelRole(r) == "admin"
	out := make([]projectView, 0, len(projects))
	for _, p := range projects {
		if p.ParentID != "" {
			continue
		}
		v := s.buildProjectView(p, false)
		if !admin {
			redactProjectRepo(&v)
		}
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"projects": out})
}

type createServiceBody struct {
	Name       string            `json:"name"`
	Slug       string            `json:"slug"`
	Kind       string            `json:"kind"`
	BuildCmd   string            `json:"buildCmd"`
	StartCmd   string            `json:"startCmd"`
	StaticDir  string            `json:"staticDir"`
	HealthPath string            `json:"healthPath"`
	Env        map[string]string `json:"env"`
}

type createProjectBody struct {
	Name           string              `json:"name"`
	Slug           string              `json:"slug"`
	Kind           string              `json:"kind"`
	Repo           string              `json:"repo"`
	Branch         string              `json:"branch"`
	BuildCmd       string              `json:"buildCmd"`
	StartCmd       string              `json:"startCmd"`
	StaticDir      string              `json:"staticDir"`
	HealthPath     string              `json:"healthPath"`
	CustomHostname string              `json:"customHostname"`
	ServerID       string              `json:"serverId"`
	PinnedServer   string              `json:"pinnedServer"`
	Selector       string              `json:"selector"`
	Replicas       int                 `json:"replicas"`
	SourceType     string              `json:"sourceType"`
	Env            map[string]string   `json:"env"`
	Stack          bool                `json:"stack"`
	Services       []createServiceBody `json:"services"`
}

func normalizeHealthPath(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "/"
	}
	if !strings.HasPrefix(v, "/") {
		return "/" + v
	}
	return v
}

func normalizeProjectKind(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "static":
		return "static"
	case "worker":
		return "worker"
	}
	return "app"
}

// normalizeStaticDir makes the publish directory relative to the release root.
// Absolute paths and traversal are rejected so a project cannot publish the
// phone's filesystem by accident.
func normalizeStaticDir(v string) (string, error) {
	v = strings.TrimSpace(strings.ReplaceAll(v, "\\", "/"))
	v = strings.TrimPrefix(v, "./")
	if v == "" || v == "." {
		return "", nil
	}
	v = path.Clean(v)
	if v == "." {
		return "", nil
	}
	if path.IsAbs(v) || v == ".." || strings.HasPrefix(v, "../") {
		return "", errors.New("static directory must be a relative path inside the release")
	}
	return v, nil
}

func projectHasSource(p model.Project) bool {
	return p.SourceType == "upload" || strings.TrimSpace(p.Repo) != ""
}

// projectDeployable reports whether a project has enough configuration to
// deploy. Static sites have no start command; workers have no listener.
func projectDeployable(p model.Project) bool {
	if !projectHasSource(p) {
		return false
	}
	return p.Kind == "static" || strings.TrimSpace(p.StartCmd) != ""
}

// normalizeDeployRef accepts a branch, tag or commit SHA. Git refs are passed
// as an argv element, not through a shell, but reject obvious traversal and
// option-like values anyway.
func isShortHexRef(v string) bool {
	if len(v) < 7 || len(v) >= 40 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func normalizeDeployRef(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !deployRefPattern.MatchString(v) ||
		strings.Contains(v, "..") ||
		strings.Contains(v, "//") ||
		strings.HasSuffix(v, "/") ||
		strings.HasSuffix(v, ".lock") {
		return "", errors.New("ref must be a branch, tag or commit SHA")
	}
	// Git cannot fetch an arbitrary short SHA with --depth 1. Treat it as an
	// error up front instead of failing later on the phone with a confusing
	// checkout message. A 40-character SHA still works.
	if isShortHexRef(v) {
		return "", errors.New("short commit SHAs are not supported; use the full 40-character SHA, or a branch/tag name")
	}
	return v, nil
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body createProjectBody
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		apiErr(w, 400, errors.New("name is required"))
		return
	}
	if body.Stack || len(body.Services) > 0 {
		if strings.TrimSpace(body.CustomHostname) != "" {
			apiErr(w, 400, errors.New("custom domains are set per service, not on the stack"))
			return
		}
		s.handleCreateStack(w, body)
		return
	}
	base := strings.TrimSpace(body.Slug)
	if base == "" {
		base = slugify(name)
	}
	if !slugPattern.MatchString(base) {
		apiErr(w, 400, errors.New("slug must be lowercase letters, numbers and dashes"))
		return
	}
	if reservedSlugs[base] {
		apiErr(w, 400, fmt.Errorf("slug %q is reserved", base))
		return
	}
	kind := normalizeProjectKind(body.Kind)
	staticDir, err := normalizeStaticDir(body.StaticDir)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	slug, err := s.uniqueSlug(base)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	hostname := ""
	customHostname := ""
	if kind != "worker" {
		hostname = slug + "." + s.appSuffix()
		if s.hostnameTaken(hostname) {
			apiErr(w, 409, fmt.Errorf("hostname %s is already in use", hostname))
			return
		}
		customHostname, err = s.normalizeCustomHostname(body.CustomHostname, "")
		if err != nil {
			apiErr(w, 400, err)
			return
		}
	} else if strings.TrimSpace(body.CustomHostname) != "" {
		apiErr(w, 400, errors.New("a worker has no hostname; custom domains apply to web and static projects"))
		return
	}

	sel, err := placement.ParseSelector(body.Selector)
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	if len(sel) > 0 && (body.ServerID != "" || body.PinnedServer != "") {
		apiErr(w, 400, errors.New("use either a label selector or a pinned phone, not both"))
		return
	}
	pinned := strings.TrimSpace(body.PinnedServer)
	if pinned == "" {
		pinned = strings.TrimSpace(body.ServerID)
	}
	if pinned != "" {
		srv, err := s.st.GetServer(pinned)
		if err != nil {
			apiErr(w, 400, errors.New("selected phone was not found"))
			return
		}
		if !serverHasUserland(srv) {
			apiErr(w, 409, noUserlandError(srv, "it cannot build or run projects"))
			return
		}
	}
	replicas := body.Replicas
	if replicas <= 0 {
		replicas = 1
	}
	if replicas > maxReplicas {
		replicas = maxReplicas
	}

	branch := strings.TrimSpace(body.Branch)
	if branch == "" {
		branch = "main"
	}
	healthPath := strings.TrimSpace(body.HealthPath)
	if healthPath == "" {
		healthPath = "/"
	}
	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}
	envEnc, err := s.encryptEnv(body.Env)
	if err != nil {
		apiErr(w, 500, err)
		return
	}

	srcType := strings.TrimSpace(body.SourceType)
	if srcType == "" {
		srcType = "git"
	}
	if srcType != "git" && srcType != "upload" {
		apiErr(w, 400, errors.New("sourceType must be git or upload"))
		return
	}
	repo := strings.TrimSpace(body.Repo)
	if srcType == "upload" {
		repo = ""
	}

	startCmd := strings.TrimSpace(body.StartCmd)
	if kind == "static" {
		// A static site is served by the agent, not by a user process.
		startCmd = ""
	}
	p := model.Project{
		ID: uuid.NewString(), Name: name, Slug: slug, Repo: repo,
		Branch: branch, Runtime: "auto", BuildCmd: strings.TrimSpace(body.BuildCmd),
		StartCmd: startCmd, StaticDir: staticDir, HealthPath: healthPath, Hostname: hostname,
		CustomHostname: customHostname, Kind: kind,
		Selector: placement.FormatSelector(sel), Replicas: replicas, PinnedServer: pinned,
		SourceType: srcType, EnvEnc: envEnc, CreatedAt: time.Now().Unix(),
	}
	if err := s.st.CreateProject(p); err != nil {
		apiErr(w, 500, err)
		return
	}
	placements, _ := s.ensurePlacements(p)
	if len(placements) == 0 {
		_ = s.st.AddEvent("scheduler", "placement.none", p.ID, "no eligible phone online yet")
	}
	s.triggerReconcile()
	_ = s.st.AddEvent("ui", "project.create", p.ID, name)
	writeJSON(w, 201, map[string]any{"project": s.buildProjectView(p, true)})
}

func (s *Server) handleCreateStack(w http.ResponseWriter, body createProjectBody) {
	name := strings.TrimSpace(body.Name)
	base := strings.TrimSpace(body.Slug)
	if base == "" {
		base = slugify(name)
	}
	if !slugPattern.MatchString(base) {
		apiErr(w, 400, errors.New("stack slug must be lowercase letters, numbers and dashes"))
		return
	}
	if reservedSlugs[base] {
		apiErr(w, 400, fmt.Errorf("slug %q is reserved", base))
		return
	}
	if len(body.Services) == 0 {
		apiErr(w, 400, errors.New("add at least one service"))
		return
	}
	if len(body.Services) > 20 {
		apiErr(w, 400, errors.New("a stack supports at most 20 services"))
		return
	}
	srcType := strings.TrimSpace(body.SourceType)
	if srcType == "" {
		srcType = "git"
	}
	if srcType != "git" && srcType != "upload" {
		apiErr(w, 400, errors.New("sourceType must be git or upload"))
		return
	}
	repo := strings.TrimSpace(body.Repo)
	if srcType == "upload" {
		repo = ""
	} else if repo == "" {
		apiErr(w, 400, errors.New("repository is required"))
		return
	}
	branch := strings.TrimSpace(body.Branch)
	if branch == "" {
		branch = "main"
	}
	pinned := strings.TrimSpace(body.PinnedServer)
	if pinned == "" {
		pinned = strings.TrimSpace(body.ServerID)
	}
	if pinned == "" {
		apiErr(w, 400, errors.New("pin the stack to a phone so its services can reach each other over localhost"))
		return
	}
	if srv, err := s.st.GetServer(pinned); err != nil {
		apiErr(w, 400, errors.New("selected phone was not found"))
		return
	} else if !serverHasUserland(srv) {
		apiErr(w, 409, noUserlandError(srv, "it cannot build or run stack services"))
		return
	}
	envEnc, err := s.encryptEnv(body.Env)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	parentSlug, err := s.uniqueSlug(base + "-stack")
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	now := time.Now().Unix()
	parent := model.Project{
		ID: uuid.NewString(), Name: name, Slug: parentSlug, Repo: repo, Branch: branch,
		Runtime: "auto", Hostname: "", Selector: "", Replicas: 1, PinnedServer: pinned,
		SourceType: srcType, Kind: "stack", EnvEnc: envEnc, CreatedAt: now,
	}
	if err := s.st.CreateProject(parent); err != nil {
		apiErr(w, 500, err)
		return
	}
	fail := func(code int, err error) {
		_ = s.st.DeleteProject(parent.ID)
		apiErr(w, code, err)
	}

	for i, svc := range body.Services {
		svcName := strings.TrimSpace(svc.Name)
		if svcName == "" {
			fail(400, fmt.Errorf("service %d needs a name", i+1))
			return
		}
		svcKind := normalizeProjectKind(svc.Kind)
		svcStaticDir, err := normalizeStaticDir(svc.StaticDir)
		if err != nil {
			fail(400, fmt.Errorf("service %s: %w", svcName, err))
			return
		}
		startCmd := strings.TrimSpace(svc.StartCmd)
		if svcKind == "static" {
			startCmd = ""
		} else if startCmd == "" {
			fail(400, fmt.Errorf("service %s needs a start command", svcName))
			return
		}
		svcSlug := strings.TrimSpace(svc.Slug)
		if svcSlug == "" {
			if i == 0 {
				svcSlug = base
			} else {
				svcSlug = base + "-" + slugify(svcName)
			}
		}
		if !slugPattern.MatchString(svcSlug) {
			fail(400, fmt.Errorf("service %s: subdomain must be lowercase letters, numbers and dashes", svcName))
			return
		}
		if reservedSlugs[svcSlug] {
			fail(400, fmt.Errorf("subdomain %q is reserved", svcSlug))
			return
		}
		if s.workloadSlugExists(svcSlug) {
			fail(409, fmt.Errorf("subdomain %s is already in use", svcSlug))
			return
		}
		hostname := ""
		if svcKind != "worker" {
			hostname = svcSlug + "." + s.appSuffix()
			if s.hostnameTaken(hostname) {
				fail(409, fmt.Errorf("hostname %s is already in use", hostname))
				return
			}
		}
		svcEnvEnc, err := s.encryptEnv(svc.Env)
		if err != nil {
			fail(500, err)
			return
		}
		child := model.Project{
			ID: uuid.NewString(), Name: svcName, Slug: svcSlug, Repo: repo, Branch: branch,
			Runtime: "auto", BuildCmd: strings.TrimSpace(svc.BuildCmd), StartCmd: startCmd,
			StaticDir: svcStaticDir, HealthPath: normalizeHealthPath(svc.HealthPath), Hostname: hostname, Selector: "",
			Replicas: 1, PinnedServer: pinned, SourceType: srcType, ParentID: parent.ID,
			Kind: svcKind, StackOrder: i, EnvEnc: svcEnvEnc, CreatedAt: now,
		}
		if err := s.st.CreateProject(child); err != nil {
			fail(500, err)
			return
		}
		if _, err := s.ensurePlacements(child); err != nil {
			fail(409, err)
			return
		}
	}
	_ = s.st.AddEvent("ui", "stack.create", parent.ID, fmt.Sprintf("%s (%d services)", name, len(body.Services)))
	s.triggerReconcile()
	writeJSON(w, 201, map[string]any{"project": s.buildProjectView(parent, true)})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	admin := s.panelRole(r) == "admin"
	v := s.buildProjectView(p, admin)
	if !admin {
		redactProjectRepo(&v)
	}
	writeJSON(w, 200, map[string]any{"project": v})
}

func redactProjectRepo(v *projectView) {
	// Repository URLs sometimes carry credentials; never expose them to a
	// read-only API token. Stack children inherit the parent repo.
	v.Repo = ""
	for i := range v.Services {
		redactProjectRepo(&v.Services[i])
	}
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		Name              *string            `json:"name"`
		Repo              *string            `json:"repo"`
		Branch            *string            `json:"branch"`
		BuildCmd          *string            `json:"buildCmd"`
		StartCmd          *string            `json:"startCmd"`
		StaticDir         *string            `json:"staticDir"`
		HealthPath        *string            `json:"healthPath"`
		BasicAuthUser     *string            `json:"basicAuthUser"`
		BasicAuthPassword *string            `json:"basicAuthPassword"`
		ClearBasicAuth    *bool              `json:"clearBasicAuth"`
		CustomHostname    *string            `json:"customHostname"`
		Env               *map[string]string `json:"env"`
		Selector          *string            `json:"selector"`
		Replicas          *int               `json:"replicas"`
		PinnedServer      *string            `json:"pinnedServer"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		p.Name = strings.TrimSpace(*body.Name)
	}
	if body.Repo != nil {
		p.Repo = strings.TrimSpace(*body.Repo)
	}
	if body.Branch != nil && strings.TrimSpace(*body.Branch) != "" {
		p.Branch = strings.TrimSpace(*body.Branch)
	}
	if body.BuildCmd != nil {
		p.BuildCmd = strings.TrimSpace(*body.BuildCmd)
	}
	if body.StartCmd != nil {
		p.StartCmd = strings.TrimSpace(*body.StartCmd)
		if p.Kind == "static" {
			p.StartCmd = ""
		}
	}
	if body.StaticDir != nil {
		v, err := normalizeStaticDir(*body.StaticDir)
		if err != nil {
			apiErr(w, 400, err)
			return
		}
		p.StaticDir = v
	}
	if body.ClearBasicAuth != nil && *body.ClearBasicAuth {
		p.BasicAuthUser = ""
		p.BasicAuthHash = ""
	}
	if body.BasicAuthUser != nil {
		p.BasicAuthUser = strings.TrimSpace(*body.BasicAuthUser)
	}
	if body.BasicAuthPassword != nil && *body.BasicAuthPassword != "" {
		hash, err := auth.HashPassword(*body.BasicAuthPassword)
		if err != nil {
			apiErr(w, 400, fmt.Errorf("basic auth: %w", err))
			return
		}
		p.BasicAuthHash = hash
	}
	if (p.BasicAuthUser == "") != (p.BasicAuthHash == "") {
		apiErr(w, 400, errors.New("basic auth needs both a username and a password"))
		return
	}
	if body.HealthPath != nil {
		hp := strings.TrimSpace(*body.HealthPath)
		if hp == "" {
			hp = "/"
		}
		if !strings.HasPrefix(hp, "/") {
			hp = "/" + hp
		}
		p.HealthPath = hp
	}
	if body.CustomHostname != nil {
		if p.Kind == "stack" && strings.TrimSpace(*body.CustomHostname) != "" {
			apiErr(w, 400, errors.New("set a custom domain on a service, not on the stack"))
			return
		}
		if p.Kind == "worker" && strings.TrimSpace(*body.CustomHostname) != "" {
			apiErr(w, 400, errors.New("a worker has no hostname"))
			return
		}
		norm, err := s.normalizeCustomHostname(*body.CustomHostname, p.ID)
		if err != nil {
			apiErr(w, 400, err)
			return
		}
		p.CustomHostname = norm
	}
	if body.Env != nil {
		enc, err := s.encryptEnv(*body.Env)
		if err != nil {
			apiErr(w, 500, err)
			return
		}
		p.EnvEnc = enc
	}
	if body.Selector != nil {
		sel, err := placement.ParseSelector(*body.Selector)
		if err != nil {
			apiErr(w, 400, err)
			return
		}
		p.Selector = placement.FormatSelector(sel)
		if len(sel) > 0 {
			p.PinnedServer = ""
		}
	}
	if body.PinnedServer != nil {
		pinned := strings.TrimSpace(*body.PinnedServer)
		if pinned != "" {
			if _, err := s.st.GetServer(pinned); err != nil {
				apiErr(w, 400, errors.New("pinned phone was not found"))
				return
			}
			p.PinnedServer = pinned
			p.Selector = ""
		} else {
			p.PinnedServer = ""
		}
	}
	if body.Replicas != nil {
		r := *body.Replicas
		if r < 1 {
			r = 1
		}
		if r > maxReplicas {
			r = maxReplicas
		}
		p.Replicas = r
	}
	if err := s.st.UpdateProject(p); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "project.update", p.ID, p.Name)
	s.triggerReconcile()
	if p.Kind == "stack" {
		if children, err := s.st.ListChildProjects(p.ID); err == nil {
			for _, child := range children {
				go s.syncTasksForProject(child.ID)
			}
		}
	} else {
		go s.syncTasksForProject(p.ID)
	}
	writeJSON(w, 200, map[string]any{"project": s.buildProjectView(p, true)})
}

// stackAddonSets splits the addons attached to a stack (and its children) into
// those used only by this stack and those shared with another project. A stack
// stop/delete only owns the first group; a shared database stays running.
func (s *Server) stackAddonSets(p model.Project) (owned, shared []model.Addon) {
	if p.Kind != "stack" {
		owned, _ = s.st.OwnedAddons(p.ID)
		return owned, nil
	}
	ids := map[string]bool{p.ID: true}
	if children, err := s.st.ListChildProjects(p.ID); err == nil {
		for _, c := range children {
			ids[c.ID] = true
		}
	}
	seen := map[string]bool{}
	add := func(a model.Addon) {
		if seen[a.ID] {
			return
		}
		seen[a.ID] = true
		bindings, _ := s.st.ListAddonBindings(a.ID)
		isShared := false
		for _, b := range bindings {
			if !ids[b.ProjectID] {
				isShared = true
				break
			}
		}
		if isShared {
			shared = append(shared, a)
		} else {
			owned = append(owned, a)
		}
	}
	for id := range ids {
		if addons, err := s.st.ListProjectAddons(id); err == nil {
			for _, a := range addons {
				add(a)
			}
		}
	}
	if ownedByProject, err := s.st.OwnedAddons(p.ID); err == nil {
		for _, a := range ownedByProject {
			add(a)
		}
	}
	return owned, shared
}

func (s *Server) stackOwnAddons(p model.Project) []model.Addon {
	owned, _ := s.stackAddonSets(p)
	return owned
}

func addonRefs(addons []model.Addon) []map[string]string {
	out := make([]map[string]string, 0, len(addons))
	for _, a := range addons {
		out = append(out, map[string]string{"id": a.ID, "name": a.Name})
	}
	return out
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var deleteAddons, sharedAddons []model.Addon
	if p.Kind == "stack" {
		deleteAddons, sharedAddons = s.stackAddonSets(p)
		if len(deleteAddons) > 0 && !truthy(r.URL.Query().Get("addons")) {
			names := make([]string, 0, len(deleteAddons))
			for _, a := range deleteAddons {
				names = append(names, a.Name)
			}
			writeJSON(w, 409, map[string]any{
				"error": fmt.Sprintf("this stack owns %s; deleting it also deletes %s and all of the data. Confirm with addons=delete.",
					strings.Join(names, ", "), "them"),
				"code":   "addons_attached",
				"addons": addonRefs(deleteAddons),
				"shared": addonRefs(sharedAddons),
			})
			return
		}
	} else if owned, err := s.st.OwnedAddons(p.ID); err == nil && len(owned) > 0 {
		names := make([]string, 0, len(owned))
		for _, a := range owned {
			names = append(names, a.Name)
		}
		apiErr(w, 409, fmt.Errorf("this project created %s; delete the database (or keep it as standalone) before deleting the project", strings.Join(names, ", ")))
		return
	}
	targets := []model.Project{p}
	if p.Kind == "stack" {
		if children, err := s.st.ListChildProjects(p.ID); err == nil {
			targets = append(targets, children...)
		}
	}
	taskServers := map[string]bool{}
	for _, target := range targets {
		if tasks, err := s.st.ListTasksForProject(target.ID); err == nil {
			for _, task := range tasks {
				taskServers[task.ServerID] = true
			}
		}
		placements, _ := s.st.ListProjectPlacements(target.ID)
		for _, pl := range placements {
			taskServers[pl.ServerID] = true
			if !s.hub.Online(pl.ServerID) {
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			_, _ = s.hub.Call(ctx, pl.ServerID, agentproto.TypeAppControl,
				agentproto.AppControlRequest{Slug: target.Slug, Action: "delete"})
			cancel()
		}
	}
	// The apps are stopped; now remove the databases that belong to the stack.
	for _, a := range deleteAddons {
		if err := s.deleteAddon(r.Context(), a, true); err != nil {
			apiErr(w, 500, fmt.Errorf("delete addon %s: %w", a.Name, err))
			return
		}
	}
	if err := s.st.DeleteProject(p.ID); err != nil {
		apiErr(w, 500, err)
		return
	}
	for _, target := range targets {
		_ = s.st.DeleteProjectBindings(target.ID)
		_, _ = s.st.ResolveAlert("app:" + target.ID + ":crashed")
		_, _ = s.st.ResolveAlert("deploy:" + target.ID)
	}
	_ = os.Remove(s.sourceBundlePath(p.ID))
	_ = s.st.AddEvent("ui", "project.delete", p.ID, p.Name)
	for serverID := range taskServers {
		go s.syncTasksForServer(serverID)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

type deployResult struct {
	ID       string `json:"id"`
	ServerID string `json:"serverId"`
	Port     int    `json:"port"`
	Status   string `json:"status"`
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	// Serialize the inflight check and deployment-row creation. Two concurrent
	// clicks or a webhook racing the panel must not queue duplicate releases.
	s.deployMu.Lock()
	defer s.deployMu.Unlock()
	ref := ""
	if r.ContentLength != 0 {
		var body struct {
			Ref string `json:"ref"`
		}
		if err := readJSON(r, &body, 1<<16); err != nil {
			apiErr(w, 400, err)
			return
		}
		ref, err = normalizeDeployRef(body.Ref)
		if err != nil {
			apiErr(w, 400, err)
			return
		}
		if ref != "" && p.SourceType != "git" {
			apiErr(w, 400, errors.New("ref deploys need a git source"))
			return
		}
	}
	if p.Kind == "stack" {
		s.handleDeployStack(w, p, ref)
		return
	}
	if p.SourceType == "upload" {
		// A stack child shares the parent's uploaded bundle; only the parent
		// owns a file under bundles/.
		sourceID := p.ID
		if p.ParentID != "" {
			sourceID = p.ParentID
		}
		if !s.hasSourceBundle(sourceID) {
			apiErr(w, 400, errors.New("upload a source folder first"))
			return
		}
	} else if strings.TrimSpace(p.Repo) == "" {
		apiErr(w, 400, errors.New("set a git repository first"))
		return
	}
	if p.Kind != "static" && strings.TrimSpace(p.StartCmd) == "" {
		apiErr(w, 400, errors.New("set a start command first"))
		return
	}
	if addons, err := s.st.ListProjectAddons(p.ID); err == nil {
		for _, a := range addons {
			if a.Kind == engines.KindFile && p.Replicas > 1 {
				apiErr(w, 409, errors.New("this project uses a SQLite database file; run one replica or use PostgreSQL"))
				return
			}
		}
	}
	if s.hasInflight(p.ID) {
		apiErr(w, 409, errors.New("a deployment is already in progress"))
		return
	}
	placements, err := s.ensurePlacements(p)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	if len(placements) == 0 {
		apiErr(w, 409, errors.New("no eligible phone is online for this project"))
		return
	}
	// Never send a build to a phone that cannot run it. Existing placements on
	// a monitoring-only phone are skipped here; the reconciler moves them once
	// another eligible phone exists.
	blocked := []string{}
	deployable := make([]model.Placement, 0, len(placements))
	for _, pl := range placements {
		srv, err := s.st.GetServer(pl.ServerID)
		if err == nil && !serverHasUserland(srv) {
			blocked = append(blocked, serverLabel(srv))
			continue
		}
		deployable = append(deployable, pl)
	}
	if len(deployable) == 0 {
		apiErr(w, 409, fmt.Errorf("%s cannot build or run projects (no Termux or userland); pick another phone",
			strings.Join(blocked, ", ")))
		return
	}
	placements = deployable

	type result = deployResult
	results := make([]result, 0, len(placements))
	skipped := []string{}
	for _, pl := range placements {
		dep, err := s.startDeployment(p, pl, ref)
		if err != nil {
			if !s.hub.Online(pl.ServerID) {
				skipped = append(skipped, pl.ServerID)
				continue
			}
			results = append(results, result{ServerID: pl.ServerID, Status: "failed"})
			continue
		}
		results = append(results, result{ID: dep.ID, ServerID: pl.ServerID, Port: pl.Port, Status: dep.Status})
	}
	if len(results) == 0 {
		apiErr(w, 409, errors.New("no online replica could be deployed"))
		return
	}
	_ = s.st.AddEvent("ui", "deploy.start", p.ID, p.Name)
	writeJSON(w, 202, map[string]any{"deployments": results, "skipped": skipped})
}

func (s *Server) handleDeployStack(w http.ResponseWriter, p model.Project, ref string) {
	children, err := s.st.ListChildProjects(p.ID)
	if err != nil || len(children) == 0 {
		apiErr(w, 409, errors.New("this stack has no services"))
		return
	}
	if p.SourceType == "upload" && !s.hasSourceBundle(p.ID) {
		apiErr(w, 400, errors.New("upload a source folder first"))
		return
	}
	for _, child := range children {
		if s.hasInflight(child.ID) {
			apiErr(w, 409, fmt.Errorf("%s is already deploying", child.Name))
			return
		}
	}
	results := make([]deployResult, 0, len(children))
	skipped := []string{}
	for _, child := range children {
		placements, err := s.ensurePlacements(child)
		if err != nil {
			results = append(results, deployResult{ServerID: child.PinnedServer, Status: "failed"})
			continue
		}
		if len(placements) == 0 {
			results = append(results, deployResult{ServerID: child.PinnedServer, Status: "failed"})
			continue
		}
		for _, pl := range placements {
			dep, err := s.startDeployment(child, pl, ref)
			if err != nil {
				if !s.hub.Online(pl.ServerID) {
					skipped = append(skipped, pl.ServerID)
					continue
				}
				results = append(results, deployResult{ServerID: pl.ServerID, Status: "failed"})
				continue
			}
			results = append(results, deployResult{ID: dep.ID, ServerID: pl.ServerID, Port: pl.Port, Status: dep.Status})
		}
	}
	if len(results) == 0 {
		apiErr(w, 409, errors.New("no online phone could deploy this stack"))
		return
	}
	_ = s.st.AddEvent("ui", "deploy.start", p.ID, fmt.Sprintf("%s (%d services)", p.Name, len(children)))
	writeJSON(w, 202, map[string]any{"deployments": results, "skipped": skipped})
}

func (s *Server) handleProjectAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.st.GetProject(r.PathValue("id"))
		if err != nil {
			apiErr(w, 404, err)
			return
		}
		if p.Kind == "stack" {
			children, err := s.st.ListChildProjects(p.ID)
			if err != nil || len(children) == 0 {
				apiErr(w, 409, errors.New("this stack has no services"))
				return
			}
			addons := s.stackOwnAddons(p)
			results := []map[string]any{}
			// Dependencies start first and stop last, so a start has its
			// database ready and a stop shuts the apps down cleanly. A stack
			// restart brings its databases back too: restart running addons and
			// start stopped ones before touching the services.
			if action == "start" || action == "restart" {
				for _, a := range addons {
					addonAction := "start"
					if action == "restart" && a.Status == "running" {
						addonAction = "restart"
					}
					if _, err := s.runAddonAction(r.Context(), a, addonAction); err != nil {
						results = append(results, map[string]any{"addon": a.Name, "ok": false, "error": err.Error()})
					} else {
						results = append(results, map[string]any{"addon": a.Name, "ok": true})
					}
				}
			}
			okCount := 0
			for _, child := range children {
				childResults, childOK := s.runProjectAction(r.Context(), child, action)
				results = append(results, childResults...)
				okCount += childOK
			}
			if action == "stop" {
				for _, a := range addons {
					if _, err := s.runAddonAction(r.Context(), a, "stop"); err != nil {
						results = append(results, map[string]any{"addon": a.Name, "ok": false, "error": err.Error()})
					} else {
						results = append(results, map[string]any{"addon": a.Name, "ok": true})
					}
				}
			}
			if okCount == 0 {
				apiErr(w, 409, errors.New("no online service accepted the action"))
				return
			}
			_ = s.st.AddEvent("ui", "project."+action, p.ID, p.Name)
			writeJSON(w, 200, map[string]any{"ok": true, "results": results})
			return
		}
		results, okCount := s.runProjectAction(r.Context(), p, action)
		if okCount == 0 {
			apiErr(w, 409, errors.New("no online replica accepted the action"))
			return
		}
		_ = s.st.AddEvent("ui", "project."+action, p.ID, p.Name)
		writeJSON(w, 200, map[string]any{"ok": true, "results": results})
	}
}

func (s *Server) runProjectAction(ctx context.Context, p model.Project, action string) ([]map[string]any, int) {
	placements, err := s.st.ListProjectPlacements(p.ID)
	if err != nil || len(placements) == 0 {
		return []map[string]any{{"serverId": p.PinnedServer, "ok": false, "error": "project has no placements"}}, 0
	}
	desired := "running"
	if action == "stop" {
		desired = "stopped"
	}
	if err := s.st.SetProjectDesired(p.ID, desired); err != nil {
		return []map[string]any{{"serverId": p.PinnedServer, "ok": false, "error": err.Error()}}, 0
	}
	results := []map[string]any{}
	okCount := 0
	for _, pl := range placements {
		if !s.hub.Online(pl.ServerID) {
			results = append(results, map[string]any{"serverId": pl.ServerID, "ok": false, "error": "offline"})
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := s.hub.Call(callCtx, pl.ServerID, agentproto.TypeAppControl,
			agentproto.AppControlRequest{Slug: p.Slug, Action: action})
		cancel()
		if err != nil {
			results = append(results, map[string]any{"serverId": pl.ServerID, "ok": false, "error": err.Error()})
			continue
		}
		okCount++
		status := "running"
		if action == "stop" {
			status = "stopped"
		}
		_ = s.st.UpdatePlacementStatusFor(p.ID, pl.ServerID, status, pl.PID, pl.Restarts, "", pl.Release)
		results = append(results, map[string]any{"serverId": pl.ServerID, "ok": true})
	}
	if action == "rollback" {
		now := time.Now().Unix()
		for _, pl := range placements {
			if !s.hub.Online(pl.ServerID) {
				continue
			}
			_ = s.st.CreateDeployment(model.Deployment{
				ID: uuid.NewString(), ProjectID: p.ID, ServerID: pl.ServerID,
				Status: "rolled_back", StartedAt: now, FinishedAt: now, Detail: "manual rollback",
			})
		}
	}
	return results, okCount
}

func (s *Server) handleDeployments(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.st.GetProject(id)
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if p.Kind == "stack" {
		children, _ := s.st.ListChildProjects(id)
		all := []model.Deployment{}
		for _, child := range children {
			list, _ := s.st.ListDeployments(child.ID, 50)
			all = append(all, list...)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].StartedAt > all[j].StartedAt })
		if len(all) > 50 {
			all = all[:50]
		}
		writeJSON(w, 200, map[string]any{"deployments": all})
		return
	}
	list, err := s.st.ListDeployments(id, 50)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"deployments": list})
}

func (s *Server) handleDeployment(w http.ResponseWriter, r *http.Request) {
	dep, err := s.st.GetDeployment(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	logs, err := s.st.ListDeployLogs(dep.ID, 2000)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"deployment": dep, "logs": logs})
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.GetProject(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	placements, err := s.st.ListProjectPlacements(p.ID)
	if err != nil || len(placements) == 0 {
		apiErr(w, 409, errors.New("project has no placements"))
		return
	}
	// Prefer an explicit server, then a running online replica.
	target := model.Placement{}
	requested := r.URL.Query().Get("serverId")
	for _, pl := range placements {
		if requested != "" && pl.ServerID != requested {
			continue
		}
		if s.hub.Online(pl.ServerID) && (target.ServerID == "" || pl.Status == "running") {
			target = pl
		}
	}
	if target.ServerID == "" {
		apiErr(w, 409, errors.New("no online replica for this project"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, target.ServerID, agentproto.TypeAppLogs, agentproto.AppLogsRequest{Slug: p.Slug, Bytes: 64 << 10})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.AppLogsResponse
	if err := json.Unmarshal(env.Data, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"slug": res.Slug, "path": res.Path, "size": res.Size, "serverId": target.ServerID,
		"truncated": res.Truncated, "logs": string(res.Data),
	})
}

// onDeployEvent persists agent deploy progress and mirrors it into the
// placement for that specific phone.
func (s *Server) onDeployEvent(serverID string, ev agentproto.DeployEvent) {
	if ev.DeploymentID == "" {
		return
	}
	switch ev.Phase {
	case "log":
		_ = s.st.AddDeployLog(ev.DeploymentID, ev.Line)
	case "status":
		dep, err := s.st.GetDeployment(ev.DeploymentID)
		if err != nil {
			return
		}
		release := dep.Release
		if ev.Release != "" {
			release = ev.Release
		}
		if ev.Commit != "" {
			_ = s.st.SetDeploymentCommit(ev.DeploymentID, ev.Commit)
		}
		if isTerminalDeploy(ev.Status) {
			_ = s.st.FinishDeployment(ev.DeploymentID, ev.Status, release, ev.Error)
			_ = s.st.AddEvent("agent", "deploy."+ev.Status, dep.ProjectID, ev.Error)
		} else {
			_ = s.st.UpdateDeploymentStatus(ev.DeploymentID, ev.Status, release, ev.Error)
		}
		switch ev.Status {
		case "active":
			_ = s.st.UpdatePlacementStatusFor(dep.ProjectID, dep.ServerID, "running", 0, 0, "", release)
			s.startDeployAlertResolve(dep.ProjectID)
		case "rolled_back":
			_ = s.st.UpdatePlacementStatusFor(dep.ProjectID, dep.ServerID, "running", 0, 0, ev.Error, release)
			s.startDeployAlertResolve(dep.ProjectID)
		case "failed":
			_ = s.st.UpdatePlacementStatusFor(dep.ProjectID, dep.ServerID, "failed", 0, 0, ev.Error, release)
		default:
			_ = s.st.UpdatePlacementStatusFor(dep.ProjectID, dep.ServerID, "deploying", 0, 0, "", release)
		}
		if isTerminalDeploy(ev.Status) {
			s.triggerReconcile()
			// A deploy changes the current release and possibly the env, so
			// refresh the task snapshot for this phone.
			go s.syncTasksForServer(serverID)
		}
		_ = serverID
	}
}

// syncAppStatus folds heartbeat app state into the placement on that phone.
// Apps that are no longer placed there (for example after a failover) are
// stopped so they do not linger forever.
func (s *Server) syncAppStatus(serverID string, app agentproto.AppStatus) {
	p, err := s.st.GetProjectBySlug(app.Slug)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			// A locked/corrupt database must never look like "the workload
			// was deleted" and stop a healthy app.
			s.log.Warn("resolve app slug", "slug", app.Slug, "err", err)
			return
		}
		// Addon instances share the same supervisor and heartbeat. Resolve
		// them by slug and let the addon path own the status update. If
		// neither exists, the workload was deleted while this phone was
		// offline; stop it instead of leaving it running forever.
		found, addonErr := s.syncAddonStatus(serverID, app)
		if addonErr != nil {
			s.log.Warn("resolve addon slug", "slug", app.Slug, "err", addonErr)
			return
		}
		if !found {
			s.cleanupUnknownApp(serverID, app)
		}
		return
	}
	placements, _ := s.st.ListProjectPlacements(p.ID)
	var mine *model.Placement
	for i := range placements {
		if placements[i].ServerID == serverID {
			mine = &placements[i]
			break
		}
	}
	if mine == nil {
		s.cleanupOrphanApp(serverID, p, app)
		return
	}
	state := app.State
	if state == "" {
		state = "unknown"
	}
	if mine.Status != state || mine.PID != app.PID || mine.Restarts != app.Restarts || mine.LastError != app.LastError {
		_ = s.st.UpdatePlacementStatusFor(p.ID, serverID, state, app.PID, app.Restarts, app.LastError, mine.Release)
	}
	if app.HasUsage && app.State == "running" && s.sampleAppMetric(p.ID) {
		if err := s.st.InsertAppMetric(p.ID, serverID, time.Now().Unix(), app.CPUPercent, app.RSS); err != nil {
			s.log.Debug("app metric", "project", p.Slug, "err", err)
		}
	}
	if mine.Desired == "stopped" && app.State == "running" {
		s.enforceStopped(serverID, p)
	}
	if app.DeploymentID != "" && app.DeployStatus != "" {
		dep, err := s.st.GetDeployment(app.DeploymentID)
		if err != nil || isTerminalDeploy(dep.Status) {
			return
		}
		if isTerminalDeploy(app.DeployStatus) {
			_ = s.st.FinishDeployment(app.DeploymentID, app.DeployStatus, dep.Release, app.LastError)
			_ = s.st.AddEvent("agent", "deploy."+app.DeployStatus, dep.ProjectID, "recovered from heartbeat")
			s.triggerReconcile()
		}
	}
}

func (s *Server) cleanupOrphanApp(serverID string, p model.Project, app agentproto.AppStatus) {
	if app.State == "" || app.State == "stopped" || app.State == "unknown" {
		return
	}
	// A project with no placements at all is also orphaned. Previously this
	// returned early, which is exactly the "deleted while offline" case.
	key := serverID + "/" + p.Slug
	s.orphanMu.Lock()
	last, seen := s.orphans[key]
	if seen && time.Since(last) < 10*time.Minute {
		s.orphanMu.Unlock()
		return
	}
	s.orphans[key] = time.Now()
	if len(s.orphans) > 2000 {
		for k, v := range s.orphans {
			if time.Since(v) > time.Hour {
				delete(s.orphans, k)
			}
		}
	}
	s.orphanMu.Unlock()
	s.log.Info("stopping orphaned app", "server", serverID, "slug", p.Slug)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = s.hub.Call(ctx, serverID, agentproto.TypeAppControl, agentproto.AppControlRequest{Slug: p.Slug, Action: "delete"})
	}()
	_ = s.st.AddEvent("scheduler", "placement.orphan_removed", p.ID, serverID)
}

// cleanupUnknownApp stops a supervised process whose slug is neither a
// project nor an addon. This is what closes the gap when a workload is deleted
// while its phone is offline: the rows are gone, the app is still running, and
// nothing else would ever tell the agent to stop it.
func (s *Server) cleanupUnknownApp(serverID string, app agentproto.AppStatus) {
	if app.Slug == "" || app.State == "" || app.State == "stopped" || app.State == "unknown" {
		return
	}
	key := "unknown:" + serverID + "/" + app.Slug
	s.orphanMu.Lock()
	last, seen := s.orphans[key]
	if seen && time.Since(last) < 10*time.Minute {
		s.orphanMu.Unlock()
		return
	}
	s.orphans[key] = time.Now()
	s.orphanMu.Unlock()
	s.log.Info("stopping unknown app", "server", serverID, "slug", app.Slug)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = s.hub.Call(ctx, serverID, agentproto.TypeAppControl,
			agentproto.AppControlRequest{Slug: app.Slug, Action: "delete"})
	}()
	_ = s.st.AddEvent("scheduler", "workload.unknown_removed", serverID, app.Slug)
}

// enforceStopped makes the device honour a stop that was requested while it
// was offline. Throttled so heartbeats do not spam the agent.
func (s *Server) enforceStopped(serverID string, p model.Project) {
	key := "stop:" + serverID + "/" + p.Slug
	s.orphanMu.Lock()
	if last, seen := s.orphans[key]; seen && time.Since(last) < time.Minute {
		s.orphanMu.Unlock()
		return
	}
	s.orphans[key] = time.Now()
	s.orphanMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = s.hub.Call(ctx, serverID, agentproto.TypeAppControl, agentproto.AppControlRequest{Slug: p.Slug, Action: "stop"})
	}()
}
