package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"phleet/internal/agentproto"
	"phleet/internal/engines"
	"phleet/internal/model"
	"phleet/internal/secrets"
	"phleet/internal/store"
)

const (
	maxBackupBytes   = 2 << 30
	maxAddonReplicas = 1 // P1: no standby promotion yet
)

type addonCreds struct {
	User          string `json:"user"`
	Password      string `json:"password"`
	Database      string `json:"database"`
	Superuser     string `json:"superuser"`
	SuperPassword string `json:"superPassword"`
	ReplicaUser   string `json:"replicaUser,omitempty"`
	ReplicaPass   string `json:"replicaPass,omitempty"`
}

type addonPlacementView struct {
	model.AddonPlacement
	ServerName string `json:"serverName"`
	Online     bool   `json:"online"`
	Drained    bool   `json:"drained"`
}

type addonRelayView struct {
	model.AddonRelay
	ServerName string `json:"serverName"`
	Online     bool   `json:"online"`
}

type addonView struct {
	model.Addon
	EngineName   string               `json:"engineName"`
	Embedded     bool                 `json:"embedded"`
	Console      bool                 `json:"console"`
	NoBackup     bool                 `json:"noBackup"`
	Category     string               `json:"category,omitempty"`
	Website      string               `json:"website,omitempty"`
	Source       string               `json:"source,omitempty"`
	Icon         string               `json:"icon,omitempty"`
	Ingress      bool                 `json:"ingress"`
	Placements   []addonPlacementView `json:"placements"`
	Bindings     []model.AddonBinding `json:"bindings"`
	Relays       []addonRelayView     `json:"relays,omitempty"`
	Env          map[string]string    `json:"env,omitempty"`
	URL          string               `json:"url,omitempty"`
	S3Endpoint   string               `json:"s3Endpoint,omitempty"`
	Backups      []model.Backup       `json:"backups,omitempty"`
	Installed    bool                 `json:"installed"`
	BridgeActive bool                 `json:"bridgeActive"`
}

func (s *Server) buildAddonView(a model.Addon, withSecrets bool) addonView {
	v := addonView{Addon: a, Placements: []addonPlacementView{}, Bindings: []model.AddonBinding{}, Installed: true}
	if eng, ok := engines.Get(a.Engine); ok {
		v.EngineName = eng.Name
		v.Embedded = eng.Embedded
		v.Console = eng.Console
		v.NoBackup = eng.NoBackup
		v.Category = eng.Category
		v.Website = eng.Website
		v.Source = eng.Source
		v.Icon = eng.Icon
		v.Ingress = eng.Ingress
	}
	if a.Service && a.Hostname != "" {
		scheme := "http"
		if _, fleetHost := s.Edge(); fleetHost != "" {
			scheme = "https"
		}
		v.URL = scheme + "://" + a.Hostname
	}
	placements, _ := s.st.ListAddonPlacements(a.ID)
	for _, pl := range placements {
		pv := addonPlacementView{AddonPlacement: pl}
		if srv, err := s.st.GetServer(pl.ServerID); err == nil {
			pv.ServerName = srv.Name
			pv.Online = s.hub.Online(srv.ID)
			pv.Drained = srv.Drain
			if installed, known := engineInstalledOn(srv, a.Engine); known {
				v.Installed = installed
			}
		}
		v.Placements = append(v.Placements, pv)
	}
	if bindings, err := s.st.ListAddonBindings(a.ID); err == nil {
		v.Bindings = bindings
	}
	if relays, err := s.st.ListAddonRelays(a.ID); err == nil {
		for _, r := range relays {
			rv := addonRelayView{AddonRelay: r}
			if srv, err := s.st.GetServer(r.ServerID); err == nil {
				rv.ServerName = srv.Name
				rv.Online = s.hub.Online(srv.ID)
			}
			v.Relays = append(v.Relays, rv)
		}
	}
	if backups, err := s.st.ListBackups(a.ID, 10); err == nil {
		v.Backups = backups
	}
	if a.Hostname != "" {
		scheme := "http"
		if _, fleetHost := s.Edge(); fleetHost != "" {
			scheme = "https"
		}
		v.S3Endpoint = scheme + "://" + a.Hostname
	}
	if withSecrets {
		if creds, err := s.decryptAddonCreds(a); err == nil {
			v.Env = engines.EnvFor(a.Engine, addonParams(a, creds))
			if !a.Service {
				v.URL = addonURL(a.Engine, v.Env)
			}
		}
	}
	v.BridgeActive = s.bridges != nil && s.bridges.active(a.ID)
	return v
}

func engineInstalledOn(srv model.Server, engineID string) (installed, known bool) {
	raw, ok := srv.Caps["engines"]
	if !ok {
		return false, false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return false, false
	}
	val, ok := m[engineID]
	if !ok {
		return false, false
	}
	b, ok := val.(bool)
	return b, ok
}

func addonURL(engineID string, env map[string]string) string {
	switch engineID {
	case "postgres":
		return env["DATABASE_URL"]
	case "mariadb":
		return env["DATABASE_URL"]
	case "redis":
		return env["REDIS_URL"]
	case "influxdb":
		return env["INFLUX_URL"]
	case "rclone":
		return env["S3_ENDPOINT"]
	case "memcached":
		return env["MEMCACHED_SERVERS"]
	case "mosquitto":
		return env["MQTT_URL"]
	case "nats":
		return env["NATS_URL"]
	case "victoriametrics":
		return env["VICTORIAMETRICS_URL"]
	case "victorialogs":
		return env["VICTORIALOGS_URL"]
	case "ntfy":
		return env["NTFY_URL"]
	default:
		return env["DATABASE_URL"]
	}
}

func replicationMode(eng *engines.Engine) string {
	if eng == nil || eng.Replication == "" {
		return "none"
	}
	return eng.Replication
}

func leaseSecOrDefault(a model.Addon) int {
	if a.LeaseSec > 0 {
		return a.LeaseSec
	}
	return 60
}

// leaseSecondsFor returns the agent's self-fence lease. It is shorter than the
// control plane's promote threshold so the old primary stops before the
// standby is promoted.
func leaseSecondsFor(a model.Addon) int {
	if !a.AutoFailover {
		return 0
	}
	after := a.FailoverAfterSec
	if after < 0 {
		after = 0
	}
	d := leaseSecOrDefault(a) + after - 30
	if d < 30 {
		d = 30
	}
	return d
}

func addonParams(a model.Addon, c addonCreds) engines.Params {
	return engines.Params{
		DataDir: a.DataDir, File: addonFile(a), Port: a.Port,
		User: c.User, Password: c.Password, Database: c.Database,
		Superuser: c.Superuser, SuperPassword: c.SuperPassword,
		ReplicaUser: c.ReplicaUser, ReplicaPass: c.ReplicaPass,
	}
}

func addonFile(a model.Addon) string {
	if a.Kind != engines.KindFile && a.Kind != engines.KindAnalytics {
		return ""
	}
	ext := ".sqlite"
	if a.Engine == "duckdb" {
		ext = ".duckdb"
	}
	return path.Join(a.DataDir, a.Slug+ext)
}

// serviceEnv decrypts the extra environment stored for a service (for example
// the DATABASE_URL of the PostgreSQL addon it uses).
func (s *Server) serviceEnv(a model.Addon) map[string]string {
	if a.ConfigEnc == "" {
		return nil
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, a.ConfigEnc)
	if err != nil {
		return nil
	}
	var env map[string]string
	if json.Unmarshal(raw, &env) != nil {
		return nil
	}
	return env
}

func (s *Server) decryptAddonCreds(a model.Addon) (addonCreds, error) {
	var c addonCreds
	if a.EnvEnc == "" {
		return c, errors.New("addon has no credentials")
	}
	raw, err := secrets.Decrypt(s.cfg.SessionKey, a.EnvEnc)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	return c, nil
}

func (s *Server) encryptAddonCreds(c addonCreds) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return secrets.Encrypt(s.cfg.SessionKey, raw)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	}
	return hex.EncodeToString(b)
}

