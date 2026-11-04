// Package agentproto defines the JSON messages exchanged over the agent
// WebSocket. Every message is an Envelope; request/response pairs are
// correlated with ID/ReplyTo. Streaming messages (PTY) use their own types
// and a session id.
package agentproto

import "encoding/json"

const (
	TypeHello        = "hello"
	TypeHelloAck     = "hello.ack"
	TypeHeartbeat    = "heartbeat"
	TypeExec         = "exec"
	TypePTYOpen      = "pty.open"
	TypePTYData      = "pty.data"
	TypePTYResize    = "pty.resize"
	TypePTYClose     = "pty.close"
	TypePTYExit      = "pty.exit"
	TypeFSList       = "fs.list"
	TypeFSRead       = "fs.read"
	TypeFSWrite      = "fs.write"
	TypeFSWriteChunk = "fs.write.chunk"
	TypeFSExtract    = "fs.extract"
	TypeFSMkdir      = "fs.mkdir"
	TypeFSRename     = "fs.rename"
	TypeFSDelete     = "fs.delete"
	TypeFSStat       = "fs.stat"
	TypeProcList     = "proc.list"

	TypeDeployRequest = "deploy.request"
	TypeDeployEvent   = "deploy.event"
	TypeAppLogs       = "app.logs"
	TypeAppControl    = "app.control"
	TypeAppReleases   = "app.releases"

	TypeAddonProvision = "addon.provision"
	TypeAddonControl   = "addon.control"
	TypeAddonBackup    = "addon.backup"
	TypeAddonRestore   = "addon.restore"
	TypeAddonVerify    = "addon.verify"
	TypeAddonEvent     = "addon.event"
	TypeAddonRelay     = "addon.relay"
	TypeAddonPromote   = "addon.promote"
	TypeAddonLease     = "addon.lease"

	TypeAgentUpdate = "agent.update"

	TypeTaskSync  = "task.sync"
	TypeTaskRun   = "task.run"
	TypeTaskLogs  = "task.logs"
	TypeTaskEvent = "task.event"
)

type Envelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	ReplyTo string          `json:"replyTo,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func MustData(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type Hello struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Hostname     string         `json:"hostname"`
	OS           string         `json:"os"`
	Arch         string         `json:"arch"`
	AgentVersion string         `json:"agentVersion"`
	Home         string         `json:"home"`
	User         string         `json:"user"`
	Caps         map[string]any `json:"caps"`
}

// HelloAck is sent after a successful hello. Token is set when the control
// plane enrolls the phone (or rotates a legacy shared token); it is the
// per-device credential the agent must use for every later connection.
type HelloAck struct {
	Token string `json:"token,omitempty"`
	OK    bool   `json:"ok"`
}

type Heartbeat struct {
	Metrics Metrics     `json:"metrics"`
	Apps    []AppStatus `json:"apps,omitempty"`
	// Tasks is the compact run state of every scheduled task on the phone.
	// Full output travels in task.event; the heartbeat only keeps the panel
	// roughly in sync while a task is running.
	Tasks []TaskStatus `json:"tasks,omitempty"`
	// Caps is sent only when the capability map changes (an engine installed on
	// the phone, or a new package manager). Empty means "unchanged".
	Caps map[string]any `json:"caps,omitempty"`
}

// AppStatus is a supervised process reported by the agent in every heartbeat.
type AppStatus struct {
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	Kind         string  `json:"kind,omitempty"`
	Role         string  `json:"role,omitempty"`
	Epoch        int     `json:"epoch,omitempty"`
	State        string  `json:"state"`
	Port         int     `json:"port"`
	PID          int     `json:"pid"`
	Restarts     int     `json:"restarts"`
	CPUPercent   float64 `json:"cpuPercent,omitempty"`
	RSS          int64   `json:"rss,omitempty"`
	HasUsage     bool    `json:"hasUsage,omitempty"`
	LagMS        int64   `json:"lagMs,omitempty"`
	LastError    string  `json:"lastError,omitempty"`
	DeploymentID string  `json:"deploymentId,omitempty"`
	DeployStatus string  `json:"deployStatus,omitempty"`
}

type Metrics struct {
	CPUPercent  float64 `json:"cpuPercent"`
	MemUsed     int64   `json:"memUsed"`
	MemTotal    int64   `json:"memTotal"`
	DiskUsed    int64   `json:"diskUsed"`
	DiskTotal   int64   `json:"diskTotal"`
	TempC       float64 `json:"tempC"`
	BatteryPct  int     `json:"batteryPct"`
	BatteryTemp float64 `json:"batteryTemp"`
	Charging    bool    `json:"charging"`
	Load1       float64 `json:"load1"`
	NetRx       int64   `json:"netRx"`
	NetTx       int64   `json:"netTx"`
	Uptime      int64   `json:"uptime"`
	At          int64   `json:"at"`
}

type ExecRequest struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeoutSec"`
	Cwd        string `json:"cwd,omitempty"`
}

