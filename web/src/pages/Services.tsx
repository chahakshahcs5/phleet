import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Bell,
  Boxes,
  Clipboard,
  ExternalLink,
  FolderOpen,
  Music,
  NotebookPen,
  Package,
  Play,
  Plus,
  RotateCw,
  Rss,
  Search,
  Square,
  StickyNote,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api'
import { Dialog } from '../components/Dialog'
import { Badge, Button, Card, DeployBadge, EmptyState, Spinner, cn } from '../components/ui'
import type { Addon, EngineInfo, Server as ServerType } from '../types'

const input =
  'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40'
const label = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

function categoryIcon(category?: string, size = 14) {
  switch (category) {
    case 'files':
      return <FolderOpen size={size} />
    case 'notes':
      return <NotebookPen size={size} />
    case 'notifications':
      return <Bell size={size} />
    case 'utilities':
      return <Clipboard size={size} />
    case 'media':
      return <Music size={size} />
    case 'reading':
      return <Rss size={size} />
    case 'productivity':
      return <StickyNote size={size} />
    default:
      return <Boxes size={size} />
  }
}

function categoryLabel(category?: string) {
  switch (category) {
    case 'files':
      return 'files & sync'
    case 'notes':
      return 'notes'
    case 'notifications':
      return 'notifications'
    case 'utilities':
      return 'utilities'
    case 'media':
      return 'media'
    case 'reading':
      return 'reading'
    case 'productivity':
      return 'productivity'
    default:
      return category || 'service'
  }
}

function serviceURL(raw?: string) {
  if (!raw) return ''
  try {
    const u = new URL(raw)
    if (u.hostname.endsWith('.localhost') && !u.port) {
      u.port = window.location.port || '80'
    }
    return u.toString()
  } catch {
    return raw
  }
}

function placementOf(service: Addon) {
  return service.placements?.find((p) => p.role === 'primary' || !p.role) ?? service.placements?.[0]
}