func dbIdentifier(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "app"
	}
	if unicode.IsDigit(rune(out[0])) {
		out = "u_" + out
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func (s *Server) serverHome(serverID string) string {
	if srv, err := s.st.GetServer(serverID); err == nil {
		if v, ok := srv.Caps["home"].(string); ok && v != "" {
			return v
		}
	}
	return "/data/data/com.termux/files/home"
}

func (s *Server) uniqueAddonSlug(name string) (string, error) {
	base := "db-" + slugify(name)
	base = strings.Trim(base, "-")
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if _, err := s.st.GetAddonBySlug(candidate); errors.Is(err, store.ErrNotFound) {
			return candidate, nil
		}
	}
	return "", errors.New("could not find a free addon slug")
}

// uniqueServiceSlug keeps the service slug clean (no "db-" prefix); it is used
// for the data directory and the supervisor app name.
func (s *Server) uniqueServiceSlug(name string) (string, error) {
	base := slugify(name)
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-")
	}
	if base == "" {
		base = "service"
	}
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if _, err := s.st.GetAddonBySlug(candidate); errors.Is(err, store.ErrNotFound) {
			return candidate, nil
		}
	}
	return "", errors.New("could not find a free service slug")
}

// allocateBridgePort picks the control-plane loopback port used by the TCP
// bridge. It is deliberately outside the app/addon range.
func (s *Server) allocateBridgePort() (int, error) {
	used, err := s.st.UsedBridgePorts()
	if err != nil {
		return 0, err
	}
	for p := 15400; p <= 16499; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, errors.New("no free bridge ports")
}

// allocateRelayPort picks a loopback facade port on an app phone.
func (s *Server) allocateRelayPort(serverID string) (int, error) {
	used, err := s.st.UsedRelayPorts(serverID)
	if err != nil {
		return 0, err
	}
	for p := 16500; p <= 17499; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, errors.New("no free relay ports on this phone")
}

// serviceHostname gives a self-hosted service its own subdomain. Services are
// HTTP apps, so they use the same wildcard tunnel as projects.
func (s *Server) serviceHostname(name string) (string, error) {
	base := slugify(name)
	if len(base) > 50 {
		base = strings.Trim(base[:50], "-")
	}
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		host := candidate + "." + s.appSuffix()
		if _, err := s.st.GetProjectByHostname(host); err == nil {
			continue
		}
		if _, err := s.st.GetAddonByHostname(host); err == nil {
			continue
		}
		return host, nil
	}
	return "", errors.New("could not find a free service hostname")
}

// addonHostname gives object storage a real hostname. S3 is HTTP, so it can be
// published through the normal wildcard tunnel.
func (s *Server) addonHostname(name string) (string, error) {
	base := "s3-" + slugify(name)
	if len(base) > 50 {
		base = strings.Trim(base[:50], "-")
	}
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		host := candidate + "." + s.appSuffix()
		if _, err := s.st.GetProjectByHostname(host); err == nil {
			continue
		}
		if _, err := s.st.GetAddonByHostname(host); err == nil {
			continue
		}
		return host, nil
	}
	return "", errors.New("could not find a free storage hostname")
}

func (s *Server) standbyDataDir(serverID, slug string) string {
	return path.Join(s.serverHome(serverID), "phleet", "data", slug+"-replica")
}

// ---- provisioning ----

func (s *Server) provisionAddon(a model.Addon) error {
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		return err
	}
	for _, pl := range placements {
		if pl.Role == "primary" || pl.Role == "" {
			return s.provisionAddonPlacement(a, pl)
		}
	}
	return errors.New("addon has no primary placement")
}

// provisionAddonPlacement sends a self-contained plan for one placement. A
// standby gets a different data directory and a loopback relay to the primary.
func (s *Server) provisionAddonPlacement(a model.Addon, pl model.AddonPlacement) error {
	eng, ok := engines.Get(a.Engine)
	if !ok {
		return fmt.Errorf("unknown engine %q", a.Engine)
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		return err
	}
	if !s.hub.Online(pl.ServerID) {
		_ = s.st.UpdateAddonPlacementStatus(a.ID, pl.ServerID, "pending", 0, 0, "waiting for the phone")
		if pl.Role == "primary" || pl.Role == "" {
			_ = s.st.SetAddonStatus(a.ID, "pending", "waiting for the phone to come online")
		}
		return errors.New("phone is offline")
	}
	standby := pl.Role == "standby"
	primaryHost, primaryPort, relayPort := "", 0, 0
	if standby {
		if eng.StandbyInit == nil {
			return fmt.Errorf("%s does not support standbys", eng.Name)
		}
		port, err := s.ensureAddonRelayRow(a, pl.ServerID)
		if err != nil {
			return fmt.Errorf("standby relay: %w", err)
		}
		primaryHost, primaryPort, relayPort = "127.0.0.1", port, port
	}
	dataDir := a.DataDir
	if standby {
		dataDir = s.standbyDataDir(pl.ServerID, a.Slug)
	}
	role := pl.Role
	if role == "" {
		role = "primary"
	}
	leaseSeconds := 0
	if !standby {
		leaseSeconds = leaseSecondsFor(a)
	}
	spec := agentproto.AddonSpec{
		AddonID: a.ID, Slug: a.Slug, Name: a.Name, Engine: a.Engine, Kind: a.Kind,
		Role: role, Epoch: a.Epoch, Port: pl.Port, DataDir: dataDir, File: addonFileAt(a, dataDir, standby),
		User: creds.User, Password: creds.Password, Database: creds.Database,
		Superuser: creds.Superuser, SuperPassword: creds.SuperPassword,
		ReplicaUser: creds.ReplicaUser, ReplicaPass: creds.ReplicaPass,
		PrimaryHost: primaryHost, PrimaryPort: primaryPort,
		RelayPort:    relayPort,
		LeaseSeconds: leaseSeconds,
		SizeLimitMB:  a.SizeLimitMB,
		Env:          s.serviceEnv(a),
	}
	if err := s.hub.Send(pl.ServerID, agentproto.TypeAddonProvision, spec); err != nil {
		_ = s.st.UpdateAddonPlacementStatus(a.ID, pl.ServerID, "pending", 0, 0, err.Error())
		return err
	}
	if !standby && a.AutoFailover {
		_ = s.st.SetAddonLease(a.ID, time.Now().Unix()+int64(leaseSecOrDefault(a)))
	}
	_ = s.st.UpdateAddonPlacementStatus(a.ID, pl.ServerID, "provisioning", 0, 0, "")
	if !standby {
		_ = s.st.SetAddonStatus(a.ID, "provisioning", "")
	}
	_ = s.st.AddEvent("ui", "addon.provision", a.ID, fmt.Sprintf("%s on %s (%s)", eng.Name, pl.ServerID, role))
	_ = s.st.AddAddonLog(a.ID, fmt.Sprintf("provisioning %s on %s (port %d, %s)", eng.Name, pl.ServerID, pl.Port, role))
	return nil
}

// addonFileAt computes the SQLite file for a specific placement.
func addonFileAt(a model.Addon, dataDir string, standby bool) string {
	if a.Kind != engines.KindFile && a.Kind != engines.KindAnalytics {
		return ""
	}
	ext := ".sqlite"
	if a.Engine == "duckdb" {
		ext = ".duckdb"
	}
	return path.Join(dataDir, a.Slug+ext)
}

// ---- HTTP ----

func (s *Server) handleListAddonEngines(w http.ResponseWriter, _ *http.Request) {
	out := make([]*engines.Engine, 0)
	for _, e := range engines.All() {
		if !e.Service {
			out = append(out, e)
		}
	}
	writeJSON(w, 200, map[string]any{"engines": out})
}

func (s *Server) handleServiceEngines(w http.ResponseWriter, _ *http.Request) {
	out := make([]*engines.Engine, 0)
	for _, e := range engines.All() {
		if e.Service {
			out = append(out, e)
		}
	}
	writeJSON(w, 200, map[string]any{"engines": out})
}

func (s *Server) handleListAddons(w http.ResponseWriter, _ *http.Request) {
	addons, err := s.st.ListAddons()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	out := make([]addonView, 0, len(addons))
	for _, a := range addons {
		if a.Service {
			continue
		}
		out = append(out, s.buildAddonView(a, false))
	}
	writeJSON(w, 200, map[string]any{"addons": out})
}