type ExecResponse struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
	Duration int64  `json:"durationMs"`
}

type PTYOpen struct {
	Session string `json:"session"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Shell   string `json:"shell,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

type PTYData struct {
	Session string `json:"session"`
	Data    []byte `json:"data"` // base64 on the wire
}

type PTYResize struct {
	Session string `json:"session"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}

type PTYSession struct {
	Session string `json:"session"`
}

type PTYExit struct {
	Session  string `json:"session"`
	ExitCode int    `json:"exitCode"`
}

type FSListRequest struct {
	Path string `json:"path"`
}

type FSEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Dir     bool   `json:"dir"`
	Symlink bool   `json:"symlink"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`
}

type FSListResponse struct {
	Path    string    `json:"path"`
	Parent  string    `json:"parent"`
	Roots   []string  `json:"roots"`
	Entries []FSEntry `json:"entries"`
}

type FSReadRequest struct {
	Path     string `json:"path"`
	MaxBytes int64  `json:"maxBytes"`
}

type FSReadResponse struct {
	Path      string `json:"path"`
	Data      []byte `json:"data"` // base64 on the wire
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	ModTime   int64  `json:"modTime"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

type FSWriteRequest struct {
	Path string `json:"path"`
	Data []byte `json:"data"` // base64 on the wire
	Mode uint32 `json:"mode,omitempty"`
}

type FSWriteChunkRequest struct {
	Path   string `json:"path"`
	Data   []byte `json:"data"` // base64 on the wire, keep chunks under 2 MB
	Offset int64  `json:"offset"`
}

type FSExtractRequest struct {
	Path     string `json:"path"`   // zip file on the phone
	Target   string `json:"target"` // destination directory
	MaxBytes int64  `json:"maxBytes,omitempty"`
}

type FSExtractResponse struct {
	Target string `json:"target"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
}

type FSStatResponse struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`
	Dir     bool   `json:"dir"`
}

type FSMkdirRequest struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
}

type FSRenameRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type FSDeleteRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type ProcListResponse struct {
	Processes []Proc `json:"processes"`
}

type Proc struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	Command string  `json:"command"`
	CPU     float64 `json:"cpu"`
	RSS     int64   `json:"rss"`
}

