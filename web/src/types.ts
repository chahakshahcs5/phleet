export interface ServerCaps {
  root?: boolean
  termux?: boolean
  userland?: boolean
  termuxApi?: boolean
  adb?: boolean
  pkg?: boolean
  pkgManager?: string
  pkgInstall?: boolean
  ip?: string
  sshPort?: string
  engines?: Record<string, boolean>
  tools?: Record<string, boolean>
  thermalZones?: number
  battery?: boolean
  chargeControl?: boolean
  cpus?: number
  home?: string
  [key: string]: unknown
}

export interface Metrics {
  cpuPercent: number
  memUsed: number
  memTotal: number
  diskUsed: number
  diskTotal: number
  tempC: number
  batteryPct: number
  batteryTemp: number
  charging: boolean
  load1: number
  netRx: number
  netTx: number
  uptime: number
  at: number
}

export interface Sample extends Metrics {
  ts: number
}

export interface Server {
  id: string
  name: string
  hostname: string
  os: string
  arch: string
  agentVersion: string
  caps: ServerCaps
  labels: Record<string, string>
  status: string
  drain: boolean
  lastSeen: number
  createdAt: number
  updatedAt: number
  online: boolean
  latest?: Sample
}

export interface EventItem {
  id: number
  ts: number
  actor: string
  action: string
  target: string
  detail: string
}

export interface EventsPage {
  events: EventItem[]
  page: {
    limit: number
    hasMore: boolean
    total: number
    nextBefore?: number
  }
}

export interface FSEntry {
  name: string
  path: string
  dir: boolean
  symlink: boolean
  size: number
  mode: string
  modTime: number
}

export interface FSList {
  path: string
  parent: string
  roots: string[]
  entries: FSEntry[]
}

export interface FSRead {
  path: string
  data: string
  size: number
  mode: string
  modTime: number
  binary: boolean
  truncated: boolean
}

export interface Proc {
  pid: number
  user: string
  command: string
  cpu: number
  rss: number
}

export interface ExecResult {
  output: string
  exitCode: number
  durationMs: number
}

export interface Me {
  authenticated: boolean
  version: string
  dataDir: string
  publicUrl: string
  agentsOnline: number
}

export interface Meta {
  version: string
  publicUrl: string
  totpRequired?: boolean
}

export interface Placement {
  projectId: string
  serverId: string
  port: number
  status: string
  desired: string
  pid: number
  restarts: number
  release: string
  lastError: string
  updatedAt: number
}

export interface PlacementView extends Placement {
  serverName: string
  online: boolean
  drained: boolean
}

export interface Deployment {
  id: string
  projectId: string
  serverId: string
  commitSha: string
  status: string
  release: string
  startedAt: number
  finishedAt: number
  detail: string
}

export interface Project {
  id: string
  name: string
  slug: string
  repo: string
  branch: string
  runtime: string
  buildCmd: string
  startCmd: string
  healthPath: string
  hostname: string
  selector: string
  replicas: number
  pinnedServer: string
  sourceType: string
  sourceSize: number
  sourceUpdatedAt: number
  parentId?: string
  kind?: string
  stackOrder?: number
  createdAt: number
}

export interface ProjectView extends Project {
  env?: Record<string, string>
  placements: PlacementView[]
  services?: ProjectView[]
  addons?: Addon[]
  latest?: Deployment
  url: string
}

export interface EngineInfo {
  id: string
  name: string
  kind: string
  summary: string
  service: boolean
  category?: string
  website?: string
  source?: string
  icon?: string
  version?: string
  ingress: boolean
  requires?: string[]
  package: string
  binaries: string[]
  minRamMb: number
  platforms?: string[]
  embedded: boolean
  customInstall: boolean
  console: boolean
  noBackup: boolean
  replication: string
  defaultPort: number
}

export interface AddonPlacement {
  addonId: string
  serverId: string
  role: string
  status: string
  desired: string
  port: number
  pid: number
  restarts: number
  lagMs: number
  lastError: string
  updatedAt: number
  serverName: string
  online: boolean
  drained: boolean
}

export interface AddonRelay {
  addonId: string
  serverId: string
  port: number
  updatedAt: number
  serverName: string
  online: boolean
}

export interface AddonBinding {
  addonId: string
  projectId: string
  createdAt: number
}

export interface Backup {
  id: string
  addonId: string
  serverId: string
  kind: string
  status: string
  size: number
  error?: string
  createdAt: number
  finishedAt: number
}

export interface Addon {
  id: string
  name: string
  slug: string
  engine: string
  kind: string
  ownerProjectId?: string
  pinnedServer: string
  port: number
  bridgePort?: number
  hostname?: string
  epoch: number
  dataDir?: string
  sizeLimitMB: number
  replicationMode: string
  autoFailover: boolean
  failoverAfterSec: number
  maxLagMs: number
  leaseSec: number
  leaseUntil: number
  status: string
  lastError?: string
  createdAt: number
  updatedAt: number
  engineName: string
  embedded: boolean
  console: boolean
  noBackup: boolean
  service: boolean
  appId?: string
  version?: string
  category?: string
  website?: string
  source?: string
  icon?: string
  ingress: boolean
  installed: boolean
  bridgeActive: boolean
  placements: AddonPlacement[]
  bindings: AddonBinding[]
  relays?: AddonRelay[]
  backups?: Backup[]
  env?: Record<string, string>
  url?: string
  s3Endpoint?: string
}

export interface AddonConnection {
  env: Record<string, string>
  url: string
  user: string
  password: string
  database: string
  host: string
  port: number
  note: string
}

export interface AddonLog {
  ts: number
  line: string
}

export interface ServiceInput {
  name: string
  slug: string
  buildCmd: string
  startCmd: string
  healthPath: string
  env?: Record<string, string>
}

export interface DeployLog {
  ts: number
  line: string
}

export interface DeploymentDetail {
  deployment: Deployment
  logs: DeployLog[]
}

export interface AppLogs {
  slug: string
  path: string
  size: number
  truncated: boolean
  serverId: string
  logs: string
}

export interface Alert {
  id: number
  key: string
  kind: string
  severity: string
  status: string
  target: string
  message: string
  createdAt: number
  updatedAt: number
  resolvedAt: number
}

export interface AlertRules {
  tempC: number
  batteryPct: number
  diskPct: number
  offlineSec: number
}

export interface NotifyConfig {
  telegramToken: string
  telegramChat: string
  webhookUrl: string
}

export interface DeployResult {
  deployments: { id: string; serverId: string; port: number; status: string }[]
  skipped: string[]
}

export interface SetupInfo {
  serverUrl: string
  agentToken: string
  sshAvailable: boolean
}

export interface TunnelStatus {
  configured: boolean
  publicUrl: string
  tunnelId: string
  tunnelName: string
  serviceInstalled: boolean
  serviceStatus: string
  startType: string
  mode: string
  running: boolean
  pid?: number
  binary?: string
  logPath?: string
  error?: string
}

export interface ProvisionResult {
  ok: boolean
  output?: string
  error?: string
  jobId?: string
}

export interface APIToken {
  id: string
  name: string
  role: 'admin' | 'viewer'
  createdAt: number
  lastUsedAt: number
}

export interface Job {
  id: string
  kind: string
  target: string
  status: 'running' | 'succeeded' | 'failed'
  output: string
  error?: string
  createdAt: number
  updatedAt: number
}