func (s *Server) handleListServices(w http.ResponseWriter, _ *http.Request) {
	addons, err := s.st.ListAddons()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	out := make([]addonView, 0, len(addons))
	for _, a := range addons {
		if a.Service {
			out = append(out, s.buildAddonView(a, false))
		}
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil || !a.Service {
		apiErr(w, 404, errors.New("service not found"))
		return
	}
	writeJSON(w, 200, map[string]any{"service": s.buildAddonView(a, true)})
}

func (s *Server) handleRenameService(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil || !a.Service {
		apiErr(w, 404, errors.New("service not found"))
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		apiErr(w, 400, errors.New("name is required"))
		return
	}
	if err := s.st.RenameAddon(a.ID, name); err != nil {
		apiErr(w, 500, err)
		return
	}
	a.Name = name
	_ = s.st.AddEvent("ui", "service.rename", a.ID, name)
	writeJSON(w, 200, map[string]any{"service": s.buildAddonView(a, true)})
}

func (s *Server) handleGetAddon(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{"addon": s.buildAddonView(a, true)})
}

type createAddonBody struct {
	Name            string            `json:"name"`
	Engine          string            `json:"engine"`
	ServerID        string            `json:"serverId"`
	ProjectID       string            `json:"projectId"`
	SizeLimitMB     int               `json:"sizeLimitMB"`
	ReplicationMode string            `json:"replicationMode"`
	Env             map[string]string `json:"env"`
}

func (s *Server) handleCreateAddon(w http.ResponseWriter, r *http.Request) {
	s.createAddon(w, r, false)
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) {
	s.createAddon(w, r, true)
}

func (s *Server) createAddon(w http.ResponseWriter, r *http.Request, service bool) {
	var body createAddonBody
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		apiErr(w, 400, errors.New("name is required"))
		return
	}
	eng, ok := engines.Get(body.Engine)
	if !ok {
		apiErr(w, 400, fmt.Errorf("unknown engine %q", body.Engine))
		return
	}
	if eng.Service != service {
		if service {
			apiErr(w, 400, errors.New("that engine is an addon, not a service"))
		} else {
			apiErr(w, 400, errors.New("that engine is a service; install it from the Services catalogue"))
		}
		return
	}
	if service && body.ProjectID != "" {
		apiErr(w, 400, errors.New("services are standalone; they are not attached to a project"))
		return
	}

	var project model.Project
	if body.ProjectID != "" {
		var err error
		project, err = s.st.GetProject(body.ProjectID)
		if err != nil {
			apiErr(w, 404, errors.New("project not found"))
			return
		}
		if project.Kind != "stack" && eng.Kind == engines.KindFile && project.Replicas > maxAddonReplicas {
			apiErr(w, 409, errors.New("a SQLite database is a file and needs one replica on its phone; reduce replicas or use PostgreSQL"))
			return
		}
	}

	serverID := strings.TrimSpace(body.ServerID)
	if serverID == "" && project.ID != "" {
		serverID = project.PinnedServer
		if serverID == "" {
			if placements, err := s.st.ListProjectPlacements(project.ID); err == nil && len(placements) > 0 {
				serverID = placements[0].ServerID
			}
		}
	}
	if serverID == "" {
		apiErr(w, 400, errors.New("choose the phone that will host this workload"))
		return
	}
	srv, err := s.st.GetServer(serverID)
	if err != nil {
		apiErr(w, 400, errors.New("selected phone was not found"))
		return
	}
	if !serverCanInstallEngine(srv, eng.ID) {
		if !serverHasUserland(srv) {
			apiErr(w, 409, noUserlandError(srv, fmt.Sprintf("%s cannot be installed there", eng.Name)))
			return
		}
		pkg := eng.AptPackage
		if pkg == "" {
			pkg = eng.Package
		}
		apiErr(w, 409, fmt.Errorf("%s cannot install %s automatically (no passwordless package manager); install it once as root, for example: apt-get install -y %s",
			serverLabel(srv), eng.Name, pkg))
		return
	}
	if project.ID != "" && project.PinnedServer != "" && project.PinnedServer != serverID {
		apiErr(w, 400, errors.New("pin the database to the same phone as the project"))
		return
	}
	if project.ID != "" {
		placements, _ := s.st.ListProjectPlacements(project.ID)
		for _, pl := range placements {
			if pl.ServerID != serverID && eng.Kind == engines.KindFile {
				apiErr(w, 409, errors.New("this project runs on another phone; a SQLite file cannot be reached across phones"))
				return
			}
		}
	}

	var slug string
	if service {
		slug, err = s.uniqueServiceSlug(name)
	} else {
		slug, err = s.uniqueAddonSlug(name)
	}
	if err != nil {
		apiErr(w, 400, err)
		return
	}
	port := 0
	if !eng.Embedded {
		port, err = s.allocatePort(serverID)
		if err != nil {
			apiErr(w, 409, err)
			return
		}
	}
	creds := addonCreds{
		User:          dbIdentifier(name),
		Password:      randomHex(24),
		Database:      dbIdentifier(name),
		Superuser:     "phleet_admin",
		SuperPassword: randomHex(24),
	}
	if eng.Replication == "streaming" || eng.Replication == "async" {
		creds.ReplicaUser = "replicator"
		creds.ReplicaPass = randomHex(24)
	}
	envEnc, err := s.encryptAddonCreds(creds)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	configEnc := ""
	if service && len(body.Env) > 0 {
		raw, err := json.Marshal(body.Env)
		if err != nil {
			apiErr(w, 500, err)
			return
		}
		configEnc, err = secrets.Encrypt(s.cfg.SessionKey, raw)
		if err != nil {
			apiErr(w, 500, err)
			return
		}
	}
	bridgePort := 0
	if !eng.Embedded {
		bridgePort, err = s.allocateBridgePort()
		if err != nil {
			apiErr(w, 409, err)
			return
		}
	}
	hostname := ""
	if service {
		hostname, err = s.serviceHostname(name)
		if err != nil {
			apiErr(w, 500, err)
			return
		}
	} else if eng.Kind == engines.KindObjectStore {
		hostname, err = s.addonHostname(name)
		if err != nil {
			apiErr(w, 500, err)
			return
		}
	}
	dataDir := path.Join(s.serverHome(serverID), "phleet", "data", slug)
	if service {
		dataDir = path.Join(s.serverHome(serverID), "phleet", "services", slug)
	}
	now := time.Now().Unix()
	a := model.Addon{
		ID: uuid.NewString(), Name: name, Slug: slug, Engine: eng.ID, Kind: eng.Kind,
		OwnerProjectID: project.ID, PinnedServer: serverID, Port: port,
		BridgePort: bridgePort, Hostname: hostname, DataDir: dataDir,
		SizeLimitMB: body.SizeLimitMB, ReplicationMode: replicationMode(eng),
		FailoverAfterSec: 120, MaxLagMS: 30000, LeaseSec: 60,
		Status: "pending", EnvEnc: envEnc, ConfigEnc: configEnc, Service: service, AppID: eng.ID, Version: eng.Version,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.st.CreateAddon(a); err != nil {
		apiErr(w, 500, err)
		return
	}
	if err := s.st.UpsertAddonPlacement(model.AddonPlacement{
		AddonID: a.ID, ServerID: serverID, Role: "primary", Status: "pending",
		Desired: "running", Port: port, UpdatedAt: now,
	}); err != nil {
		apiErr(w, 500, err)
		return
	}
	if project.ID != "" {
		if err := s.st.BindAddon(a.ID, project.ID); err != nil {
			apiErr(w, 500, err)
			return
		}
	}
	kind := "addon"
	if service {
		kind = "service"
	}
	_ = s.st.AddEvent("ui", kind+".create", a.ID, fmt.Sprintf("%s (%s) on %s", name, eng.Name, serverID))
	if s.bridges != nil && bridgePort > 0 {
		s.bridgeSyncSoon(a)
	}
	// Provision in the background: pkg install and initdb can take minutes
	// and must not hold the HTTP request open through the edge.
	go func() {
		if err := s.provisionAddon(a); err != nil {
			s.log.Warn("provision "+kind, "id", a.Slug, "err", err)
		}
	}()
	a.Status = "provisioning"
	writeJSON(w, 201, map[string]any{kind: s.buildAddonView(a, true)})
}