// DeployRequest is a self-contained plan executed by the agent. The control
// plane only observes: if it disappears mid-deploy, the agent still finishes
// or rolls back on its own.
// GitAuth carries one deployment's repository credentials. The control plane
// encrypts them at rest and only sends them over the authenticated agent
// connection; they are never written to the project environment.
type GitAuth struct {
	Type     string `json:"type,omitempty"` // https | ssh
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

type DeployRequest struct {
	DeploymentID  string            `json:"deploymentId"`
	ProjectID     string            `json:"projectId"`
	Slug          string            `json:"slug"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind,omitempty"`
	Ref           string            `json:"ref,omitempty"`
	Repo          string            `json:"repo"`
	Branch        string            `json:"branch"`
	GitAuth       GitAuth           `json:"gitAuth,omitempty"`
	BuildCmd      string            `json:"buildCmd"`
	StartCmd      string            `json:"startCmd"`
	StaticDir     string            `json:"staticDir,omitempty"`
	HealthPath    string            `json:"healthPath"`
	Port          int               `json:"port"`
	SourcePath    string            `json:"sourcePath,omitempty"`
	Theme         string            `json:"theme,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Secrets       map[string]string `json:"secrets,omitempty"`
	KeepReleases  int               `json:"keepReleases"`
	RestartPolicy string            `json:"restartPolicy,omitempty"`
}

type DeployEvent struct {
	DeploymentID string `json:"deploymentId"`
	Phase        string `json:"phase"` // log | status
	Status       string `json:"status,omitempty"`
	Line         string `json:"line,omitempty"`
	Error        string `json:"error,omitempty"`
	Release      string `json:"release,omitempty"`
	Commit       string `json:"commit,omitempty"`
}

type AppLogsRequest struct {
	Slug      string `json:"slug"`
	Bytes     int    `json:"bytes"`
	Offset    int64  `json:"offset,omitempty"`
	FromStart bool   `json:"fromStart,omitempty"`
}

type AppLogsResponse struct {
	Slug      string `json:"slug"`
	Path      string `json:"path"`
	Data      []byte `json:"data"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	Next      int64  `json:"next"`
	Rotated   bool   `json:"rotated,omitempty"`
	Truncated bool   `json:"truncated"`
}

// AppControl actions: start | stop | restart | rollback | delete.
type AppControlRequest struct {
	Slug   string `json:"slug"`
	Action string `json:"action"`
	// Release selects a specific kept release for rollback. Empty means the
	// previous release (the original behaviour).
	Release string `json:"release,omitempty"`
}

type AppReleasesRequest struct {
	Slug string `json:"slug"`
}

type AppRelease struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Current    bool   `json:"current"`
	ModifiedAt int64  `json:"modifiedAt"`
}

type AppReleasesResponse struct {
	Slug     string       `json:"slug"`
	Releases []AppRelease `json:"releases"`
}

// AddonSpec is a self-contained provisioning plan. The agent owns execution so
// a sleeping control plane cannot leave a half-built database behind.
type AddonSpec struct {
	AddonID       string            `json:"addonId"`
	Slug          string            `json:"slug"`
	Name          string            `json:"name"`
	Engine        string            `json:"engine"`
	Kind          string            `json:"kind"`
	Role          string            `json:"role,omitempty"` // primary | standby
	Epoch         int               `json:"epoch,omitempty"`
	Port          int               `json:"port"`
	DataDir       string            `json:"dataDir"`
	File          string            `json:"file,omitempty"`
	User          string            `json:"user"`
	Password      string            `json:"password"`
	Database      string            `json:"database"`
	Superuser     string            `json:"superuser"`
	SuperPassword string            `json:"superPassword"`
	ReplicaUser   string            `json:"replicaUser,omitempty"`
	ReplicaPass   string            `json:"replicaPass,omitempty"`
	PrimaryHost   string            `json:"primaryHost,omitempty"`
	PrimaryPort   int               `json:"primaryPort,omitempty"`
	RelayPort     int               `json:"relayPort,omitempty"`
	LeaseSeconds  int               `json:"leaseSeconds,omitempty"`
	SizeLimitMB   int               `json:"sizeLimitMB,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
}

// AddonRelaySpec asks an agent to expose a loopback facade for an addon. Apps
// on that phone use it when the addon lives on a different phone.
type AddonRelaySpec struct {
	AddonID      string `json:"addonId"`
	Action       string `json:"action"` // start | stop
	LocalPort    int    `json:"localPort"`
	TargetServer string `json:"targetServer,omitempty"`
	TargetPort   int    `json:"targetPort,omitempty"`
}

// AddonPromoteRequest promotes a standby to primary after the old primary has
// been fenced.
type AddonPromoteRequest struct {
	AddonID       string `json:"addonId"`
	Slug          string `json:"slug"`
	Engine        string `json:"engine"`
	DataDir       string `json:"dataDir"`
	File          string `json:"file,omitempty"`
	Epoch         int    `json:"epoch,omitempty"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	Password      string `json:"password"`
	Database      string `json:"database"`
	Superuser     string `json:"superuser,omitempty"`
	SuperPassword string `json:"superPassword,omitempty"`
	LeaseSeconds  int    `json:"leaseSeconds,omitempty"`
}

