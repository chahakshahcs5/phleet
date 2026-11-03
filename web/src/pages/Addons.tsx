import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  BarChart3,
  Bell,
  Boxes,
  Cloud,
  Cpu,
  Database,
  FileText,
  GitBranch,
  HardDrive,
  Inbox,
  Package,
  Plus,
  Search,
  Server,
  Shield,
  Smartphone,
  Terminal,
  X,
  Zap,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../api'
import { Badge, Button, Card, DeployBadge, EmptyState, Spinner, cn } from '../components/ui'
import { ago, bytes } from '../lib/format'
import type { Addon, EngineInfo, Server as ServerType } from '../types'

const input =
  'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40'
const label = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

function engineIcon(kind: string, size = 13) {
  switch (kind) {
    case 'objectstore':
      return <Cloud size={size} />
    case 'cache':
      return <Zap size={size} />
    case 'file':
      return <HardDrive size={size} />
    case 'analytics':
      return <BarChart3 size={size} />
    case 'monitoring':
      return <Activity size={size} />
    case 'timeseries':
      return <BarChart3 size={size} />
    case 'auth':
      return <Shield size={size} />
    case 'database':
      return <Database size={size} />
    case 'queue':
      return <Inbox size={size} />
    case 'search':
      return <Search size={size} />
    case 'logging':
      return <FileText size={size} />
    case 'notifications':
      return <Bell size={size} />
    case 'backend':
      return <Shield size={size} />
    case 'coordination':
      return <Server size={size} />
    case 'git':
      return <GitBranch size={size} />
    default:
      // Addons are not only data services: an engine may be anything that runs
      // as a supervised service on a phone.
      return <Boxes size={size} />
  }
}

function kindLabel(kind: string) {
  switch (kind) {
    case 'database':
      return 'relational database'
    case 'cache':
      return 'cache / queue'
    case 'objectstore':
      return 'object storage'
    case 'file':
      return 'embedded file'
    case 'analytics':
      return 'analytics'
    case 'timeseries':
      return 'time series'
    case 'monitoring':
      return 'monitoring'
    case 'auth':
      return 'authentication'
    case 'queue':
      return 'queue / messaging'
    case 'search':
      return 'search'
    case 'logging':
      return 'logging'
    case 'notifications':
      return 'notifications'
    case 'backend':
      return 'backend / auth'
    case 'coordination':
      return 'coordination'
    case 'git':
      return 'git'
    default:
      return kind
  }
}

function installedCount(engine: EngineInfo, servers: ServerType[]): { count: number; known: boolean; online: number } {
  const online = servers.filter((s) => s.online)
  let count = 0
  let known = true
  for (const s of online) {
    const map = s.caps?.engines as Record<string, boolean> | undefined
    if (!map) {
      known = false
      continue
    }
    if (map[engine.id]) count++
  }
  return { count, known, online: online.length }
}