func (s *Server) handleAddonAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := s.st.GetAddon(r.PathValue("id"))
		if err != nil {
			apiErr(w, 404, err)
			return
		}
		placements, err := s.st.ListAddonPlacements(a.ID)
		if err != nil || len(placements) == 0 {
			apiErr(w, 409, errors.New("this addon has no placement"))
			return
		}
		okCount := 0
		var lastErr error
		for _, pl := range placements {
			if !s.hub.Online(pl.ServerID) {
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
			_, err := s.hub.Call(ctx, pl.ServerID, agentproto.TypeAddonControl,
				agentproto.AddonControlRequest{AddonID: a.ID, Slug: a.Slug, Action: action})
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			okCount++
		}
		if okCount == 0 {
			if lastErr == nil {
				lastErr = errors.New("no online placement accepted the action")
			}
			apiErr(w, 409, lastErr)
			return
		}
		switch action {
		case "stop":
			_ = s.st.SetAddonDesired(a.ID, "stopped")
			_ = s.st.SetAddonStatus(a.ID, "stopped", "")
			_ = s.st.UpdateAddonPlacementStatus(a.ID, a.PinnedServer, "stopped", 0, 0, "")
			if s.bridges != nil {
				s.bridges.stop(a.ID)
			}
		case "start", "restart":
			_ = s.st.SetAddonDesired(a.ID, "running")
			_ = s.st.SetAddonStatus(a.ID, "running", "")
			_ = s.st.UpdateAddonPlacementStatus(a.ID, a.PinnedServer, "running", 0, 0, "")
			if s.bridges != nil {
				_ = s.bridges.start(a)
			}
		}
		_ = s.st.AddEvent("ui", "addon."+action, a.ID, a.Name)
		a, _ = s.st.GetAddon(a.ID)
		writeJSON(w, 200, map[string]any{"ok": true, "addon": s.buildAddonView(a, true)})
	}
}