export default function Services() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [tab, setTab] = useState<'installed' | 'catalogue'>('installed')
  const [category, setCategory] = useState('all')
  const [query, setQuery] = useState('')
  const [installEngine, setInstallEngine] = useState<string | undefined>()

  const services = useQuery({ queryKey: ['services'], queryFn: api.services, refetchInterval: 6000 })
  const engines = useQuery({ queryKey: ['service-engines'], queryFn: api.serviceEngines })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 })

  const list = services.data?.services ?? []
  const online = servers.data?.servers.filter((s) => s.online) ?? []
  const catalogue = engines.data?.engines ?? []

  const categories = useMemo(() => {
    const seen = new Set<string>()
    for (const e of catalogue) if (e.category) seen.add(e.category)
    return ['all', ...Array.from(seen).sort()]
  }, [catalogue])

  const filtered = catalogue.filter((e) => {
    if (category !== 'all' && e.category !== category) return false
    const q = query.trim().toLowerCase()
    if (!q) return true
    return e.name.toLowerCase().includes(q) || e.summary.toLowerCase().includes(q) || e.id.includes(q)
  })

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Services</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            Ready-built self-hosted apps installed on a phone — no source, build or deploy step.
          </p>
        </div>
        <Button variant="primary" onClick={() => setInstallEngine('')}>
          <Plus size={14} /> Install service
        </Button>
      </div>

      <div className="mt-4 flex items-center gap-1 border-b border-zinc-800/70">
        {[
          { id: 'installed' as const, label: 'Installed', count: list.length },
          { id: 'catalogue' as const, label: 'Catalogue', count: catalogue.length },
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

      {tab === 'installed' ? (
        services.isLoading ? (
          <div className="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-40 animate-pulse rounded-xl bg-zinc-900/40" />
            ))}
          </div>
        ) : list.length === 0 ? (
          <div className="mt-6">
            <EmptyState icon={<Boxes size={22} />} title="No services yet">
              <p>
                Install a prebuilt app on one of your phones. The first install downloads the release and starts it
                under the same supervisor as projects.
              </p>
              <div className="mt-4 flex items-center justify-center gap-2">
                <Button variant="primary" size="sm" onClick={() => setTab('catalogue')}>
                  Browse catalogue
                </Button>
              </div>
            </EmptyState>
          </div>
        ) : (
          <div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {list.map((s) => (
              <ServiceCard key={s.id} service={s} onOpen={() => navigate(`/services/${s.id}`)} />
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
                placeholder="Search services…"
              />
            </div>
            <div className="flex flex-wrap items-center gap-1">
              {categories.map((c) => (
                <button
                  key={c}
                  onClick={() => setCategory(c)}
                  className={cn(
                    'rounded-md px-2 py-1 text-[10px] transition-colors',
                    category === c ? 'bg-emerald-400/10 text-emerald-300' : 'text-zinc-500 hover:bg-zinc-900 hover:text-zinc-300',
                  )}
                >
                  {c === 'all' ? 'All' : categoryLabel(c)}
                </button>
              ))}
            </div>
          </div>
          {engines.isLoading ? (
            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {[0, 1, 2].map((i) => (
                <div key={i} className="h-44 animate-pulse rounded-xl bg-zinc-900/40" />
              ))}
            </div>
          ) : filtered.length === 0 ? (
            <div className="mt-6">
              <EmptyState icon={<Package size={22} />} title="No services match">
                <p>Clear the search or pick another category.</p>
              </EmptyState>
            </div>
          ) : (
            <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {filtered.map((e) => (
                <EngineCard key={e.id} engine={e} online={online.length > 0} onInstall={() => setInstallEngine(e.id)} />
              ))}
            </div>
          )}
        </>
      )}

      <InstallService
        open={installEngine !== undefined}
        initialEngine={installEngine || ''}
        onClose={() => setInstallEngine(undefined)}
        onInstalled={(id) => {
          qc.invalidateQueries({ queryKey: ['services'] })
          setInstallEngine(undefined)
          navigate(`/services/${id}`)
        }}
      />
    </div>
  )
}

function ServiceCard({ service, onOpen }: { service: Addon; onOpen: () => void }) {
  const qc = useQueryClient()
  const placement = placementOf(service)
  const action = useMutation({
    mutationFn: (a: 'start' | 'stop' | 'restart') => api.serviceAction(service.id, a),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['services'] }),
  })
  const url = serviceURL(service.url)
  return (
    <Card className="p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-zinc-400">{categoryIcon(service.category)}</span>
          <div className="min-w-0">
            <div className="truncate text-sm font-medium text-zinc-100">{service.name}</div>
            <div className="truncate text-[10px] text-zinc-500">
              {service.engineName}
              {service.version ? ` · ${service.version}` : ''}
            </div>
          </div>
        </div>
        <DeployBadge status={service.status} />
      </div>
      <div className="mt-2 text-[10px] text-zinc-500">
        {placement?.serverName || service.pinnedServer} · port {service.port}
      </div>
      {url ? (
        <a
          href={url}
          target="_blank"
          rel="noreferrer"
          className="mt-2 flex items-center gap-1 truncate font-mono text-[10px] text-emerald-300/80 hover:text-emerald-200"
        >
          {url.replace(/^https?:\/\//, '')} <ExternalLink size={10} />
        </a>
      ) : null}
      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <Button size="sm" onClick={onOpen}>
          Open
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={action.isPending}
          onClick={() => action.mutate(service.status === 'running' ? 'stop' : 'start')}
        >
          {service.status === 'running' ? <Square size={11} /> : <Play size={11} />}
          {service.status === 'running' ? 'Stop' : 'Start'}
        </Button>
        <Button size="sm" variant="ghost" disabled={action.isPending} onClick={() => action.mutate('restart')}>
          <RotateCw size={11} /> Restart
        </Button>
      </div>
      {action.isError ? <div className="mt-2 text-[10px] text-red-400">{String(action.error)}</div> : null}
    </Card>
  )
}

function EngineCard({ engine, online, onInstall }: { engine: EngineInfo; online: boolean; onInstall: () => void }) {
  return (
    <Card className="flex flex-col p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-zinc-400">{categoryIcon(engine.category, 15)}</span>
          <div className="min-w-0">
            <div className="truncate text-sm font-medium text-zinc-100">{engine.name}</div>
            <div className="text-[10px] tracking-wide text-zinc-500 uppercase">{categoryLabel(engine.category)}</div>
          </div>
        </div>
        {engine.version ? <Badge tone="neutral">{engine.version}</Badge> : null}
      </div>
      <p className="mt-2 min-h-[2.5rem] flex-1 text-[10px] leading-relaxed text-zinc-500">{engine.summary}</p>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-[10px] text-zinc-600">
        <span className="rounded bg-zinc-800/70 px-1.5 py-0.5 font-mono">{engine.minRamMb} MB</span>
        <span className="rounded bg-zinc-800/70 px-1.5 py-0.5 font-mono">:{engine.defaultPort}</span>
        {engine.website ? (
          <a href={engine.website} target="_blank" rel="noreferrer" className="hover:text-zinc-300">
            website
          </a>
        ) : null}
      </div>
      <div className="mt-3">
        <Button size="sm" variant="primary" disabled={!online} onClick={onInstall}>
          <Plus size={12} /> Install
        </Button>
      </div>
    </Card>
  )
}

function InstallService({
  open,
  initialEngine,
  onClose,
  onInstalled,
}: {
  open: boolean
  initialEngine: string
  onClose: () => void
  onInstalled: (id: string) => void
}) {
  const engines = useQuery({ queryKey: ['service-engines'], queryFn: api.serviceEngines, enabled: open })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, enabled: open })
  const [name, setName] = useState('')
  const [engine, setEngine] = useState(initialEngine)
  const [serverId, setServerId] = useState('')
  const [databaseUrl, setDatabaseUrl] = useState('')

  // The dialog stays mounted while closed, so seed the form every time it
  // opens. Clicking a catalogue card passes that engine as initialEngine.
  useEffect(() => {
    if (!open) return
    setEngine(initialEngine)
    setName('')
    setServerId('')
    setDatabaseUrl('')
  }, [open, initialEngine])

  const online = servers.data?.servers.filter((s) => s.online) ?? []
  const selected = engines.data?.engines.find((e) => e.id === engine)
  const needsPostgres = selected?.requires?.includes('postgres') ?? false
  const ready = !!name.trim() && !!engine && !!serverId && (!needsPostgres || !!databaseUrl.trim())

  const create = useMutation({
    mutationFn: () =>
      api.createService({
        name: name.trim(),
        engine,
        serverId,
        env: needsPostgres ? { DATABASE_URL: databaseUrl.trim() } : undefined,
      }),
    onSuccess: (res) => onInstalled(res.service.id),
  })

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Install service"
      description="Choose a prebuilt app and the phone that will run it. phleet installs the release, starts it and gives it its own subdomain."
      width="max-w-md"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={!ready || create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Plus size={13} />}
            Install
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div>
          <div className={label}>Name</div>
          <input
            className={`${input} mt-1`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My notes"
            autoFocus
          />
        </div>
        <div>
          <div className={label}>Service</div>
          <select className={`${input} mt-1`} value={engine} onChange={(e) => setEngine(e.target.value)}>
            <option value="">Select a service…</option>
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
            <option value="">Select a phone…</option>
            {online.map((s: ServerType) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </div>
        {needsPostgres ? (
          <div>
            <div className={label}>PostgreSQL DATABASE_URL</div>
            <input
              className={`${input} mt-1 font-mono`}
              value={databaseUrl}
              onChange={(e) => setDatabaseUrl(e.target.value)}
              placeholder="postgres://app:…@127.0.0.1:4100/app"
            />
            <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
              Create a PostgreSQL addon first, then copy its DATABASE_URL from the addon page.
            </p>
          </div>
        ) : null}
        {selected ? (
          <div className="rounded-lg border border-zinc-800/80 bg-zinc-900/40 p-3">
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs text-zinc-300">{selected.name}</span>
              <Badge tone="neutral">{selected.version || selected.category || 'service'}</Badge>
            </div>
            <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">{selected.summary}</p>
            <p className="mt-1 text-[10px] text-zinc-600">
              ~{selected.minRamMb} MB RAM · first install downloads the release. Open the service URL afterwards to
              finish its first-run setup.
            </p>
          </div>
        ) : null}
        {create.isError ? <div className="text-[11px] text-red-400">{String(create.error)}</div> : null}
      </div>
    </Dialog>
  )
}