function AddonCard({ addon }: { addon: Addon }) {
  const placement = addon.placements.find((p) => p.role === 'primary' || p.role === '') ?? addon.placements[0]
  const backup = addon.backups?.[0]
  return (
    <Card className="group flex flex-col p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-1.5">
            <span className="text-zinc-500">{engineIcon(addon.kind)}</span>
            <Link to={`/addons/${addon.id}`} className="truncate text-sm font-medium text-zinc-100 hover:text-emerald-300">
              {addon.name}
            </Link>
            <Badge tone="neutral" className="shrink-0">
              {addon.engineName || addon.engine}
            </Badge>
          </div>
          <div className="mt-0.5 font-mono text-[10px] text-zinc-500">
            {addon.s3Endpoint ? addon.s3Endpoint.replace(/^https?:\/\//, '') : addon.embedded ? 'file' : `port ${addon.port}`}
          </div>
        </div>
        <DeployBadge status={addon.status} />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2 text-[10px] text-zinc-500">
        <span className="flex items-center gap-1 truncate" title={placement?.serverName || addon.pinnedServer}>
          <Smartphone size={10} /> {placement?.serverName || addon.pinnedServer || 'unplaced'}
        </span>
        <span className="flex items-center gap-1 truncate">
          <Server size={10} /> {addon.bindings.length ? `${addon.bindings.length} attached` : 'standalone'}
        </span>
        <span className="col-span-2 truncate" title={backup ? `${backup.status} ${bytes(backup.size)}` : ''}>
          backup {backup ? `${ago(backup.createdAt)} · ${bytes(backup.size)}` : 'never'}
        </span>
      </div>

      <div className="mt-3 flex items-center justify-between border-t border-zinc-800/70 pt-2.5">
        <Link to={`/addons/${addon.id}`} className="text-[10px] text-zinc-500 hover:text-zinc-200">
          manage →
        </Link>
        {addon.lastError ? (
          <span className="max-w-[60%] truncate text-[10px] text-red-300/80" title={addon.lastError}>
            {addon.lastError}
          </span>
        ) : null}
      </div>
    </Card>
  )
}

function EngineCard({
  engine,
  servers,
  onPick,
}: {
  engine: EngineInfo
  servers: ServerType[]
  onPick: (engine: EngineInfo) => void
}) {
  const { count, known, online } = installedCount(engine, servers)
  const installText =
    online === 0
      ? 'no phone online'
      : known
        ? count > 0
          ? `installed on ${count}/${online} phone${online === 1 ? '' : 's'}`
          : 'installs on first create'
        : 'install status unknown'
  return (
    <Card className="flex flex-col p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="grid h-8 w-8 place-items-center rounded-lg bg-emerald-400/10 text-emerald-300 ring-1 ring-emerald-400/20">
            {engineIcon(engine.kind, 15)}
          </span>
          <div>
            <div className="text-sm font-medium text-zinc-100">{engine.name}</div>
            <div className="text-[10px] tracking-wide text-zinc-500 uppercase">{kindLabel(engine.kind)}</div>
          </div>
        </div>
        <Badge tone={engine.embedded ? 'info' : 'neutral'}>{engine.embedded ? 'file' : `:${engine.defaultPort}`}</Badge>
      </div>
      <p className="mt-2 min-h-[2.5rem] text-[10px] leading-relaxed text-zinc-500">{engine.summary}</p>
      <div className="mt-2 flex flex-wrap gap-1">
        {engine.embedded ? (
          <Badge>no process</Badge>
        ) : (
          <Badge>
            <Cpu size={9} /> ~{engine.minRamMb} MB
          </Badge>
        )}
        {engine.console ? (
          <Badge tone="good">
            <Terminal size={9} /> console
          </Badge>
        ) : null}
        {engine.replication !== 'none' ? <Badge tone="info">{engine.replication}</Badge> : <Badge>no standby</Badge>}
      </div>
      <div className="mt-3 flex items-center justify-between gap-2 border-t border-zinc-800/70 pt-2.5">
        <span className="flex items-center gap-1 truncate text-[10px] text-zinc-500" title={installText}>
          <Package size={10} className="shrink-0" /> {installText}
        </span>
        <Button size="sm" variant="primary" disabled={online === 0} onClick={() => onPick(engine)}>
          <Plus size={12} /> Create
        </Button>
      </div>
    </Card>
  )
}

function NewAddon({
  onClose,
  initialEngine,
  initialProject,
}: {
  onClose: () => void
  initialEngine?: string
  initialProject?: string
}) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const engines = useQuery({ queryKey: ['addon-engines'], queryFn: api.addonEngines })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers })
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects })
  const [name, setName] = useState('')
  const [engine, setEngine] = useState(initialEngine || 'postgres')
  const [mode, setMode] = useState<'standalone' | 'project'>(initialProject ? 'project' : 'standalone')
  const [serverId, setServerId] = useState('')
  const [projectId, setProjectId] = useState(initialProject || '')
  const [sizeLimitMB, setSizeLimitMB] = useState(1024)

  const selected = engines.data?.engines.find((e) => e.id === engine)
  const online = servers.data?.servers.filter((s) => s.online) ?? []
  const project = projects.data?.projects.find((p) => p.id === projectId)
  const pickedServer = online.find((s) => s.id === serverId)
  const installedMap = pickedServer?.caps?.engines as Record<string, boolean> | undefined
  const willInstall = pickedServer && installedMap ? installedMap[engine] === false : false
  // A phone without Termux/userland can only host an engine whose binaries are
  // already there; the agent has no package manager to install one.
  const platformOk = (s: ServerType) => {
    if (!selected?.platforms?.length) return true
    const manager = s.caps?.pkgManager ?? ''
    return selected.platforms.includes(manager) || (selected.platforms.includes('static') && selected.customInstall)
  }
  const canInstallOn = (s: ServerType) =>
    platformOk(s) &&
    (s.caps?.engines?.[engine] === true || selected?.customInstall === true || s.caps?.pkgInstall !== false)
  const ready =
    !!name.trim() &&
    !!serverId &&
    !!selected &&
    (!pickedServer || canInstallOn(pickedServer)) &&
    (mode === 'standalone' || !!projectId)

  useEffect(() => {
    if (mode !== 'project') return
    const p = projects.data?.projects.find((x) => x.id === projectId)
    const target = p?.pinnedServer || p?.placements?.[0]?.serverId
    if (target && target !== serverId) setServerId(target)
  }, [mode, projectId, projects.data])

  const create = useMutation({
    mutationFn: () =>
      api.createAddon({
        name: name.trim(),
        engine,
        serverId,
        projectId: mode === 'project' ? projectId : undefined,
        sizeLimitMB,
      }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['addons'] })
      qc.invalidateQueries({ queryKey: ['projects'] })
      navigate(`/addons/${res.addon.id}`)
    },
  })

  return (
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-black/70 p-4 backdrop-blur-sm"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (ready) create.mutate()
        }}
        className="max-h-[90vh] w-full max-w-xl overflow-y-auto rounded-xl border border-zinc-800 bg-zinc-950 p-5 shadow-2xl"
      >
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-sm font-semibold text-zinc-100">New addon</h2>
            <p className="mt-0.5 text-[11px] text-zinc-500">
              A supporting service pinned to one phone. phleet installs the engine at create time; nothing is preinstalled.
            </p>
          </div>
          <button type="button" onClick={onClose} className="text-zinc-500 hover:text-zinc-200">
            <X size={14} />
          </button>
        </div>

        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <div className={label}>Name</div>
            <input
              className={`${input} mt-1`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="App database"
              autoFocus
            />
          </div>
          <div>
            <div className={label}>Engine</div>
            <select className={`${input} mt-1`} value={engine} onChange={(e) => setEngine(e.target.value)}>
              {engines.data?.engines.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <div className={label}>Phone</div>
            <select className={`${input} mt-1`} value={serverId} onChange={(e) => setServerId(e.target.value)}>
              <option value="">Select a phone — required</option>
              {online.map((s) => (
                <option key={s.id} value={s.id} disabled={!canInstallOn(s)}>
                  {s.name}
                  {canInstallOn(s) ? '' : ' — monitoring only'}
                </option>
              ))}
            </select>
            {pickedServer && !canInstallOn(pickedServer) ? (
              <p className="mt-1 text-[10px] leading-relaxed text-amber-300/80">
                {selected?.platforms?.length && !platformOk(pickedServer)
                  ? `${selected.name} is only available on ${selected.platforms.join(' / ')} phones.`
                  : `${selected?.name ?? 'This engine'} is not installed on ${pickedServer.name}, and that phone has no Termux/package manager to install it. Install it there by hand or choose another phone.`}
              </p>
            ) : willInstall ? (
              <p className="mt-1 text-[10px] leading-relaxed text-amber-300/80">
                This engine is not installed yet; the first create runs the{' '}
                {selected?.customInstall ? 'static download' : 'package install'} and streams the log.
              </p>
            ) : null}
          </div>

          <div className="sm:col-span-2">
            <div className={label}>Scope</div>
            <div className="mt-1 grid grid-cols-2 gap-1.5">
              <button
                type="button"
                onClick={() => {
                  setMode('standalone')
                  setProjectId('')
                }}
                className={cn(
                  'h-8 rounded-lg border text-xs transition-colors',
                  mode === 'standalone'
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200',
                )}
              >
                Standalone addon
              </button>
              <button
                type="button"
                onClick={() => setMode('project')}
                className={cn(
                  'h-8 rounded-lg border text-xs transition-colors',
                  mode === 'project'
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200',
                )}
              >
                For a project
              </button>
            </div>
            <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
              {mode === 'standalone'
                ? 'A standalone service you can attach to one or more projects later.'
                : 'Created for one project and attached automatically; it can still be shared afterwards.'}
            </p>
          </div>

          {mode === 'project' ? (
            <div className="sm:col-span-2">
              <div className={label}>Project</div>
              <select className={`${input} mt-1`} value={projectId} onChange={(e) => setProjectId(e.target.value)}>
                <option value="">Select a project</option>
                {projects.data?.projects
                  .filter((p) => !serverId || !p.pinnedServer || p.pinnedServer === serverId)
                  .map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                      {p.pinnedServer ? '' : ' · not pinned'}
                    </option>
                  ))}
              </select>
              <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
                {project?.pinnedServer
                  ? 'The project is pinned, so the addon uses that phone. Its connection variables are injected on deploy.'
                  : 'The addon is pinned to the phone you selected. Apps on other phones reach it through a loopback relay.'}
              </p>
            </div>
          ) : (
            <div className="sm:col-span-2 rounded-lg border border-zinc-800/80 bg-zinc-900/30 p-3 text-[10px] leading-relaxed text-zinc-500">
              Standalone is the right choice when several projects share one database, or when you want to create the
              service before the app. Attach it from <span className="text-zinc-300">Project → Overview → Addons</span>.
            </div>
          )}

          <div>
            <div className={label}>Size hint (MB)</div>
            <input
              type="number"
              min={0}
              className={`${input} mt-1`}
              value={sizeLimitMB}
              onChange={(e) => setSizeLimitMB(Math.max(0, Number(e.target.value) || 0))}
            />
            <p className="mt-1 text-[10px] text-zinc-600">Stored for planning; not enforced yet.</p>
          </div>
        </div>

        {selected ? <EngineNote engine={selected} /> : null}
        {create.isError ? <div className="mt-3 text-[11px] text-red-400">{String(create.error)}</div> : null}

        <div className="mt-5 flex items-center justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={!ready || create.isPending}>
            {create.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Plus size={13} />}
            Create addon
          </Button>
        </div>
      </form>
    </div>
  )
}