func (s *Server) handleDeleteAddon(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.st.GetAddon(id)
	if err != nil {
		apiErr(w, 404, fmt.Errorf("workload %q: %w", id, err))
		return
	}
	bindings, _ := s.st.ListAddonBindings(a.ID)
	if len(bindings) > 0 && !truthy(r.URL.Query().Get("force")) {
		apiErr(w, 409, fmt.Errorf("this database is attached to %d project(s); detach it first or pass force=1", len(bindings)))
		return
	}
	purge := truthy(r.URL.Query().Get("purge"))
	if s.bridges != nil {
		s.bridges.stop(a.ID)
	}
	s.stopAddonRelays(a)
	// A standby lives on another phone; stop it too, not just the primary.
	placements, _ := s.st.ListAddonPlacements(a.ID)
	for _, pl := range placements {
		if !s.hub.Online(pl.ServerID) {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		_, _ = s.hub.Call(ctx, pl.ServerID, agentproto.TypeAddonControl,
			agentproto.AddonControlRequest{AddonID: a.ID, Slug: a.Slug, Action: "delete", PurgeData: purge})
		cancel()
	}
	// Remove stored backups before the rows disappear.
	if backups, err := s.st.ListBackups(a.ID, 200); err == nil {
		for _, b := range backups {
			if b.Path != "" {
				_ = os.Remove(b.Path)
			}
		}
	}
	if err := s.st.DeleteAddon(a.ID); err != nil {
		apiErr(w, 500, err)
		return
	}
	_, _ = s.st.ResolveAlert("addon:" + a.ID)
	_ = s.st.AddEvent("ui", "addon.delete", a.ID, a.Name)
	writeJSON(w, 200, map[string]any{"ok": true, "purged": purge})
}

// ---- standby + promotion ----

func (s *Server) handleAddStandby(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	eng, ok := engines.Get(a.Engine)
	if !ok || eng.StandbyInit == nil {
		apiErr(w, 409, errors.New("this engine does not support a standby"))
		return
	}
	var body struct {
		ServerID string `json:"serverId"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	serverID := strings.TrimSpace(body.ServerID)
	if serverID == "" {
		apiErr(w, 400, errors.New("choose the phone for the standby"))
		return
	}
	if serverID == a.PinnedServer {
		apiErr(w, 400, errors.New("the standby must be on a different phone"))
		return
	}
	srv, err := s.st.GetServer(serverID)
	if err != nil {
		apiErr(w, 400, errors.New("selected phone was not found"))
		return
	}
	if !serverCanInstallEngine(srv, a.Engine) {
		if !serverHasUserland(srv) {
			apiErr(w, 409, noUserlandError(srv, "the standby engine cannot be installed there"))
			return
		}
		apiErr(w, 409, fmt.Errorf("%s cannot install the %s engine automatically; install it once as root and retry",
			serverLabel(srv), a.Engine))
		return
	}
	placements, _ := s.st.ListAddonPlacements(a.ID)
	for _, pl := range placements {
		if pl.ServerID == serverID {
			apiErr(w, 409, errors.New("this addon already has a placement on that phone"))
			return
		}
	}

	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	needPrimary := creds.ReplicaUser == ""
	if needPrimary {
		creds.ReplicaUser = "replicator"
		creds.ReplicaPass = randomHex(24)
		enc, err := s.encryptAddonCreds(creds)
		if err != nil {
			apiErr(w, 500, err)
			return
		}
		a.EnvEnc = enc
	}
	port, err := s.allocatePort(serverID)
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	a.ReplicationMode = replicationMode(eng)
	if err := s.st.UpdateAddon(a); err != nil {
		apiErr(w, 500, err)
		return
	}
	pl := model.AddonPlacement{
		AddonID: a.ID, ServerID: serverID, Role: "standby", Status: "pending",
		Desired: "running", Port: port, LagMS: -1, UpdatedAt: time.Now().Unix(),
	}
	if err := s.st.UpsertAddonPlacement(pl); err != nil {
		apiErr(w, 500, err)
		return
	}
	// The standby needs a loopback facade to the primary for pg_basebackup and
	// ongoing streaming replication.
	// The standby starts its own facade from the provisioning plan; the row is
	// only needed for authorization and port allocation.
	if _, err := s.ensureAddonRelayRow(a, serverID); err != nil {
		s.log.Warn("standby relay", "addon", a.Slug, "err", err)
	}
	// Re-provision the primary only when an older addon needs the replication
	// role created; new addons already have it and must not restart.
	go func() {
		if needPrimary {
			if err := s.provisionAddon(a); err != nil {
				s.log.Warn("re-provision primary for standby", "addon", a.Slug, "err", err)
			}
		}
		if err := s.provisionAddonPlacement(a, pl); err != nil {
			s.log.Warn("provision standby", "addon", a.Slug, "err", err)
		}
	}()
	_ = s.st.AddEvent("ui", "addon.standby", a.ID, serverID)
	writeJSON(w, 202, map[string]any{"addon": s.buildAddonView(a, true)})
}

// findAddonPlacements returns the current primary and a standby (optionally for
// a specific phone).
func findAddonPlacements(placements []model.AddonPlacement, serverID string) (*model.AddonPlacement, *model.AddonPlacement) {
	var primary, standby *model.AddonPlacement
	for i := range placements {
		switch placements[i].Role {
		case "standby":
			if serverID == "" || placements[i].ServerID == serverID {
				standby = &placements[i]
			}
		case "primary", "":
			primary = &placements[i]
		}
	}
	return primary, standby
}

// promoteAddon performs fence → promote → repoint. It is shared by the manual
// button and the automatic lease evaluator.
func (s *Server) promoteAddon(a model.Addon, standby model.AddonPlacement, reason string) (model.Addon, error) {
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		return a, err
	}
	primary, found := findAddonPlacements(placements, standby.ServerID)
	if found == nil || found.ServerID != standby.ServerID {
		return a, errors.New("standby placement disappeared")
	}
	standby = *found
	if standby.Status != "running" {
		return a, fmt.Errorf("the standby is %s; wait for it to catch up", standby.Status)
	}
	if primary != nil && primary.ServerID == standby.ServerID {
		return a, errors.New("this placement is already the primary")
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		return a, err
	}

	fenced := "offline"
	if primary != nil && s.hub.Online(primary.ServerID) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		_, err := s.hub.Call(ctx, primary.ServerID, agentproto.TypeAddonControl,
			agentproto.AddonControlRequest{AddonID: a.ID, Slug: a.Slug, Action: "stop"})
		cancel()
		if err != nil {
			return a, fmt.Errorf("could not fence the old primary: %w", err)
		}
		fenced = "stopped"
	}

	newEpoch := a.Epoch + 1
	req := agentproto.AddonPromoteRequest{
		AddonID: a.ID, Slug: a.Slug, Engine: a.Engine,
		DataDir: s.standbyDataDir(standby.ServerID, a.Slug), Port: standby.Port,
		User: creds.User, Password: creds.Password, Database: creds.Database,
		Superuser: creds.Superuser, SuperPassword: creds.SuperPassword,
		Epoch: newEpoch, LeaseSeconds: leaseSecondsFor(a),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	_, err = s.hub.Call(ctx, standby.ServerID, agentproto.TypeAddonPromote, req)
	cancel()
	if err != nil {
		return a, fmt.Errorf("promotion failed (old primary is %s): %w", fenced, err)
	}

	a.PinnedServer = standby.ServerID
	a.Port = standby.Port
	a.DataDir = s.standbyDataDir(standby.ServerID, a.Slug)
	a.Epoch = newEpoch
	a.Status = "running"
	a.LastError = ""
	if a.AutoFailover {
		a.LeaseUntil = time.Now().Unix() + int64(leaseSecOrDefault(a))
	}
	if err := s.st.UpdateAddon(a); err != nil {
		return a, err
	}
	if primary != nil {
		_ = s.st.SetAddonPlacementRole(a.ID, primary.ServerID, "demoted", "stopped")
		_ = s.st.UpdateAddonPlacementStatus(a.ID, primary.ServerID, "stopped", 0, 0, "demoted")
	}
	_ = s.st.SetAddonPlacementRole(a.ID, standby.ServerID, "primary", "running")
	_ = s.st.UpdateAddonPlacementStatus(a.ID, standby.ServerID, "running", standby.PID, standby.Restarts, "")
	// The new primary no longer needs a facade to itself.
	if s.hub.Online(standby.ServerID) {
		_ = s.hub.Send(standby.ServerID, agentproto.TypeAddonRelay,
			agentproto.AddonRelaySpec{AddonID: a.ID, Action: "stop"})
	}
	_ = s.st.DeleteAddonRelay(a.ID, standby.ServerID)
	if s.bridges != nil {
		_ = s.bridges.start(a)
	}
	_ = s.st.AddEvent(reason, "addon.promote", a.ID, standby.ServerID)
	_ = s.st.AddAddonLog(a.ID, fmt.Sprintf("promoted %s to primary (%s)", standby.ServerID, reason))
	return a, nil
}

func (s *Server) handlePromoteAddon(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		ServerID string `json:"serverId"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	_, standby := findAddonPlacements(placements, body.ServerID)
	if standby == nil {
		apiErr(w, 404, errors.New("no standby placement for this phone"))
		return
	}
	a, err = s.promoteAddon(a, *standby, "ui")
	if err != nil {
		apiErr(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"addon": s.buildAddonView(a, true)})
}

// autoFailover promotes a healthy standby after the primary's lease expires.
// It runs only for addons that opted in, only when the standby is running and
// its last measured lag is within the configured bound.
func (s *Server) autoFailover(a model.Addon) {
	if !a.AutoFailover || a.LeaseUntil == 0 {
		return
	}
	if time.Now().Unix() <= a.LeaseUntil+int64(a.FailoverAfterSec) {
		return
	}
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		return
	}
	primary, _ := findAddonPlacements(placements, "")
	if primary != nil && (primary.Desired == "stopped" || primary.Status == "provisioning" || primary.Status == "pending") {
		return
	}
	var standby *model.AddonPlacement
	for i := range placements {
		pl := &placements[i]
		if pl.Role != "standby" || pl.Status != "running" {
			continue
		}
		if a.MaxLagMS > 0 && (pl.LagMS < 0 || pl.LagMS > a.MaxLagMS) {
			continue
		}
		standby = pl
		break
	}
	if standby == nil {
		return
	}
	key := "addon-failover:" + a.ID
	s.recMu.Lock()
	if last, seen := s.recAt[key]; seen && time.Since(last) < 10*time.Minute {
		s.recMu.Unlock()
		return
	}
	s.recAt[key] = time.Now()
	s.recMu.Unlock()
	updated, err := s.promoteAddon(a, *standby, "auto")
	if err != nil {
		s.log.Warn("auto failover", "addon", a.Slug, "err", err)
		return
	}
	s.log.Info("auto failover promoted standby", "addon", updated.Slug, "server", standby.ServerID)
	_ = s.st.AddEvent("scheduler", "addon.auto_failover", a.ID, standby.ServerID)
}

// handleUpdateAddon configures replication policy without restarting the engine.
func (s *Server) handleUpdateAddon(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		AutoFailover     *bool  `json:"autoFailover"`
		FailoverAfterSec *int   `json:"failoverAfterSec"`
		MaxLagMS         *int64 `json:"maxLagMs"`
		LeaseSec         *int   `json:"leaseSec"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	eng, _ := engines.Get(a.Engine)
	if body.AutoFailover != nil && *body.AutoFailover && (eng == nil || eng.StandbyInit == nil) {
		apiErr(w, 409, errors.New("this engine has no standby to promote"))
		return
	}
	if body.AutoFailover != nil {
		a.AutoFailover = *body.AutoFailover
	}
	if body.FailoverAfterSec != nil {
		v := *body.FailoverAfterSec
		if v < 0 {
			v = 0
		}
		if v > 3600 {
			v = 3600
		}
		a.FailoverAfterSec = v
	}
	if body.MaxLagMS != nil {
		v := *body.MaxLagMS
		if v < 0 {
			v = 0
		}
		if v > 600000 {
			v = 600000
		}
		a.MaxLagMS = v
	}
	if body.LeaseSec != nil {
		v := *body.LeaseSec
		if v < 30 {
			v = 30
		}
		if v > 600 {
			v = 600
		}
		a.LeaseSec = v
	}
	now := time.Now().Unix()
	if a.AutoFailover {
		a.LeaseUntil = now + int64(leaseSecOrDefault(a))
	} else {
		a.LeaseUntil = 0
	}
	if err := s.st.UpdateAddon(a); err != nil {
		apiErr(w, 500, err)
		return
	}
	if s.hub.Online(a.PinnedServer) {
		_ = s.hub.Send(a.PinnedServer, agentproto.TypeAddonLease, agentproto.AddonLeaseRequest{
			AddonID: a.ID, Slug: a.Slug, LeaseSeconds: leaseSecondsFor(a),
		})
	}
	_ = s.st.AddEvent("ui", "addon.policy", a.ID, fmt.Sprintf("auto=%t after=%ds lag=%dms", a.AutoFailover, a.FailoverAfterSec, a.MaxLagMS))
	writeJSON(w, 200, map[string]any{"addon": s.buildAddonView(a, true)})
}

func (s *Server) syncAddonLeases(serverID string) {
	if !s.hub.Online(serverID) {
		return
	}
	addons, err := s.st.ListAddons()
	if err != nil {
		return
	}
	for _, a := range addons {
		if a.PinnedServer != serverID {
			continue
		}
		_ = s.hub.Send(serverID, agentproto.TypeAddonLease, agentproto.AddonLeaseRequest{
			AddonID: a.ID, Slug: a.Slug, LeaseSeconds: leaseSecondsFor(a),
		})
	}
}

// enforceAddonStopped stops a demoted primary that comes back online.
func (s *Server) enforceAddonStopped(serverID string, a model.Addon) {
	key := "addon-stop:" + serverID + "/" + a.Slug
	s.orphanMu.Lock()
	if last, seen := s.orphans[key]; seen && time.Since(last) < time.Minute {
		s.orphanMu.Unlock()
		return
	}
	s.orphans[key] = time.Now()
	s.orphanMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s.hub.Call(ctx, serverID, agentproto.TypeAddonControl,
			agentproto.AddonControlRequest{AddonID: a.ID, Slug: a.Slug, Action: "stop"})
	}()
}

// backupLoop keeps an off-device copy of every addon. It is deliberately
// conservative: only while charging or with a healthy battery, never more than
// once a day, and never while another backup is running.
func (s *Server) backupLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.autoBackups()
		}
	}
}

func (s *Server) autoBackups() {
	addons, err := s.st.ListAddons()
	if err != nil {
		return
	}
	for _, a := range addons {
		if a.Status != "running" || !s.hub.Online(a.PinnedServer) {
			continue
		}
		backups, err := s.st.ListBackups(a.ID, 1)
		if err != nil {
			continue
		}
		if len(backups) > 0 {
			if backups[0].Status == "running" {
				if time.Since(time.Unix(backups[0].CreatedAt, 0)) > 2*time.Hour {
					_ = s.st.FinishBackup(backups[0].ID, "failed", 0, "", "upload did not finish")
				}
				continue
			}
			if time.Since(time.Unix(backups[0].CreatedAt, 0)) < 24*time.Hour {
				continue
			}
		}
		if m, err := s.st.LatestMetrics(a.PinnedServer); err == nil {
			if !m.Charging && m.BatteryPct >= 0 && m.BatteryPct < 40 {
				continue
			}
		}
		if _, err := s.startAddonBackup(a); err != nil {
			s.log.Warn("scheduled backup", "addon", a.Slug, "err", err)
			continue
		}
		s.log.Info("scheduled backup started", "addon", a.Slug)
	}
}

// handleDeleteAddonPlacement removes a standby or demoted copy. The primary
// cannot be removed: promote a standby first.
func (s *Server) handleDeleteAddonPlacement(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	serverID := r.PathValue("serverId")
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	var target *model.AddonPlacement
	for i := range placements {
		if placements[i].ServerID == serverID {
			target = &placements[i]
			break
		}
	}
	if target == nil {
		apiErr(w, 404, errors.New("no placement on that phone"))
		return
	}
	if target.Role == "primary" || target.Role == "" {
		apiErr(w, 409, errors.New("cannot remove the primary; promote a standby first"))
		return
	}
	if !s.hub.Online(serverID) {
		apiErr(w, 409, errors.New("the phone is offline; reconnect it to remove this copy"))
		return
	}
	keep := truthy(r.URL.Query().Get("keep"))
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	_, _ = s.hub.Call(ctx, serverID, agentproto.TypeAddonControl,
		agentproto.AddonControlRequest{AddonID: a.ID, Slug: a.Slug, Action: "delete", PurgeData: !keep})
	cancel()
	_ = s.hub.Send(serverID, agentproto.TypeAddonRelay,
		agentproto.AddonRelaySpec{AddonID: a.ID, Action: "stop"})
	_ = s.st.DeleteAddonRelay(a.ID, serverID)
	if err := s.st.DeleteAddonPlacement(a.ID, serverID); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "addon.placement_remove", a.ID, serverID)
	writeJSON(w, 200, map[string]any{"addon": s.buildAddonView(a, true)})
}

// ---- bindings ----

func (s *Server) handleBindAddon(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		ProjectID string `json:"projectId"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	p, err := s.st.GetProject(body.ProjectID)
	if err != nil {
		apiErr(w, 404, errors.New("project not found"))
		return
	}
	eng, _ := engines.Get(a.Engine)
	if eng != nil && eng.Kind == engines.KindFile {
		if p.Kind != "stack" && p.Replicas > maxAddonReplicas {
			apiErr(w, 409, errors.New("a SQLite database needs one replica on its phone; reduce replicas first"))
			return
		}
		placements, _ := s.st.ListProjectPlacements(p.ID)
		if p.PinnedServer != "" && p.PinnedServer != a.PinnedServer {
			apiErr(w, 409, fmt.Errorf("the SQLite file lives on %s; pin the project there first", a.PinnedServer))
			return
		}
		if len(placements) > 0 && placements[0].ServerID != a.PinnedServer {
			apiErr(w, 409, fmt.Errorf("the SQLite file lives on %s; pin the project there first", a.PinnedServer))
			return
		}
	}
	if err := s.st.BindAddon(a.ID, p.ID); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "addon.bind", a.ID, p.Name)
	writeJSON(w, 200, map[string]any{"addon": s.buildAddonView(a, true)})
}