// AddonLeaseRequest updates an addon's self-fence lease without restarting it.
type AddonLeaseRequest struct {
	AddonID      string `json:"addonId"`
	Slug         string `json:"slug"`
	LeaseSeconds int    `json:"leaseSeconds"`
}

// AddonControlRequest actions: start | stop | restart | delete.
type AddonControlRequest struct {
	AddonID   string `json:"addonId"`
	Slug      string `json:"slug"`
	Action    string `json:"action"`
	PurgeData bool   `json:"purgeData,omitempty"`
}

type AddonBackupRequest struct {
	AddonID    string `json:"addonId"`
	Slug       string `json:"slug"`
	BackupID   string `json:"backupId"`
	Engine     string `json:"engine"`
	UploadPath string `json:"uploadPath"`
	DataDir    string `json:"dataDir"`
	File       string `json:"file,omitempty"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	Database   string `json:"database"`
}

type AddonRestoreRequest struct {
	AddonID      string `json:"addonId"`
	Slug         string `json:"slug"`
	BackupID     string `json:"backupId"`
	Engine       string `json:"engine"`
	DownloadPath string `json:"downloadPath"`
	Checksum     string `json:"checksum,omitempty"` // SHA-256 of the backup file
	DataDir      string `json:"dataDir"`
	File         string `json:"file,omitempty"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password"`
	Database     string `json:"database"`
}

// AddonEvent streams provisioning, backup and restore progress back to the
// control plane. Phase is log or status.
// AddonVerifyRequest checks a backup without restoring it.
type AddonVerifyRequest struct {
	AddonID      string `json:"addonId"`
	Slug         string `json:"slug"`
	BackupID     string `json:"backupId"`
	Engine       string `json:"engine"`
	DownloadPath string `json:"downloadPath"`
	Checksum     string `json:"checksum,omitempty"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password"`
	Database     string `json:"database"`
}

type AddonEvent struct {
	AddonID  string `json:"addonId"`
	BackupID string `json:"backupId,omitempty"`
	Phase    string `json:"phase"`
	Status   string `json:"status,omitempty"`
	Line     string `json:"line,omitempty"`
	Error    string `json:"error,omitempty"`
}

// TaskSpec is one recurring command as executed by the agent. The control
// plane re-sends the full list for a phone whenever a task, project env or
// addon binding changes; the agent persists it and runs it locally even while
// the panel is unreachable.
type TaskSpec struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Slug       string            `json:"slug,omitempty"` // project slug, when Project is true
	Project    bool              `json:"project,omitempty"`
	Command    string            `json:"command"`
	Cron       string            `json:"cron"`
	TimeoutSec int               `json:"timeoutSec,omitempty"`
	Enabled    bool              `json:"enabled"`
	Env        map[string]string `json:"env,omitempty"`
}

type TaskSyncRequest struct {
	Tasks []TaskSpec `json:"tasks"`
}

type TaskRunRequest struct {
	TaskID string `json:"taskId"`
}

type TaskLogsRequest struct {
	TaskID string `json:"taskId"`
	Bytes  int    `json:"bytes,omitempty"`
}

type TaskLogsResponse struct {
	TaskID    string `json:"taskId"`
	Path      string `json:"path"`
	Data      []byte `json:"data"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
}

// TaskStatus is the compact heartbeat state for one task.
type TaskStatus struct {
	ID       string `json:"id"`
	LastRun  int64  `json:"lastRun"`
	Status   string `json:"status"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
	Running  bool   `json:"running,omitempty"`
}

// TaskEvent reports a run transition and, on completion, a bounded output
// tail. The full log stays on the phone and is fetched with task.logs.
type TaskEvent struct {
	TaskID     string `json:"taskId"`
	Name       string `json:"name,omitempty"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exitCode,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Empty struct{}