function EngineNote({ engine }: { engine: EngineInfo }) {
  const repl = engine.noBackup
    ? 'In-memory only. Nothing to back up; data is lost when the process restarts.'
    : engine.id === 'rclone'
      ? 'S3 over HTTP on its own hostname, backed by a directory. No standby; back up the bucket.'
      : engine.replication === 'none'
        ? 'Single copy. Back it up — this engine has no standby.'
        : `${engine.replication} replication is available with an explicit standby and manual promotion.`
  return (
    <div className="mt-3 rounded-lg border border-zinc-800/80 bg-zinc-900/40 p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs text-zinc-300">{engine.name}</span>
        <Badge tone={engine.embedded ? 'info' : 'neutral'}>{engine.embedded ? 'no process' : `~${engine.minRamMb} MB RAM`}</Badge>
      </div>
      <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">{engine.summary}</p>
      <p className="mt-1 text-[10px] leading-relaxed text-amber-300/80">{repl}</p>
    </div>
  )
}

const kinds = [
  { id: 'all', label: 'All' },
  { id: 'database', label: 'Databases' },
  { id: 'cache', label: 'Cache' },
  { id: 'queue', label: 'Messaging' },
  { id: 'timeseries', label: 'Time series' },
  { id: 'logging', label: 'Logging' },
  { id: 'monitoring', label: 'Monitoring' },
  { id: 'notifications', label: 'Notifications' },
  { id: 'search', label: 'Search' },
  { id: 'objectstore', label: 'Object storage' },
  { id: 'file', label: 'Files' },
  { id: 'analytics', label: 'Analytics' },
  { id: 'backend', label: 'Backend' },
  { id: 'coordination', label: 'Coordination' },
  { id: 'git', label: 'Git' },
]