func (s *Server) handleUnbindAddon(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	if err := s.st.UnbindAddon(a.ID, r.PathValue("projectId")); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.AddEvent("ui", "addon.unbind", a.ID, r.PathValue("projectId"))
	writeJSON(w, 200, map[string]any{"addon": s.buildAddonView(a, true)})
}

// ---- backups ----

func (s *Server) backupPath(a model.Addon, backupID string) string {
	ext := engines.BackupExt(a.Engine)
	return filepath.Join(s.cfg.DataDir, "backups", a.ID, backupID+ext)
}

func (s *Server) startAddonBackup(a model.Addon) (model.Backup, error) {
	if !s.hub.Online(a.PinnedServer) {
		return model.Backup{}, errors.New("the phone hosting this database is offline")
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		return model.Backup{}, err
	}
	now := time.Now().Unix()
	b := model.Backup{
		ID: uuid.NewString(), AddonID: a.ID, ServerID: a.PinnedServer,
		Kind: "manual", Status: "running", CreatedAt: now,
	}
	if err := s.st.CreateBackup(b); err != nil {
		return b, err
	}
	req := agentproto.AddonBackupRequest{
		AddonID: a.ID, Slug: a.Slug, BackupID: b.ID, Engine: a.Engine,
		UploadPath: "/api/addons/" + a.ID + "/backups/" + b.ID,
		DataDir:    a.DataDir, File: addonFile(a), Port: a.Port,
		User: creds.User, Password: creds.Password, Database: creds.Database,
	}
	if err := s.hub.Send(a.PinnedServer, agentproto.TypeAddonBackup, req); err != nil {
		_ = s.st.FinishBackup(b.ID, "failed", 0, "", err.Error())
		return b, err
	}
	_ = s.st.AddEvent("ui", "addon.backup", a.ID, a.Name)
	return b, nil
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	b, err := s.startAddonBackup(a)
	if err != nil {
		if b.ID == "" {
			apiErr(w, 409, err)
		} else {
			apiErr(w, 502, err)
		}
		return
	}
	writeJSON(w, 202, map[string]any{"backup": b})
}

// handleAddonBackupUpload accepts either one PUT (offset=0&last=true) or a
// sequence of chunks. Chunking keeps a dump under Cloudflare's per-request cap.
func (s *Server) handleAddonBackupUpload(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	b, err := s.st.GetBackup(r.PathValue("backupId"))
	if err != nil || b.AddonID != a.ID {
		apiErr(w, 404, errors.New("backup not found"))
		return
	}
	offset := int64(0)
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			apiErr(w, 400, errors.New("invalid offset"))
			return
		}
		offset = n
	}
	last := true
	if v := r.URL.Query().Get("last"); v != "" {
		last = truthy(v)
	}
	dest := s.backupPath(a, b.ID)
	part := dest + ".part"
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		apiErr(w, 500, err)
		return
	}
	if offset == 0 {
		_ = os.Remove(part)
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		apiErr(w, 500, err)
		return
	}
	if offset != info.Size() {
		f.Close()
		apiErr(w, 409, fmt.Errorf("offset %d does not match the current upload (%d bytes)", offset, info.Size()))
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		apiErr(w, 500, err)
		return
	}
	const maxChunk = 48 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxChunk+1)
	n, copyErr := io.Copy(f, io.LimitReader(r.Body, maxChunk+1))
	closeErr := f.Close()
	if copyErr != nil || n > maxChunk || closeErr != nil {
		_ = os.Remove(part)
		msg := "upload failed"
		if copyErr != nil {
			msg = copyErr.Error()
		}
		_ = s.st.FinishBackup(b.ID, "failed", 0, "", msg)
		apiErr(w, 413, errors.New(msg))
		return
	}
	total := offset + n
	if !last {
		writeJSON(w, 200, map[string]any{"ok": true, "received": total})
		return
	}
	info, err = os.Stat(part)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(part)
		_ = s.st.FinishBackup(b.ID, "failed", 0, "", "empty backup")
		apiErr(w, 400, errors.New("empty backup"))
		return
	}
	if info.Size() > maxBackupBytes {
		_ = os.Remove(part)
		_ = s.st.FinishBackup(b.ID, "failed", 0, "", "backup too large")
		apiErr(w, 413, errors.New("backup too large"))
		return
	}
	_ = os.Remove(dest)
	if err := os.Rename(part, dest); err != nil {
		apiErr(w, 500, err)
		return
	}
	_ = s.st.FinishBackup(b.ID, "complete", info.Size(), dest, "")
	if removed, err := s.st.PruneBackups(a.ID, 10); err == nil {
		for _, old := range removed {
			if old.Path != "" && old.Path != dest {
				_ = os.Remove(old.Path)
			}
		}
	}
	_ = s.st.AddEvent("agent", "addon.backup_complete", a.ID, fmt.Sprintf("%d bytes", info.Size()))
	writeJSON(w, 200, map[string]any{"ok": true, "bytes": info.Size()})
}

