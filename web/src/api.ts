import type {
  Addon,
  APIToken,
  AddonConnection,
  AddonLog,
  Alert,
  AlertRules,
  AppLogs,
  Backup,
  DeployResult,
  Deployment,
  DeploymentDetail,
  EngineInfo,
  EventItem,
  EventsPage,
  ExecResult,
  FSList,
  FSRead,
  Job,
  Me,
  Meta,
  NotifyConfig,
  Proc,
  ProjectView,
  ProvisionResult,
  Sample,
  Server,
  ServiceInput,
  SetupInfo,
  TunnelStatus,
  User,
} from './types'

export class ApiError extends Error {
  status: number
  data?: unknown
  constructor(status: number, message: string, data?: unknown) {
    super(message)
    this.status = status
    this.data = data
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const res = await fetch(path, { credentials: 'include', ...init, headers })
  if (!res.ok) {
    let message = res.statusText
    let data: unknown
    try {
      data = await res.json()
      const body = data as { error?: string }
      message = body.error || message
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, message, data)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  meta: () => request<Meta>('/api/meta'),
  me: () => request<Me>('/api/me'),
  login: (body: { token?: string; username?: string; password?: string; code?: string }) =>
    request<{ ok: boolean }>('/api/login', { method: 'POST', body: JSON.stringify(body) }),
  logout: () => request<{ ok: boolean }>('/api/logout', { method: 'POST' }),

  servers: () =>
    request<{ servers: Server[]; now: number; agentsOnline: number }>('/api/servers'),
  server: (id: string) => request<{ server: Server }>(`/api/servers/${id}`),
  patchServer: (id: string, body: { name?: string; labels?: Record<string, string>; drain?: boolean }) =>
    request<{ server: Server }>(`/api/servers/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteServer: (id: string) => request<{ ok: boolean }>(`/api/servers/${id}`, { method: 'DELETE' }),
  metrics: (id: string, minutes = 360) =>
    request<{ samples: Sample[]; bucketSec: number }>(`/api/servers/${id}/metrics?minutes=${minutes}`),
  exec: (id: string, command: string, timeoutSec = 30) =>
    request<ExecResult>(`/api/servers/${id}/exec`, {
      method: 'POST',
      body: JSON.stringify({ command, timeoutSec }),
    }),
  processes: (id: string) => request<{ processes: Proc[] }>(`/api/servers/${id}/processes`),

  fsList: (id: string, path: string) =>
    request<FSList>(`/api/servers/${id}/files?path=${encodeURIComponent(path)}`),
  fsRead: (id: string, path: string) =>
    request<FSRead>(`/api/servers/${id}/file?path=${encodeURIComponent(path)}`),
  fsWrite: (id: string, path: string, data: Uint8Array, mode?: number) =>
    request<{ ok: boolean }>(`/api/servers/${id}/file`, {
      method: 'PUT',
      body: JSON.stringify({ path, data: base64FromBytes(data), mode }),
    }),
  fsOp: (id: string, body: { op: string; path: string; to?: string; recursive?: boolean }) =>
    request<{ ok: boolean }>(`/api/servers/${id}/fs`, { method: 'POST', body: JSON.stringify(body) }),
  upload: (id: string, dir: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return request<{ ok: boolean; path: string; bytes: number }>(
      `/api/servers/${id}/upload?path=${encodeURIComponent(dir)}`,
      { method: 'POST', body: form },
    )
  },
  uploadFolder: (id: string, dir: string, blob: Blob) => {
    const form = new FormData()
    form.append('file', blob, 'upload.zip')
    return request<{ ok: boolean; target: string; files: number; bytes: number }>(
      `/api/servers/${id}/upload-folder?path=${encodeURIComponent(dir)}`,
      { method: 'POST', body: form },
    )
  },
  uploadProjectSource: (id: string, blob: Blob) => {
    const form = new FormData()
    form.append('file', blob, 'source.zip')
    return request<{ project: ProjectView; bytes: number }>(`/api/projects/${id}/source`, {
      method: 'POST',
      body: form,
    })
  },
  downloadUrl: (id: string, path: string) =>
    `/api/servers/${id}/download?path=${encodeURIComponent(path)}`,

  events: (params: { limit?: number; before?: number; action?: string; q?: string } = {}) => {
    const sp = new URLSearchParams()
    if (params.limit) sp.set('limit', String(params.limit))
    if (params.before) sp.set('before', String(params.before))
    if (params.action) sp.set('action', params.action)
    if (params.q) sp.set('q', params.q)
    return request<EventsPage>(`/api/events?${sp.toString()}`)
  },
  eventFacets: () => request<{ actions: string[]; actors: string[] }>('/api/events/facets'),

  setupInfo: () => request<SetupInfo>('/api/setup'),
  totp: () => request<{ enabled: boolean }>('/api/totp'),
  setupTOTP: () => request<{ secret: string; url: string }>('/api/totp/setup', { method: 'POST' }),
  enableTOTP: (code: string) =>
    request<{ enabled: boolean }>('/api/totp/enable', { method: 'POST', body: JSON.stringify({ code }) }),
  disableTOTP: () => request<{ enabled: boolean }>('/api/totp/disable', { method: 'POST' }),
  tunnelStatus: () => request<TunnelStatus>('/api/edge/tunnel'),
  tunnelAction: (action: 'start' | 'stop' | 'restart') =>
    request<TunnelStatus>(`/api/edge/tunnel/${action}`, { method: 'POST' }),
  provisionServer: (body: {
    name?: string
    target: string
    port?: number
    identityFile?: string
    serverUrl?: string
    async?: boolean
  }) => request<ProvisionResult>('/api/servers/provision', { method: 'POST', body: JSON.stringify(body) }),
  job: (id: string) => request<{ job: Job }>(`/api/jobs/${id}`),
  users: () => request<{ users: User[] }>('/api/users'),
  createUser: (body: { username: string; password: string; role: 'admin' | 'viewer' }) =>
    request<{ user: User }>('/api/users', { method: 'POST', body: JSON.stringify(body) }),
  updateUser: (id: string, body: { role?: 'admin' | 'viewer'; disabled?: boolean }) =>
    request<{ user: User }>(`/api/users/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  resetUserPassword: (id: string, password: string) =>
    request<{ ok: boolean }>(`/api/users/${id}/password`, {
      method: 'POST',
      body: JSON.stringify({ password }),
    }),
  deleteUser: (id: string) => request<{ ok: boolean }>(`/api/users/${id}`, { method: 'DELETE' }),
  apiTokens: () => request<{ tokens: APIToken[] }>('/api/api-tokens'),
  createAPIToken: (body: { name: string; role: 'admin' | 'viewer' }) =>
    request<{ token: string; apiToken: APIToken }>('/api/api-tokens', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  deleteAPIToken: (id: string) => request<{ ok: boolean }>(`/api/api-tokens/${id}`, { method: 'DELETE' }),

  projects: () => request<{ projects: ProjectView[] }>('/api/projects'),
  project: (id: string) => request<{ project: ProjectView }>(`/api/projects/${id}`),
  createProject: (body: {
    name: string
    repo: string
    branch: string
    buildCmd: string
    startCmd: string
    healthPath: string
    customHostname?: string
    serverId?: string
    selector?: string
    replicas?: number
    sourceType?: string
    env?: Record<string, string>
    stack?: boolean
    services?: ServiceInput[]
  }) => request<{ project: ProjectView }>('/api/projects', { method: 'POST', body: JSON.stringify(body) }),
  updateProject: (id: string, body: Partial<{
    name: string
    repo: string
    branch: string
    buildCmd: string
    startCmd: string
    healthPath: string
    customHostname: string
    selector: string
    replicas: number
    pinnedServer: string
    env: Record<string, string>
  }>) => request<{ project: ProjectView }>(`/api/projects/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteProject: (id: string, opts: { deleteAddons?: boolean } = {}) => {
    const sp = new URLSearchParams()
    if (opts.deleteAddons) sp.set('addons', 'delete')
    const q = sp.toString()
    return request<{ ok: boolean }>(`/api/projects/${id}${q ? `?${q}` : ''}`, { method: 'DELETE' })
  },
  deployProject: (id: string) =>
    request<DeployResult>(`/api/projects/${id}/deploy`, { method: 'POST' }),
  projectAction: (id: string, action: 'start' | 'stop' | 'restart' | 'rollback') =>
    request<{ ok: boolean; results: { serverId: string; ok: boolean; error?: string }[] }>(
      `/api/projects/${id}/${action}`,
      { method: 'POST' },
    ),
  deployments: (id: string) => request<{ deployments: Deployment[] }>(`/api/projects/${id}/deployments`),
  deployment: (id: string) => request<DeploymentDetail>(`/api/deployments/${id}`),
  appLogs: (id: string, serverId?: string) =>
    request<AppLogs>(`/api/projects/${id}/logs${serverId ? `?serverId=${encodeURIComponent(serverId)}` : ''}`),
  rebalance: () => request<{ ok: boolean }>('/api/rebalance', { method: 'POST' }),

  addonEngines: () => request<{ engines: EngineInfo[] }>('/api/addons/engines'),
  addons: () => request<{ addons: Addon[] }>('/api/addons'),
  addon: (id: string) => request<{ addon: Addon }>(`/api/addons/${id}`),
  createAddon: (body: {
    name: string
    engine: string
    serverId: string
    projectId?: string
    sizeLimitMB?: number
  }) => request<{ addon: Addon }>('/api/addons', { method: 'POST', body: JSON.stringify(body) }),
  deleteAddon: (id: string, opts: { force?: boolean; purge?: boolean } = {}) => {
    const sp = new URLSearchParams()
    if (opts.force) sp.set('force', '1')
    if (opts.purge) sp.set('purge', '1')
    return request<{ ok: boolean }>(`/api/addons/${id}?${sp.toString()}`, { method: 'DELETE' })
  },
  addonAction: (id: string, action: 'start' | 'stop' | 'restart') =>
    request<{ ok: boolean; addon: Addon }>(`/api/addons/${id}/${action}`, { method: 'POST' }),
  bindAddon: (id: string, projectId: string) =>
    request<{ addon: Addon }>(`/api/addons/${id}/bind`, {
      method: 'POST',
      body: JSON.stringify({ projectId }),
    }),
  unbindAddon: (id: string, projectId: string) =>
    request<{ addon: Addon }>(`/api/addons/${id}/bind/${projectId}`, { method: 'DELETE' }),
  addStandby: (id: string, serverId: string) =>
    request<{ addon: Addon }>(`/api/addons/${id}/standby`, {
      method: 'POST',
      body: JSON.stringify({ serverId }),
    }),
  promoteAddon: (id: string, serverId: string) =>
    request<{ addon: Addon }>(`/api/addons/${id}/promote`, {
      method: 'POST',
      body: JSON.stringify({ serverId }),
    }),
  removeAddonPlacement: (id: string, serverId: string) =>
    request<{ addon: Addon }>(`/api/addons/${id}/placement/${serverId}`, { method: 'DELETE' }),
  updateAddonPolicy: (
    id: string,
    body: {
      autoFailover?: boolean
      failoverAfterSec?: number
      maxLagMs?: number
      leaseSec?: number
    },
  ) => request<{ addon: Addon }>(`/api/addons/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  addonConnection: (id: string) => request<AddonConnection>(`/api/addons/${id}/connection`),
  addonLogs: (id: string, limit = 300) =>
    request<{ logs: AddonLog[] }>(`/api/addons/${id}/logs?limit=${limit}`),
  addonQuery: (id: string, input: string) =>
    request<ExecResult>(`/api/addons/${id}/query`, {
      method: 'POST',
      body: JSON.stringify({ input, timeoutSec: 60 }),
    }),
  addonBackups: (id: string) => request<{ backups: Backup[] }>(`/api/addons/${id}/backups`),
  createAddonBackup: (id: string) =>
    request<{ backup: Backup }>(`/api/addons/${id}/backup`, { method: 'POST' }),
  restoreAddonBackup: (id: string, backupId: string) =>
    request<{ ok: boolean }>(`/api/addons/${id}/restore`, {
      method: 'POST',
      body: JSON.stringify({ backupId }),
    }),

  serviceEngines: () => request<{ engines: EngineInfo[] }>('/api/services/engines'),
  services: () => request<{ services: Addon[] }>('/api/services'),
  service: (id: string) => request<{ service: Addon }>(`/api/services/${id}`),
  createService: (body: {
    name: string
    engine: string
    serverId: string
    sizeLimitMB?: number
    env?: Record<string, string>
  }) => request<{ service: Addon }>('/api/services', { method: 'POST', body: JSON.stringify(body) }),
  deleteService: (id: string, opts: { purge?: boolean } = {}) => {
    const sp = new URLSearchParams()
    if (opts.purge) sp.set('purge', '1')
    return request<{ ok: boolean }>(`/api/services/${id}?${sp.toString()}`, { method: 'DELETE' })
  },
  serviceAction: (id: string, action: 'start' | 'stop' | 'restart') =>
    request<{ ok: boolean; addon: Addon }>(`/api/services/${id}/${action}`, { method: 'POST' }),
  renameService: (id: string, name: string) =>
    request<{ service: Addon }>(`/api/services/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  serviceLogs: (id: string, limit = 300) =>
    request<{ logs: string; path?: string; size?: number; truncated?: boolean }>(
      `/api/services/${id}/logs?limit=${limit}`,
    ),
  serviceBackups: (id: string) => request<{ backups: Backup[] }>(`/api/services/${id}/backups`),
  createServiceBackup: (id: string) =>
    request<{ backup: Backup }>(`/api/services/${id}/backup`, { method: 'POST' }),
  restoreServiceBackup: (id: string, backupId: string) =>
    request<{ ok: boolean }>(`/api/services/${id}/restore`, {
      method: 'POST',
      body: JSON.stringify({ backupId }),
    }),

  alerts: (active = false) =>
    request<{ alerts: Alert[]; active: number }>(`/api/alerts?active=${active ? 1 : 0}&limit=200`),
  alertCount: () => request<{ count: number }>('/api/alerts/count'),
  ackAlert: (id: number) => request<{ ok: boolean }>(`/api/alerts/${id}/ack`, { method: 'POST' }),
  alertRules: () => request<AlertRules>('/api/alert-rules'),
  saveAlertRules: (rules: AlertRules) =>
    request<AlertRules>('/api/alert-rules', { method: 'PUT', body: JSON.stringify(rules) }),
  notifyConfig: () => request<NotifyConfig>('/api/notify'),
  saveNotifyConfig: (cfg: NotifyConfig) =>
    request<{ ok: boolean }>('/api/notify', { method: 'PUT', body: JSON.stringify(cfg) }),
  testNotify: () => request<{ ok: boolean }>('/api/notify/test', { method: 'POST' }),
}

export function terminalUrl(id: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/api/servers/${id}/terminal`
}

export function base64FromBytes(bytes: Uint8Array): string {
  let bin = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(bin)
}

export function bytesFromBase64(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function textFromBase64(b64: string): string {
  return new TextDecoder().decode(bytesFromBase64(b64))
}

export function textToBase64(text: string): string {
  return base64FromBytes(new TextEncoder().encode(text))
}
