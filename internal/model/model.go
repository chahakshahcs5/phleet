// Package model contains the types shared between the control plane, the
// agent protocol and the HTTP API.
package model

type Server struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Hostname     string            `json:"hostname"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	AgentVersion string            `json:"agentVersion"`
	Caps         map[string]any    `json:"caps"`
	Labels       map[string]string `json:"labels"`
	Status       string            `json:"status"`
	Drain        bool              `json:"drain"`
	LastSeen     int64             `json:"lastSeen"`
	CreatedAt    int64             `json:"createdAt"`
	UpdatedAt    int64             `json:"updatedAt"`
}

// Metrics is a single heartbeat sample sent by an agent.
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

// Sample is a metrics row as returned by the store.
type Sample struct {
	Metrics
	TS int64 `json:"ts"`
}

type Event struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

type Project struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Repo            string `json:"repo"`
	Branch          string `json:"branch"`
	Runtime         string `json:"runtime"`
	BuildCmd        string `json:"buildCmd"`
	StartCmd        string `json:"startCmd"`
	HealthPath      string `json:"healthPath"`
	Hostname        string `json:"hostname"`
	Selector        string `json:"selector"`
	Replicas        int    `json:"replicas"`
	PinnedServer    string `json:"pinnedServer"`
	SourceType      string `json:"sourceType"`
	SourceSize      int64  `json:"sourceSize"`
	SourceUpdatedAt int64  `json:"sourceUpdatedAt"`
	ParentID        string `json:"parentId,omitempty"`
	Kind            string `json:"kind"`
	StackOrder      int    `json:"stackOrder,omitempty"`
	EnvEnc          string `json:"-"`
	CreatedAt       int64  `json:"createdAt"`
}

// Placement is where a project runs. One per project in phase 2.
type Placement struct {
	ProjectID string `json:"projectId"`
	ServerID  string `json:"serverId"`
	Port      int    `json:"port"`
	Status    string `json:"status"`
	Desired   string `json:"desired"`
	PID       int    `json:"pid"`
	Restarts  int    `json:"restarts"`
	Release   string `json:"release"`
	LastError string `json:"lastError"`
	UpdatedAt int64  `json:"updatedAt"`
}

type Deployment struct {
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	ServerID   string `json:"serverId"`
	CommitSHA  string `json:"commitSha"`
	Status     string `json:"status"`
	Release    string `json:"release"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	Detail     string `json:"detail"`
}

type DeployLog struct {
	TS   int64  `json:"ts"`
	Line string `json:"line"`
}

// Addon is a data service (database, cache or file store) placed on one phone
// and optionally bound to one or more projects. It is deliberately separate
// from Project: state, credentials and lifecycle differ from stateless apps.
type Addon struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	Engine           string `json:"engine"`
	Kind             string `json:"kind"`
	OwnerProjectID   string `json:"ownerProjectId,omitempty"`
	PinnedServer     string `json:"pinnedServer"`
	Port             int    `json:"port"`
	BridgePort       int    `json:"bridgePort,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
	Epoch            int    `json:"epoch"`
	DataDir          string `json:"dataDir,omitempty"`
	SizeLimitMB      int    `json:"sizeLimitMB,omitempty"`
	ReplicationMode  string `json:"replicationMode"`
	AutoFailover     bool   `json:"autoFailover"`
	FailoverAfterSec int    `json:"failoverAfterSec"`
	MaxLagMS         int64  `json:"maxLagMs"`
	LeaseSec         int    `json:"leaseSec"`
	LeaseUntil       int64  `json:"leaseUntil"`
	Status           string `json:"status"`
	LastError        string `json:"lastError,omitempty"`
	EnvEnc           string `json:"-"`
	ConfigEnc        string `json:"-"`
	Service          bool   `json:"service"`
	AppID            string `json:"appId,omitempty"`
	Version          string `json:"version,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
}

// AddonPlacement is where an addon instance lives. Unlike a stateless app this
// row is sticky: the volume lives on that phone, so the scheduler never moves
// it automatically.
type AddonPlacement struct {
	AddonID   string `json:"addonId"`
	ServerID  string `json:"serverId"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	Desired   string `json:"desired"`
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	Restarts  int    `json:"restarts"`
	LagMS     int64  `json:"lagMs"`
	LastError string `json:"lastError"`
	UpdatedAt int64  `json:"updatedAt"`
}

// AddonRelay is a loopback facade on an app phone that forwards to an addon
// living on another phone. One row per (addon, server).
type AddonRelay struct {
	AddonID   string `json:"addonId"`
	ServerID  string `json:"serverId"`
	Port      int    `json:"port"`
	UpdatedAt int64  `json:"updatedAt"`
}

type AddonBinding struct {
	AddonID   string `json:"addonId"`
	ProjectID string `json:"projectId"`
	CreatedAt int64  `json:"createdAt"`
}

type Backup struct {
	ID         string `json:"id"`
	AddonID    string `json:"addonId"`
	ServerID   string `json:"serverId"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Size       int64  `json:"size"`
	Path       string `json:"-"`
	Error      string `json:"error,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	FinishedAt int64  `json:"finishedAt"`
}

type Alert struct {
	ID         int64  `json:"id"`
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	Status     string `json:"status"`
	Target     string `json:"target"`
	Message    string `json:"message"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	ResolvedAt int64  `json:"resolvedAt"`
}

type AlertRules struct {
	TempC      float64 `json:"tempC"`
	BatteryPct int     `json:"batteryPct"`
	DiskPct    int     `json:"diskPct"`
	OfflineSec int     `json:"offlineSec"`
}

type NotifyConfig struct {
	TelegramToken string `json:"telegramToken"`
	TelegramChat  string `json:"telegramChat"`
	WebhookURL    string `json:"webhookUrl"`
}