func (s *Server) handleAddonBackupDownload(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	b, err := s.st.GetBackup(r.PathValue("backupId"))
	if err != nil || b.AddonID != a.ID || b.Path == "" {
		apiErr(w, 404, errors.New("backup not found"))
		return
	}
	f, err := os.Open(b.Path)
	if err != nil {
		apiErr(w, 404, errors.New("backup file is missing"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+b.ID+engines.BackupExt(a.Engine))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, b.ID, st.ModTime(), f)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	list, err := s.st.ListBackups(a.ID, 100)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backups": list})
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		BackupID string `json:"backupId"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		apiErr(w, 400, err)
		return
	}
	b, err := s.st.GetBackup(body.BackupID)
	if err != nil || b.AddonID != a.ID {
		apiErr(w, 404, errors.New("backup not found"))
		return
	}
	if b.Status != "complete" || b.Path == "" {
		apiErr(w, 409, errors.New("this backup is not complete"))
		return
	}
	if !s.hub.Online(a.PinnedServer) {
		apiErr(w, 409, errors.New("the phone hosting this database is offline"))
		return
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	req := agentproto.AddonRestoreRequest{
		AddonID: a.ID, Slug: a.Slug, BackupID: b.ID, Engine: a.Engine,
		DownloadPath: "/api/addons/" + a.ID + "/backups/" + b.ID + "/download",
		DataDir:      a.DataDir, File: addonFile(a), Port: a.Port,
		User: creds.User, Password: creds.Password, Database: creds.Database,
	}
	if err := s.hub.Send(a.PinnedServer, agentproto.TypeAddonRestore, req); err != nil {
		apiErr(w, 502, err)
		return
	}
	_ = s.st.SetAddonStatus(a.ID, "restoring", "")
	_ = s.st.AddEvent("ui", "addon.restore", a.ID, "backup "+b.ID[:8])
	writeJSON(w, 202, map[string]any{"ok": true})
}

// ---- logs, connection, console ----

func (s *Server) handleAddonLogs(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	limit := 300
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	logs, err := s.st.ListAddonLogs(a.ID, limit)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"logs": logs})
}

func (s *Server) handleAddonConnection(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	params := addonParams(a, creds)
	env := engines.EnvFor(a.Engine, params)
	writeJSON(w, 200, map[string]any{
		"env": env, "url": addonURL(a.Engine, env),
		"user": creds.User, "password": creds.Password, "database": creds.Database,
		"host": "127.0.0.1", "port": a.Port,
		"note": "reachable from apps on the same phone over loopback; use the console for remote access",
	})
}

// handleAddonQuery runs a one-shot command through the engine's CLI on the
// phone. Input is base64-piped so neither SQL nor Redis commands are ever
// interpreted by the shell.
func (s *Server) handleAddonQuery(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.GetAddon(r.PathValue("id"))
	if err != nil {
		apiErr(w, 404, err)
		return
	}
	var body struct {
		Input      string `json:"input"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := readJSON(r, &body, 1<<20); err != nil {
		apiErr(w, 400, err)
		return
	}
	creds, err := s.decryptAddonCreds(a)
	if err != nil {
		apiErr(w, 500, err)
		return
	}
	client := engines.ClientCommand(a.Engine, addonParams(a, creds))
	if client == "" {
		apiErr(w, 400, errors.New("this engine has no console client"))
		return
	}
	if body.TimeoutSec <= 0 || body.TimeoutSec > 300 {
		body.TimeoutSec = 60
	}
	input := base64.StdEncoding.EncodeToString([]byte(body.Input))
	cmd := fmt.Sprintf("printf %%s %s | base64 -d | %s", shellQuote(input), client)
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(body.TimeoutSec+10)*time.Second)
	defer cancel()
	env, err := s.hub.Call(ctx, a.PinnedServer, agentproto.TypeExec, agentproto.ExecRequest{
		Command: cmd, TimeoutSec: body.TimeoutSec,
	})
	if err != nil {
		apiErr(w, 502, err)
		return
	}
	var res agentproto.ExecResponse
	if err := json.Unmarshal(env.Data, &res); err != nil {
		apiErr(w, 502, err)
		return
	}
	writeJSON(w, 200, res)
}

// ---- heartbeat + reconcile helpers ----

func (s *Server) syncAddonStatus(serverID string, app agentproto.AppStatus) {
	a, err := s.st.GetAddonBySlug(app.Slug)
	if err != nil {
		return
	}
	placements, _ := s.st.ListAddonPlacements(a.ID)
	var mine *model.AddonPlacement
	for i := range placements {
		if placements[i].ServerID == serverID {
			mine = &placements[i]
			break
		}
	}
	if mine == nil {
		// The app is running somewhere it is no longer placed: stop it so a
		// forgotten database process cannot linger forever.
		key := "addon:" + serverID + "/" + a.Slug
		s.orphanMu.Lock()
		last, seen := s.orphans[key]
		if seen && time.Since(last) < 10*time.Minute {
			s.orphanMu.Unlock()
			return
		}
		s.orphans[key] = time.Now()
		s.orphanMu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = s.hub.Call(ctx, serverID, agentproto.TypeAddonControl,
				agentproto.AddonControlRequest{AddonID: a.ID, Slug: a.Slug, Action: "delete"})
		}()
		return
	}
	// A phone that was fenced by a promotion can never keep serving writes.
	// Its agent reports the old epoch; stop it and mark the placement demoted.
	if app.Epoch > 0 && a.Epoch > 0 && app.Epoch < a.Epoch {
		if app.State == "running" || app.State == "starting" || app.State == "deploying" {
			s.enforceAddonStopped(serverID, a)
			_ = s.st.SetAddonPlacementRole(a.ID, serverID, "demoted", "stopped")
			_ = s.st.UpdateAddonPlacementStatus(a.ID, serverID, "stopped", app.PID, app.Restarts, "stale epoch")
			_ = s.st.AddAddonLog(a.ID, fmt.Sprintf("fenced stale primary on %s (epoch %d < %d)", serverID, app.Epoch, a.Epoch))
			_ = s.st.AddEvent("scheduler", "addon.fenced", a.ID, serverID)
		}
		return
	}
	if mine.Status != app.State || mine.PID != app.PID || mine.Restarts != app.Restarts {
		_ = s.st.UpdateAddonPlacementStatus(a.ID, serverID, app.State, app.PID, app.Restarts, app.LastError)
	}
	if mine.Role == "primary" || mine.Role == "" {
		if app.LagMS >= 0 {
			_ = s.st.SetAddonPlacementLag(a.ID, serverID, app.LagMS)
			// Mirror the primary's measurement onto the standby rows for the UI.
			for _, pl := range placements {
				if pl.Role == "standby" {
					_ = s.st.SetAddonPlacementLag(a.ID, pl.ServerID, app.LagMS)
				}
			}
		} else if mine.LagMS != -1 {
			_ = s.st.SetAddonPlacementLag(a.ID, serverID, -1)
		}
	}
	if mine.Desired == "stopped" && app.State == "running" {
		s.enforceAddonStopped(serverID, a)
	}
	if (mine.Role == "primary" || mine.Role == "") && app.State == "running" && a.AutoFailover {
		_ = s.st.SetAddonLease(a.ID, time.Now().Unix()+int64(leaseSecOrDefault(a)))
	}
	// A standby or a demoted old primary reports its own state; it must not
	// overwrite the status of the primary that the panel and alerts follow.
	if mine.Role != "" && mine.Role != "primary" {
		return
	}
	// Never downgrade an in-flight operation from a heartbeat. Embedded
	// engines report "running" with no process, so a heartbeat would otherwise
	// end a restore before the file is copied.
	if a.Status == "provisioning" || a.Status == "restoring" {
		return
	}
	status := app.State
	switch app.State {
	case "crashed":
		status = "failed"
	case "deploying":
		status = "provisioning"
	}
	if status != "" && status != a.Status {
		_ = s.st.SetAddonStatus(a.ID, status, app.LastError)
	}
}

