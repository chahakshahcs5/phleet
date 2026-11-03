import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  ArrowLeft,
  BarChart3,
  Bell,
  Cloud,
  Copy,
  Database,
  Eye,
  EyeOff,
  FileText,
  GitBranch,
  HardDrive,
  Inbox,
  Play,
  Plus,
  RefreshCw,
  RotateCw,
  Save,
  Search,
  Server,
  ShieldAlert,
  Smartphone,
  Square,
  Terminal,
  Trash2,
  Zap,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../api'
import { ConfirmDialog, Dialog } from '../components/Dialog'
import { Badge, Button, Card, DeployBadge, KeyValue, Spinner, cn } from '../components/ui'
import { ago, bytes, time } from '../lib/format'

type Tab = 'overview' | 'console' | 'backups' | 'logs'

const label = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'
const input =
  'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40'

function kindIcon(kind: string, size = 14) {
  switch (kind) {
    case 'objectstore':
      return <Cloud size={size} />
    case 'cache':
      return <Zap size={size} />
    case 'file':
      return <HardDrive size={size} />
    case 'timeseries':
    case 'monitoring':
      return <Activity size={size} />
    case 'queue':
      return <Inbox size={size} />
    case 'search':
      return <Search size={size} />
    case 'logging':
      return <FileText size={size} />
    case 'analytics':
      return <BarChart3 size={size} />
    case 'notifications':
      return <Bell size={size} />
    case 'backend':
    case 'auth':
      return <ShieldAlert size={size} />
    case 'coordination':
      return <Server size={size} />
    case 'git':
      return <GitBranch size={size} />
    default:
      return <Database size={size} />
  }
}

function maskURL(u: string) {
  return u.replace(/(:\/\/[^:/@]+:)([^@]+)(@)/, (_m, a, b, c) => `${a}${'•'.repeat(Math.min(12, b.length))}${c}`)
}