export default function Addons() {
  const [params] = useSearchParams()
  const [tab, setTab] = useState<'instances' | 'explore'>('instances')
  const [creating, setCreating] = useState(false)
  const [createEngine, setCreateEngine] = useState<string | undefined>()
  const [createProject, setCreateProject] = useState<string | undefined>()
  const [query, setQuery] = useState('')
  const [kind, setKind] = useState('all')

  const addons = useQuery({ queryKey: ['addons'], queryFn: api.addons, refetchInterval: 6000 })
  const engines = useQuery({ queryKey: ['addon-engines'], queryFn: api.addonEngines })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 })
  const list = addons.data?.addons ?? []
  const online = servers.data?.servers.filter((s) => s.online) ?? []

  useEffect(() => {
    if (params.get('new') !== '1' && !params.get('project')) return
    setCreateProject(params.get('project') || undefined)
    setCreateEngine(params.get('engine') || undefined)
    setCreating(true)
    // Only react to the URL, not to modal state changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params.toString()])

  const openCreate = (engine?: string, project?: string) => {
    setCreateEngine(engine)
    setCreateProject(project)
    setCreating(true)
  }

  const catalog = (engines.data?.engines ?? []).filter((e) => {
    if (kind !== 'all' && e.kind !== kind) return false
    const q = query.trim().toLowerCase()
    if (!q) return true
    return e.name.toLowerCase().includes(q) || e.summary.toLowerCase().includes(q) || e.id.includes(q)
  })

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Addons</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            {list.length === 0
              ? `${online.length} phone${online.length === 1 ? '' : 's'} ready · nothing deployed yet`
              : `${list.length} addon${list.length === 1 ? '' : 's'} on ${online.length} online phone${online.length === 1 ? '' : 's'}`}
          </p>
        </div>
        <Button variant="primary" onClick={() => openCreate()}>
          <Plus size={14} /> New addon
        </Button>
      </div>

      <div className="mt-4 flex items-center gap-1 border-b border-zinc-800/70">
        {[
          { id: 'instances' as const, label: 'Instances', count: list.length },
          { id: 'explore' as const, label: 'Explore', count: engines.data?.engines.length ?? 0 },
        ].map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={cn(
              'flex items-center gap-1.5 border-b-2 px-3 py-2 text-xs whitespace-nowrap transition-colors',
              tab === t.id ? 'border-emerald-400 text-zinc-100' : 'border-transparent text-zinc-500 hover:text-zinc-300',
            )}
          >
            {t.label}
            <span className="rounded bg-zinc-800/80 px-1.5 py-0.5 text-[10px] text-zinc-500">{t.count}</span>
          </button>
        ))}
      </div>

      {tab === 'instances' ? (
        addons.isLoading ? (
          <div className="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-36 animate-pulse rounded-xl bg-zinc-900/40" />
            ))}
          </div>
        ) : list.length === 0 ? (
          <div className="mt-6">
            <EmptyState icon={<Boxes size={22} />} title="No addons yet">
              <p>
                An addon is any stateful or supporting service installed and supervised on a phone — a database, cache,
                object storage, analytics, monitoring, and more. Nothing is preinstalled: pick an engine and phleet
                installs it on first create.
              </p>
              <div className="mt-4 flex items-center justify-center gap-2">
                <Button variant="primary" size="sm" onClick={() => openCreate()}>
                  <Plus size={12} /> Create an addon
                </Button>
                <Button size="sm" onClick={() => setTab('explore')}>
                  Browse engines
                </Button>
              </div>
              <pre className="mt-4 overflow-x-auto rounded-lg bg-zinc-950 p-3 text-left font-mono text-[10px] leading-relaxed text-zinc-400 ring-1 ring-zinc-800">
{`standalone addon  → attach to one or more projects
project addon     → created with the project, injected on deploy
app               → DATABASE_URL=postgres://…@127.0.0.1:4100/app`}
              </pre>
            </EmptyState>
          </div>
        ) : (
          <div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {list.map((a) => (
              <AddonCard key={a.id} addon={a} />
            ))}
          </div>
        )
      ) : (
        <>
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <div className="relative min-w-0 flex-1">
              <Search size={13} className="pointer-events-none absolute top-2.5 left-2.5 text-zinc-600" />
              <input
                className={`${input} pl-8`}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search engines…"
              />
            </div>
            <div className="flex flex-wrap items-center gap-1">
              {kinds.map((k) => (
                <button
                  key={k.id}
                  onClick={() => setKind(k.id)}
                  className={cn(
                    'rounded-md px-2 py-1 text-[10px] transition-colors',
                    kind === k.id ? 'bg-emerald-400/10 text-emerald-300' : 'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-300',
                  )}
                >
                  {k.label}
                </button>
              ))}
            </div>
          </div>
          {engines.isLoading ? (
            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {[0, 1, 2].map((i) => (
                <div key={i} className="h-40 animate-pulse rounded-xl bg-zinc-900/40" />
              ))}
            </div>
          ) : catalog.length === 0 ? (
            <div className="mt-6">
              <EmptyState icon={<Package size={22} />} title="No engines match">
                <p>Clear the search or pick another category.</p>
              </EmptyState>
            </div>
          ) : (
            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {catalog.map((e) => (
                <EngineCard key={e.id} engine={e} servers={servers.data?.servers ?? []} onPick={(x) => openCreate(x.id)} />
              ))}
            </div>
          )}
          <p className="mt-4 text-[10px] leading-relaxed text-zinc-600">
            Engines are installed at create time with the phone's package manager (or a static rclone download). The
            bootstrap's <span className="font-mono">--with</span> flag only pre-warms a phone; it is not required.
          </p>
        </>
      )}

      <div className="mt-6 rounded-xl border border-zinc-800/80 bg-zinc-900/30 p-4 text-[10px] leading-relaxed text-zinc-500">
        <span className="text-zinc-400">How this works.</span> An addon is any supporting service installed and
        supervised on a phone: a database, cache, object storage, analytics, monitoring — the same idea as addons in a
        hosting platform. Each engine instance is bound to <span className="font-mono">127.0.0.1</span> on one phone. A
        standalone addon can be attached to several projects; a project addon is created and attached in one step. Apps
        on another phone reach it through a loopback relay, and the panel exposes a bridge on your PC. Backups are dumped
        on the phone and stored on the control plane. Replicas are explicit: add a standby, watch lag, promote by hand,
        and never re-create an empty database.
      </div>

      {creating ? (
        <NewAddon
          onClose={() => {
            setCreating(false)
            setCreateEngine(undefined)
            setCreateProject(undefined)
          }}
          initialEngine={createEngine}
          initialProject={createProject}
        />
      ) : null}
    </div>
  )
}