// injectAddonEnv merges bound addon connection details into a deploy plan. It
// refuses to deploy when the addon is not reachable from that phone: a
// database URL pointing at another device's loopback is worse than a clear
// error.
func (s *Server) injectAddonEnv(p model.Project, serverID string, env map[string]string) error {
	addons, err := s.st.ListProjectAddons(p.ID)
	if err != nil {
		return err
	}
	if p.ParentID != "" {
		if parentAddons, err := s.st.ListProjectAddons(p.ParentID); err == nil {
			addons = append(addons, parentAddons...)
		}
	}
	seen := map[string]bool{}
	for _, a := range addons {
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		if a.Status != "running" {
			return fmt.Errorf("%s is not running (%s); start it before deploying", a.Name, a.Status)
		}
		creds, err := s.decryptAddonCreds(a)
		if err != nil {
			return fmt.Errorf("cannot decrypt credentials for %s: %w", a.Name, err)
		}
		params := addonParams(a, creds)
		if a.PinnedServer != serverID {
			if a.Kind == engines.KindFile {
				return fmt.Errorf("%s is a file on another phone; pin this service to %s", a.Name, a.PinnedServer)
			}
			port, err := s.ensureAddonRelay(a, serverID)
			if err != nil {
				return fmt.Errorf("cannot reach %s from %s: %w", a.Name, serverID, err)
			}
			params.Port = port
			params.DataDir = ""
		}
		vars := engines.EnvFor(a.Engine, params)
		prefix := "PHLEET_ADDON_" + strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(a.Slug, "db-"), "-", "_"))
		for k, v := range vars {
			env[k] = v
			env[prefix+"_"+k] = v
		}
		if u := addonURL(a.Engine, vars); u != "" {
			env[prefix+"_URL"] = u
		}
		if a.PinnedServer != serverID {
			env[prefix+"_RELAYED"] = "true"
		}
	}
	return nil
}

// projectAddonSummary is used by the project view and the create wizard.
func (s *Server) projectAddons(projectID string) []addonView {
	addons, err := s.st.ListProjectAddons(projectID)
	if err != nil || len(addons) == 0 {
		return nil
	}
	out := make([]addonView, 0, len(addons))
	for _, a := range addons {
		out = append(out, s.buildAddonView(a, false))
	}
	return out
}

// onAddonEvent folds provisioning/backup/restore progress into the addon and
// its placement. Log lines go to addon_logs so the UI can tail them.
func (s *Server) onAddonEvent(serverID string, ev agentproto.AddonEvent) {
	if ev.AddonID == "" {
		return
	}
	if ev.Phase == "log" && ev.Line != "" {
		_ = s.st.AddAddonLog(ev.AddonID, ev.Line)
		return
	}
	if ev.BackupID != "" {
		switch ev.Status {
		case "backup_ok":
			b, err := s.st.GetBackup(ev.BackupID)
			if err == nil && b.Status == "running" {
				status := "complete"
				if b.Path == "" {
					status = "failed"
				}
				_ = s.st.FinishBackup(b.ID, status, b.Size, b.Path, "")
			}
			return
		case "backup_failed":
			_ = s.st.FinishBackup(ev.BackupID, "failed", 0, "", ev.Error)
			return
		}
	}
	if ev.Phase != "status" {
		return
	}
	// A standby reports its own state. Only a primary event is allowed to
	// change the addon's headline status.
	role := "primary"
	if placements, err := s.st.ListAddonPlacements(ev.AddonID); err == nil {
		for _, pl := range placements {
			if pl.ServerID == serverID {
				role = pl.Role
				break
			}
		}
	}
	applyAddonStatus := role == "primary" || role == ""
	switch ev.Status {
	case "running", "active":
		if applyAddonStatus {
			_ = s.st.SetAddonStatus(ev.AddonID, "running", "")
			if a, err := s.st.GetAddon(ev.AddonID); err == nil && a.AutoFailover {
				_ = s.st.SetAddonLease(a.ID, time.Now().Unix()+int64(leaseSecOrDefault(a)))
			}
		}
		_ = s.st.UpdateAddonPlacementStatus(ev.AddonID, serverID, "running", 0, 0, "")
		s.triggerReconcile()
	case "failed":
		if applyAddonStatus {
			_ = s.st.SetAddonStatus(ev.AddonID, "failed", ev.Error)
		}
		_ = s.st.UpdateAddonPlacementStatus(ev.AddonID, serverID, "failed", 0, 0, ev.Error)
	default:
		if ev.Status != "" && applyAddonStatus {
			_ = s.st.SetAddonStatus(ev.AddonID, ev.Status, ev.Error)
		}
	}
	if ev.Error != "" {
		s.log.Warn("addon event", "addon", ev.AddonID, "status", ev.Status, "err", ev.Error)
	}
}

// reconcileAddon never invents a placement or an empty database. It only makes
// sure an existing, placed addon is provisioned when its phone is reachable.
func (s *Server) reconcileAddon(a model.Addon) {
	// Backfill fields introduced after an addon was created.
	changed := false
	if a.BridgePort == 0 && a.Kind != engines.KindFile {
		if p, err := s.allocateBridgePort(); err == nil {
			a.BridgePort = p
			changed = true
		}
	}
	if a.Hostname == "" && a.Kind == engines.KindObjectStore {
		if h, err := s.addonHostname(a.Name); err == nil {
			a.Hostname = h
			changed = true
		}
	}
	if changed {
		_ = s.st.UpdateAddon(a)
	}
	placements, err := s.st.ListAddonPlacements(a.ID)
	if err != nil || len(placements) == 0 {
		return
	}
	for _, pl := range placements {
		if pl.Desired == "stopped" {
			continue
		}
		if !s.hub.Online(pl.ServerID) {
			if pl.Role != "standby" && a.Status != "unreachable" && a.Status != "stopped" {
				_ = s.st.SetAddonStatus(a.ID, "unreachable", "phone is offline")
			}
			continue
		}
		if pl.Role == "standby" {
			if pl.Status == "pending" || pl.Status == "failed" {
				if s.addonCooldown(a.ID, pl.ServerID) {
					continue
				}
				if err := s.provisionAddonPlacement(a, pl); err != nil {
					s.log.Warn("reconcile standby", "addon", a.Slug, "server", pl.ServerID, "err", err)
				}
			}
			continue
		}
		switch a.Status {
		case "unreachable":
			// The phone is back but a new heartbeat has not arrived yet.
			_ = s.st.SetAddonStatus(a.ID, "running", "")
		case "provisioning", "restoring":
			// A status event should end these. If the agent vanished mid-way,
			// unblock the addon after a generous timeout.
			if a.UpdatedAt > 0 && time.Since(time.Unix(a.UpdatedAt, 0)) > 20*time.Minute {
				_ = s.st.SetAddonStatus(a.ID, "failed", "operation timed out; retrying provisioning")
			}
		case "pending", "failed":
			if s.addonCooldown(a.ID, pl.ServerID) {
				continue
			}
			if err := s.provisionAddonPlacement(a, pl); err != nil {
				s.log.Warn("reconcile addon", "addon", a.Slug, "err", err)
			}
		}
	}
	if a.AutoFailover {
		s.autoFailover(a)
	}
}

func (s *Server) addonCooldown(addonID, serverID string) bool {
	key := "addon-provision:" + addonID + ":" + serverID
	s.recMu.Lock()
	defer s.recMu.Unlock()
	last, seen := s.recAt[key]
	if seen && time.Since(last) < 2*time.Minute {
		return true
	}
	s.recAt[key] = time.Now()
	return false
}