export default function AddonDetail() {
  const { id = '' } = useParams()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [tab, setTab] = useState<Tab>('overview')
  const [reveal, setReveal] = useState(false)
  const [consoleInput, setConsoleInput] = useState('select 1;')
  const [consoleOut, setConsoleOut] = useState('')
  const [restoreId, setRestoreId] = useState('')
  const [promoteId, setPromoteId] = useState('')
  const [removePlacementId, setRemovePlacementId] = useState('')
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [purge, setPurge] = useState(false)
  const logRef = useRef<HTMLPreElement>(null)
  const follow = true

  const query = useQuery({
    queryKey: ['addon', id],
    queryFn: () => api.addon(id),
    enabled: !!id,
    refetchInterval: 5000,
  })
  const logs = useQuery({
    queryKey: ['addon-logs', id],
    queryFn: () => api.addonLogs(id, 300),
    enabled: !!id && tab === 'logs',
    refetchInterval: tab === 'logs' ? 3000 : false,
  })
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 })
  const [standbyServer, setStandbyServer] = useState('')
  const [autoPolicy, setAutoPolicy] = useState({ enabled: false, after: 120, lag: 30000 })

  const addon = query.data?.addon
  const placement = addon?.placements.find((p) => p.role === 'primary' || p.role === '') ?? addon?.placements[0]
  const standbys = addon?.placements.filter((p) => p.role === 'standby') ?? []
  const canReplicate = addon?.replicationMode === 'streaming' || addon?.replicationMode === 'async'
  const standbyCandidates = (servers.data?.servers ?? []).filter(
    (s) => s.online && s.id !== placement?.serverId && !addon?.placements.some((p) => p.serverId === s.id),
  )
  const online = placement?.online ?? false

  useEffect(() => {
    if (!addon) return
    setConsoleInput(addon.engine === 'redis' ? 'PING' : addon.engine === 'sqlite' ? '.tables' : 'select 1;')
    setAutoPolicy({
      enabled: addon.autoFailover,
      after: addon.failoverAfterSec,
      lag: addon.maxLagMs,
    })
    // Only reset when the addon changes, not on every poll.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [addon?.id])

  useEffect(() => {
    if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight
  }, [logs.dataUpdatedAt, follow])

  const action = useMutation({
    mutationFn: (a: 'start' | 'stop' | 'restart') => api.addonAction(id, a),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['addon', id] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })
  const backup = useMutation({
    mutationFn: () => api.createAddonBackup(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['addon', id] }),
  })
  const standby = useMutation({
    mutationFn: (serverId: string) => api.addStandby(id, serverId),
    onSuccess: () => {
      setStandbyServer('')
      qc.invalidateQueries({ queryKey: ['addon', id] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })
  const promote = useMutation({
    mutationFn: (serverId: string) => api.promoteAddon(id, serverId),
    onSuccess: () => {
      setPromoteId('')
      qc.invalidateQueries({ queryKey: ['addon', id] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })
  const removePlacement = useMutation({
    mutationFn: (serverId: string) => api.removeAddonPlacement(id, serverId),
    onSuccess: () => {
      setRemovePlacementId('')
      qc.invalidateQueries({ queryKey: ['addon', id] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })
  const savePolicy = useMutation({
    mutationFn: () =>
      api.updateAddonPolicy(id, {
        autoFailover: autoPolicy.enabled,
        failoverAfterSec: autoPolicy.after,
        maxLagMs: autoPolicy.lag,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['addon', id] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })
  const restore = useMutation({
    mutationFn: (backupId: string) => api.restoreAddonBackup(id, backupId),
    onSuccess: () => {
      setRestoreId('')
      qc.invalidateQueries({ queryKey: ['addon', id] })
    },
  })
  const verify = useMutation({
    mutationFn: (backupId: string) => api.verifyAddonBackup(id, backupId),
  })
  const run = useMutation({
    mutationFn: () => api.addonQuery(id, consoleInput),
    onSuccess: (res) => {
      setConsoleOut((prev) => `${prev}${prev ? '\n' : ''}$ ${consoleInput}\n${res.output}${res.exitCode ? `\n[exit ${res.exitCode}]` : ''}`)
    },
    onError: (e) => setConsoleOut((prev) => `${prev}\n[error] ${String(e)}\n`),
  })
  const remove = useMutation({
    mutationFn: () => api.deleteAddon(id, { force: true, purge }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['addons'] })
      qc.invalidateQueries({ queryKey: ['projects'] })
      navigate('/addons')
    },
  })

  if (query.isLoading) return <div className="p-8 text-xs text-zinc-500">loading addon…</div>
  if (query.isError || !addon) {
    return (
      <div className="p-8">
        <Link to="/addons" className="text-xs text-zinc-500 hover:text-zinc-300">
          ← addons
        </Link>
        <div className="mt-4 text-sm text-red-400">{String((query.error as Error)?.message || 'not found')}</div>
      </div>
    )
  }

  const url = addon.url || ''
  const backups = addon.backups ?? []
  const lastBackup = backups[0]
  const busy = action.isPending

  return (
    <div className="flex min-h-full flex-col">
      <div className="border-b border-zinc-800/70 bg-zinc-950/60 px-4 pt-4 md:px-8">
        <div className="flex items-center gap-1 text-[11px] text-zinc-500">
          <Link to="/addons" className="inline-flex items-center gap-1 hover:text-zinc-300">
            <ArrowLeft size={12} /> addons
          </Link>
        </div>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="truncate text-lg font-semibold tracking-tight text-zinc-100">{addon.name}</h1>
              <Badge tone="info">{addon.engineName || addon.engine}</Badge>
              {addon.autoFailover ? <Badge tone="info">auto-failover</Badge> : null}
              <DeployBadge status={addon.status} />
              {!online ? <Badge tone="bad">phone offline</Badge> : null}
              {addon.installed ? null : <Badge tone="warn">engine not installed</Badge>}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-zinc-500">
              <span className="font-mono">
                {placement?.serverName || addon.pinnedServer} · {addon.embedded ? 'file' : `port ${addon.port}`}
              </span>
              <span>{addon.bindings.length ? `${addon.bindings.length} project(s) attached` : 'standalone'}</span>
              <span>created {time(addon.createdAt)}</span>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button size="sm" disabled={!online || busy} onClick={() => action.mutate('restart')}>
              <RotateCw size={12} /> Restart
            </Button>
            <Button size="sm" disabled={!online || busy || addon.status === 'stopped'} onClick={() => action.mutate('stop')}>
              <Square size={11} /> Stop
            </Button>
            <Button size="sm" disabled={!online || busy || addon.status !== 'stopped'} onClick={() => action.mutate('start')}>
              <Play size={12} /> Start
            </Button>
            {addon.noBackup ? null : (
              <Button size="sm" variant="primary" disabled={!online || backup.isPending} onClick={() => backup.mutate()}>
                {backup.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
                Backup now
              </Button>
            )}
          </div>
        </div>
        {addon.lastError ? (
          <div className="mt-2 rounded-lg border border-red-900/30 bg-red-500/5 px-3 py-2 text-[10px] text-red-300">
            {addon.lastError}
          </div>
        ) : null}
        {action.isError ? <div className="mt-2 text-[11px] text-red-400">{String(action.error)}</div> : null}
        {backup.isError ? <div className="mt-2 text-[11px] text-red-400">{String(backup.error)}</div> : null}

        <div className="mt-4 flex gap-1 overflow-x-auto">
          {(
            [
              { id: 'overview', label: 'Overview', icon: kindIcon(addon.kind) },
              { id: 'console', label: 'Console', icon: <Terminal size={14} /> },
              { id: 'backups', label: 'Backups', icon: <HardDrive size={14} /> },
              { id: 'logs', label: 'Logs', icon: <RefreshCw size={14} /> },
            ] as { id: Tab; label: string; icon: React.ReactNode }[]
          )
            .filter((t) => t.id !== 'console' || addon.console)
            .filter((t) => t.id !== 'backups' || !addon.noBackup)
            .map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={cn(
                'flex items-center gap-1.5 border-b-2 px-3 py-2 text-xs whitespace-nowrap transition-colors',
                tab === t.id ? 'border-emerald-400 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300',
              )}
            >
              {t.icon}
              {t.label}
            </button>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 px-4 py-5 md:px-8">
        {tab === 'overview' ? (
          <div className="grid gap-4 lg:grid-cols-2">
            <Card className="p-4">
              <div className={label}>Connection</div>
              <p className="mt-1 text-[10px] text-zinc-600">
                Private to the fleet. Apps on {placement?.serverName || 'the database phone'} use loopback; apps on other
                phones use a relay facade.
              </p>
              {addon.s3Endpoint ? (
                <div className="mt-3">
                  <div className="text-[10px] text-zinc-500">S3 endpoint (published through the tunnel)</div>
                  <div className="mt-1 flex items-center gap-2">
                    <code className="min-w-0 flex-1 truncate rounded-lg bg-zinc-950 px-3 py-2 font-mono text-[11px] text-emerald-200/90 ring-1 ring-zinc-800">
                      {addon.s3Endpoint}
                    </code>
                    <Button
                      size="icon"
                      variant="ghost"
                      title="Copy"
                      onClick={() => addon.s3Endpoint && navigator.clipboard?.writeText(addon.s3Endpoint)}
                    >
                      <Copy size={13} />
                    </Button>
                  </div>
                </div>
              ) : null}
              <div className="mt-3">
                <div className="text-[10px] text-zinc-500">
                  {addon.kind === 'objectstore' ? 'S3 endpoint (from the phone)' : 'Loopback URL'}
                </div>
                <div className="mt-1 flex items-center gap-2">
                  <code className="min-w-0 flex-1 truncate rounded-lg bg-zinc-950 px-3 py-2 font-mono text-[11px] text-emerald-200/90 ring-1 ring-zinc-800">
                    {url ? (reveal ? url : maskURL(url)) : 'not provisioned yet'}
                  </code>
                  <Button size="icon" variant="ghost" title={reveal ? 'Hide' : 'Reveal'} onClick={() => setReveal((v) => !v)} disabled={!url}>
                    {reveal ? <EyeOff size={13} /> : <Eye size={13} />}
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    title="Copy"
                    disabled={!url}
                    onClick={() => url && navigator.clipboard?.writeText(url)}
                  >
                    <Copy size={13} />
                  </Button>
                </div>
              </div>
              <div className="mt-4 divide-y divide-zinc-800/60">
                <KeyValue label="Phone">{placement?.serverName || addon.pinnedServer || '—'}</KeyValue>
                <KeyValue label="Protocol port">{addon.embedded ? '—' : addon.port}</KeyValue>
                <KeyValue label="Bridge on this PC">
                  {addon.bridgePort
                    ? addon.bridgeActive
                      ? `127.0.0.1:${addon.bridgePort}`
                      : `port ${addon.bridgePort} (starting…)`
                    : '—'}
                </KeyValue>
                <KeyValue label="Data directory">
                  <span className="font-mono text-[10px]">{addon.dataDir || '—'}</span>
                </KeyValue>
                <KeyValue label="Size hint">{addon.sizeLimitMB ? `${addon.sizeLimitMB} MB (advisory)` : 'none'}</KeyValue>
                <KeyValue label="Replication">
                  {addon.replicationMode === 'none' ? 'none — single copy' : addon.replicationMode}
                </KeyValue>
                {addon.noBackup ? null : (
                  <KeyValue label="Last backup">{lastBackup ? `${ago(lastBackup.createdAt)} · ${bytes(lastBackup.size)}` : 'never'}</KeyValue>
                )}
              </div>
            </Card>

            <Card className="p-4">
              <div className={label}>Injected environment</div>
              <p className="mt-1 text-[10px] text-zinc-600">
                Added to every deploy of the attached projects. The URL and credentials are injected automatically.
              </p>
              <div className="mt-3 space-y-1.5">
                {Object.entries(addon.env ?? {}).map(([k, v]) => (
                  <div key={k} className="rounded-lg bg-zinc-900/60 px-2.5 py-1.5">
                    <div className="font-mono text-[10px] text-zinc-400">{k}</div>
                    <div className="truncate font-mono text-[10px] text-zinc-600">{reveal ? v : maskURL(v)}</div>
                  </div>
                ))}
                {Object.keys(addon.env ?? {}).length === 0 ? (
                  <p className="text-[11px] text-zinc-600">Unavailable until the database is provisioned.</p>
                ) : null}
              </div>
            </Card>

            <Card className="p-4 lg:col-span-2">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-1.5 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
                  <Database size={11} /> Replication
                </div>
                <span className="text-[10px] text-zinc-600">
                  epoch {addon.epoch} · {canReplicate ? 'manual promotion only' : 'no standby'}
                </span>
              </div>
              <div className="mt-3 overflow-x-auto">
                <table className="w-full text-left text-[11px]">
                  <thead className="bg-zinc-950/40 text-[9px] tracking-wider text-zinc-500 uppercase">
                    <tr>
                      <th className="px-3 py-2 font-medium">phone</th>
                      <th className="px-3 py-2 font-medium">role</th>
                      <th className="px-3 py-2 font-medium">state</th>
                      <th className="px-3 py-2 font-medium">port</th>
                      <th className="px-3 py-2 font-medium">lag</th>
                      <th className="px-3 py-2 font-medium"></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-zinc-800/50">
                    {addon.placements.map((pl) => (
                      <tr key={pl.serverId} className="hover:bg-zinc-800/20">
                        <td className="px-3 py-2 text-zinc-300">
                          <span className="flex items-center gap-1.5">
                            <Smartphone size={10} className="text-zinc-500" />
                            {pl.serverName || pl.serverId.slice(0, 8)}
                          </span>
                        </td>
                        <td className="px-3 py-2">
                          <Badge tone={pl.role === 'standby' ? 'info' : pl.role === 'demoted' ? 'warn' : 'neutral'}>
                            {pl.role || 'primary'}
                          </Badge>
                        </td>
                        <td className="px-3 py-2">
                          <DeployBadge status={pl.status} />
                        </td>
                        <td className="px-3 py-2 font-mono text-zinc-500">{pl.port || '—'}</td>
                        <td className="px-3 py-2 font-mono text-zinc-500">
                          {pl.role === 'standby' && pl.lagMs >= 0 ? `${pl.lagMs} ms` : '—'}
                        </td>
                        <td className="px-3 py-2 text-right">
                          <div className="flex justify-end gap-1">
                            {pl.role === 'standby' && pl.status === 'running' ? (
                              <Button size="sm" variant="ghost" disabled={promote.isPending} onClick={() => setPromoteId(pl.serverId)}>
                                Promote
                              </Button>
                            ) : null}
                            {pl.role !== 'primary' && pl.role !== '' ? (
                              <Button
                                size="sm"
                                variant="ghost"
                                disabled={removePlacement.isPending}
                                onClick={() => setRemovePlacementId(pl.serverId)}
                              >
                                Remove
                              </Button>
                            ) : null}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {canReplicate ? (
                <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-zinc-800/70 pt-3">
                  <select
                    className="h-8 min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950/70 px-2 text-[11px] text-zinc-300 outline-none"
                    value={standbyServer}
                    onChange={(e) => setStandbyServer(e.target.value)}
                  >
                    <option value="">Add a standby on…</option>
                    {standbyCandidates.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name}
                      </option>
                    ))}
                  </select>
                  <Button
                    size="sm"
                    disabled={!standbyServer || standby.isPending}
                    onClick={() => standbyServer && standby.mutate(standbyServer)}
                  >
                    {standby.isPending ? <Spinner className="h-3 w-3" /> : <Plus size={12} />} Add standby
                  </Button>
                </div>
              ) : null}
              {addon.relays?.length ? (
                <div className="mt-3 border-t border-zinc-800/70 pt-3">
                  <div className="text-[10px] text-zinc-500">Relay facades (apps on other phones)</div>
                  <div className="mt-1 flex flex-wrap gap-1.5">
                    {addon.relays.map((r) => (
                      <span key={r.serverId} className="rounded-md bg-zinc-800/70 px-2 py-1 font-mono text-[10px] text-zinc-400">
                        {r.serverName || r.serverId.slice(0, 8)}:127.0.0.1:{r.port}
                        {r.online ? '' : ' · offline'}
                      </span>
                    ))}
                  </div>
                </div>
              ) : null}
              {canReplicate ? (
                <div className="mt-3 border-t border-zinc-800/70 pt-3">
                  <label className="flex cursor-pointer items-start gap-2">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={autoPolicy.enabled}
                      onChange={(e) => setAutoPolicy({ ...autoPolicy, enabled: e.target.checked })}
                    />
                    <span className="text-[11px] text-zinc-300">
                      Automatic failover
                      <span className="mt-0.5 block text-[10px] leading-relaxed text-zinc-600">
                        The control plane is the witness. If the panel is unreachable for the lease, the primary stops
                        itself, then a running standby within the lag bound is promoted. A panel outage becomes a
                        database outage for this addon, so this is opt-in.
                      </span>
                    </span>
                  </label>
                  {autoPolicy.enabled ? (
                    <div className="mt-2 grid grid-cols-2 gap-2">
                      <div>
                        <div className={label}>After (seconds)</div>
                        <input
                          type="number"
                          min={0}
                          max={3600}
                          className={`${input} mt-1`}
                          value={autoPolicy.after}
                          onChange={(e) =>
                            setAutoPolicy({ ...autoPolicy, after: Math.max(0, Number(e.target.value) || 0) })
                          }
                        />
                      </div>
                      <div>
                        <div className={label}>Max lag (ms, 0 = ignore)</div>
                        <input
                          type="number"
                          min={0}
                          max={600000}
                          className={`${input} mt-1`}
                          value={autoPolicy.lag}
                          onChange={(e) =>
                            setAutoPolicy({ ...autoPolicy, lag: Math.max(0, Number(e.target.value) || 0) })
                          }
                        />
                      </div>
                    </div>
                  ) : null}
                  <div className="mt-2 flex items-center justify-end gap-2">
                    {savePolicy.isError ? (
                      <span className="text-[10px] text-red-400">{String(savePolicy.error)}</span>
                    ) : null}
                    <Button size="sm" disabled={savePolicy.isPending} onClick={() => savePolicy.mutate()}>
                      {savePolicy.isPending ? <Spinner className="h-3 w-3" /> : null} Save policy
                    </Button>
                  </div>
                </div>
              ) : null}
              {standby.isError ? <div className="mt-2 text-[10px] text-red-400">{String(standby.error)}</div> : null}
              {promote.isError ? <div className="mt-2 text-[10px] text-red-400">{String(promote.error)}</div> : null}
            </Card>

            <Card className="p-4 lg:col-span-2">
              <div className="flex items-center justify-between">
                <div className={label}>Attached projects</div>
                <span className="text-[10px] text-zinc-600">apps on other phones use a loopback relay</span>
              </div>
              <div className="mt-3 flex flex-wrap gap-1.5">
                {addon.bindings.map((b) => {
                  const p = projects.data?.projects.find((x) => x.id === b.projectId)
                  return (
                    <Link
                      key={b.projectId}
                      to={`/projects/${b.projectId}`}
                      className="rounded-md bg-zinc-800/70 px-2 py-1 text-[10px] text-zinc-300 hover:text-emerald-300"
                    >
                      {p?.name || b.projectId.slice(0, 8)}
                    </Link>
                  )
                })}
                {addon.bindings.length === 0 ? (
                  <span className="text-[11px] text-zinc-600">
                    Nothing attached. Open a project and attach this database, or create one from the project wizard.
                  </span>
                ) : null}
              </div>
            </Card>

            <Card className="border-red-900/40 p-4 lg:col-span-2">
              <div className="text-[10px] font-medium tracking-widest text-red-400/80 uppercase">Danger zone</div>
              <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">
                Removing the addon stops the process and forgets it. Data on the phone is kept unless you purge it.
              </p>
              <Button variant="danger" size="sm" className="mt-3" onClick={() => setDeleteOpen(true)}>
                <Trash2 size={12} /> Remove database
              </Button>
            </Card>
          </div>
        ) : null}

        {tab === 'console' ? (
          <Card className="flex h-[calc(100vh-300px)] min-h-[380px] flex-col overflow-hidden">
            <div className="flex items-center justify-between border-b border-zinc-800/70 px-3 py-2">
              <div className="flex items-center gap-2 text-[11px] text-zinc-400">
                <Terminal size={12} className="text-zinc-500" />
                {addon.engine === 'redis'
                  ? 'redis-cli'
                  : addon.engine === 'sqlite'
                    ? 'sqlite3'
                    : addon.engine === 'duckdb'
                      ? 'duckdb'
                      : 'psql'}
                <Badge>{online ? 'loopback' : 'offline'}</Badge>
              </div>
              <div className="flex items-center gap-1">
                <Button size="icon" variant="ghost" title="Clear" onClick={() => setConsoleOut('')}>
                  <Trash2 size={12} />
                </Button>
              </div>
            </div>
            <pre className="min-h-0 flex-1 overflow-auto bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300">
              {consoleOut || 'run a statement below — output appears here'}
            </pre>
            <div className="border-t border-zinc-800/70 p-2">
              <textarea
                className="min-h-20 w-full resize-y rounded-lg border border-zinc-800 bg-zinc-950/70 p-3 font-mono text-[11px] text-zinc-200 outline-none focus:border-emerald-400/40"
                value={consoleInput}
                onChange={(e) => setConsoleInput(e.target.value)}
                spellCheck={false}
              />
              <div className="mt-2 flex items-center justify-between">
                <span className="text-[10px] text-zinc-600">One-shot: the statement is piped to the engine client on the phone.</span>
                <Button size="sm" variant="primary" disabled={!online || run.isPending || !consoleInput.trim()} onClick={() => run.mutate()}>
                  {run.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Play size={12} />} Run
                </Button>
              </div>
            </div>
          </Card>
        ) : null}

        {tab === 'backups' ? (
          <Card className="overflow-hidden">
            <div className="flex items-center justify-between border-b border-zinc-800/70 px-4 py-2.5">
              <div className={label}>Backups</div>
              <span className="text-[10px] text-zinc-600">stored on the control plane · last 10 kept</span>
            </div>
            {backups.length === 0 ? (
              <div className="px-4 py-8 text-center text-[11px] text-zinc-600">
                No backups yet. A standby copy is not a backup — take one before you need it.
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-[11px]">
                  <thead className="bg-zinc-950/40 text-[9px] tracking-wider text-zinc-500 uppercase">
                    <tr>
                      <th className="px-4 py-2 font-medium">created</th>
                      <th className="px-3 py-2 font-medium">status</th>
                      <th className="px-3 py-2 font-medium">size</th>
                      <th className="px-3 py-2 font-medium">id</th>
                      <th className="px-4 py-2 font-medium"></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-zinc-800/50">
                    {backups.map((b) => (
                      <tr key={b.id} className="hover:bg-zinc-800/20">
                        <td className="px-4 py-2 text-zinc-400">{time(b.createdAt)}</td>
                        <td className="px-3 py-2">
                          <DeployBadge status={b.status === 'complete' ? 'active' : b.status} />
                        </td>
                        <td className="px-3 py-2 text-zinc-500">{bytes(b.size)}</td>
                        <td className="px-3 py-2 font-mono text-[10px] text-zinc-600">{b.id.slice(0, 8)}</td>
                        <td className="px-4 py-2 text-right">
                          <div className="flex justify-end gap-1">
                            <a
                              href={`/api/addons/${id}/backups/${b.id}/download`}
                              className="rounded-md px-2 py-1 text-[10px] text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100"
                            >
                              download
                            </a>
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={b.status !== 'complete' || !online || verify.isPending}
                              onClick={() => verify.mutate(b.id)}
                            >
                              Verify
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              disabled={b.status !== 'complete' || !online || restore.isPending}
                              onClick={() => setRestoreId(b.id)}
                            >
                              Restore
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {verify.isSuccess ? <div className="px-4 pb-3 text-[11px] text-emerald-300">Backup verified.</div> : null}
            {verify.isError ? <div className="px-4 pb-3 text-[11px] text-red-400">{String(verify.error)}</div> : null}
            {restore.isError ? <div className="px-4 pb-3 text-[11px] text-red-400">{String(restore.error)}</div> : null}
          </Card>
        ) : null}

        {tab === 'logs' ? (
          <Card className="flex h-[calc(100vh-300px)] min-h-[380px] flex-col overflow-hidden">
            <div className="flex items-center justify-between border-b border-zinc-800/70 px-3 py-2">
              <div className="flex items-center gap-2 text-[11px] text-zinc-400">
                <RefreshCw size={12} className="text-zinc-500" /> provisioning &amp; maintenance log
              </div>
              <Button size="icon" variant="ghost" onClick={() => logs.refetch()}>
                <RefreshCw size={12} />
              </Button>
            </div>
            <pre ref={logRef} className="min-h-0 flex-1 overflow-auto bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300">
              {logs.data?.logs.length
                ? logs.data.logs.map((l) => l.line).join('\n')
                : (logs.isError ? String(logs.error) : 'no output yet')}
            </pre>
          </Card>
        ) : null}
      </div>

      <ConfirmDialog
        open={!!restoreId}
        onClose={() => setRestoreId('')}
        title="Restore this backup?"
        description="The engine is stopped (or reloaded) while the dump is applied. The current data is replaced."
        confirmLabel="Restore"
        danger
        busy={restore.isPending}
        onConfirm={() => restoreId && restore.mutate(restoreId)}
      />

      <ConfirmDialog
        open={!!promoteId}
        onClose={() => setPromoteId('')}
        title="Promote this standby?"
        description="The old primary is stopped first (fenced). If it is offline it is marked demoted and stopped when it reconnects. Promotion is manual and does not wait for a quorum."
        confirmLabel="Promote to primary"
        danger
        busy={promote.isPending}
        onConfirm={() => promoteId && promote.mutate(promoteId)}
      />

      <ConfirmDialog
        open={!!removePlacementId}
        onClose={() => setRemovePlacementId('')}
        title="Remove this copy?"
        description="The process is stopped and its data directory on that phone is deleted. The primary and other copies are untouched. Re-add it later with Add standby (it clones again)."
        confirmLabel="Remove copy"
        danger
        busy={removePlacement.isPending}
        onConfirm={() => removePlacementId && removePlacement.mutate(removePlacementId)}
      />

      <Dialog
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        title={`Remove ${addon.name}?`}
        description="The process is stopped and the addon is removed from the panel."
        danger
        busy={remove.isPending}
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteOpen(false)} disabled={remove.isPending}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => remove.mutate()} disabled={remove.isPending}>
              {remove.isPending ? <Spinner className="h-3 w-3" /> : <Trash2 size={12} />} Remove
            </Button>
          </>
        }
      >
        <label className="flex items-start gap-2 rounded-lg border border-red-900/40 bg-red-500/5 p-3 text-[11px] text-red-200/80">
          <input type="checkbox" className="mt-0.5" checked={purge} onChange={(e) => setPurge(e.target.checked)} />
          <span>
            <span className="flex items-center gap-1 font-medium text-red-300">
              <ShieldAlert size={12} /> Also delete the data on the phone
            </span>
            This cannot be undone. Leave it unchecked to keep the volume for a manual restore.
          </span>
        </label>
      </Dialog>
    </div>
  )
}
