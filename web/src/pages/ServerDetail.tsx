import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  BatteryCharging,
  ChevronRight,
  Cpu,
  FolderTree,
  HardDrive,
  ListTree,
  MemoryStick,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Save,
  Settings as SettingsIcon,
  Smartphone,
  Terminal,
  Thermometer,
  Trash2,
  X,
} from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../api'
import FilesTab from '../components/FilesTab'
import { PromptDialog, ConfirmDialog } from '../components/Dialog'
import ProcessesTab from '../components/ProcessesTab'
import TerminalTab from '../components/TerminalTab'
import { Badge, Bar, Button, Card, KeyValue, PageLoader, Sparkline, Spinner, StatusDot, cn } from '../components/ui'
import { ago, bytes, pct, temp, time, uptime } from '../lib/format'
import type { Server } from '../types'

type Tab = 'overview' | 'terminal' | 'files' | 'processes' | 'settings'

const TABS: { id: Tab; label: string; icon: React.ReactNode }[] = [
  { id: 'overview', label: 'Overview', icon: <Smartphone size={14} /> },
  { id: 'terminal', label: 'Terminal', icon: <Terminal size={14} /> },
  { id: 'files', label: 'Files', icon: <FolderTree size={14} /> },
  { id: 'processes', label: 'Processes', icon: <ListTree size={14} /> },
  { id: 'settings', label: 'Settings', icon: <SettingsIcon size={14} /> },
]

export default function ServerDetail() {
  const { id = '' } = useParams()
  const [tab, setTab] = useState<Tab>('overview')
  const [renameOpen, setRenameOpen] = useState(false)
  const qc = useQueryClient()
  const navigate = useNavigate()

  const query = useQuery({
    queryKey: ['server', id],
    queryFn: () => api.server(id),
    refetchInterval: 8000,
    enabled: !!id,
  })

  const rename = useMutation({
    mutationFn: (name: string) => api.patchServer(id, { name }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['server', id] })
      qc.invalidateQueries({ queryKey: ['servers'] })
    },
  })

  const remove = useMutation({
    mutationFn: () => api.deleteServer(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['servers'] })
      navigate('/')
    },
  })

  if (query.isLoading) {
    return <PageLoader label="Loading phone" />
  }
  if (query.isError || !query.data) {
    return (
      <div className="p-8">
        <Link to="/" className="text-xs text-zinc-500 hover:text-zinc-300">
          ← back to fleet
        </Link>
        <div className="mt-4 text-sm text-red-400">{String((query.error as Error)?.message || 'server not found')}</div>
      </div>
    )
  }

  const server = query.data.server

  return (
    <div className="flex min-h-full flex-col">
      <div className="border-b border-zinc-800/70 bg-zinc-950/60 px-4 pt-4 md:px-8">
        <Link
          to="/"
          className="inline-flex items-center gap-1 text-[11px] text-zinc-500 transition-colors hover:text-zinc-300"
        >
          <ArrowLeft size={12} /> fleet
        </Link>

        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2.5">
              <StatusDot online={server.online} />
              <h1 className="truncate text-lg font-semibold tracking-tight text-zinc-100">{server.name}</h1>
              <Badge tone={server.online ? 'good' : 'neutral'}>{server.online ? 'online' : 'offline'}</Badge>
              {server.drain ? <Badge tone="warn">drained</Badge> : null}
              {server.caps?.root ? <Badge tone="warn">root agent</Badge> : null}
              {server.caps?.termux ? <Badge tone="info">termux</Badge> : null}
              {server.caps?.userland === false ? <Badge tone="warn">monitoring only</Badge> : null}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-zinc-500">
              <span className="font-mono">{server.hostname || server.id}</span>
              <span>{server.os}</span>
              <span>{server.arch}</span>
              <span>agent {server.agentVersion}</span>
              <span>{server.online ? `up ${uptime(server.latest?.uptime)}` : `seen ${ago(server.lastSeen)}`}</span>
            </div>
          </div>
          <div className="flex items-center gap-1.5">
            <Button size="sm" variant="ghost" onClick={() => query.refetch()}>
              <RefreshCw size={13} /> Refresh
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setRenameOpen(true)}
            >
              <Pencil size={13} /> Rename
            </Button>
          </div>
        </div>

        <div className="mt-4 flex gap-1 overflow-x-auto">
          {TABS.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={cn(
                'flex items-center gap-1.5 border-b-2 px-3 py-2 text-xs whitespace-nowrap transition-colors',
                tab === t.id
                  ? 'border-emerald-400 text-zinc-100'
                  : 'border-transparent text-zinc-500 hover:text-zinc-300',
              )}
            >
              {t.icon}
              {t.label}
            </button>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 px-4 py-5 md:px-8">
        {tab === 'settings' ? (
          <ServerSettings
            server={server}
            onDeleted={() => remove.mutate()}
            onRetired={() => {
              qc.invalidateQueries({ queryKey: ['servers'] })
              navigate('/')
            }}
            deleting={remove.isPending}
          />
        ) : !server.online && tab !== 'overview' ? (
          <Card className="p-6 text-center text-xs text-zinc-500">
            This phone is offline. {tab === 'terminal' ? 'Terminal' : tab === 'files' ? 'Files' : 'Processes'} needs a
            live agent connection.
          </Card>
        ) : tab === 'overview' ? (
          <Overview server={server} />
        ) : tab === 'terminal' ? (
          <TerminalTab id={server.id} online={server.online} />
        ) : tab === 'files' ? (
          <FilesTab id={server.id} />
        ) : (
          <ProcessesTab id={server.id} online={server.online} />
        )}
      </div>

      <PromptDialog
        open={renameOpen}
        onClose={() => setRenameOpen(false)}
        title="Rename phone"
        description={
          <>
            Give <span className="font-mono text-zinc-400">{server.name}</span> a new display name.
          </>
        }
        label="Phone name"
        initialValue={server.name}
        confirmLabel="Save name"
        validate={(name) => (name.length > 64 ? 'Keep it under 64 characters.' : '')}
        onSubmit={(name) => {
          if (name !== server.name) rename.mutate(name)
          setRenameOpen(false)
        }}
      />
    </div>
  )
}

function Overview({ server }: { server: Server }) {
  const m = server.latest
  const [command, setCommand] = useState('')
  const metrics = useQuery({
    queryKey: ['metrics', server.id, 360],
    queryFn: () => api.metrics(server.id, 360),
    refetchInterval: 30000,
  })

  const exec = useMutation({ mutationFn: (cmd: string) => api.exec(server.id, cmd, 30) })

  const samples = metrics.data?.samples ?? []
  const metricsFailed = metrics.isError
  const cpuSeries = samples.map((s) => s.cpuPercent)
  const tempSeries = samples.map((s) => s.tempC)
  const tools = Object.entries(server.caps?.tools ?? {}).filter(([, v]) => v)

  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Card className="p-4">
          <div className="flex items-center justify-between text-[10px] tracking-widest text-zinc-500 uppercase">
            <span className="flex items-center gap-1.5">
              <Cpu size={12} /> CPU
            </span>
            <span className="font-mono text-zinc-300">{m ? pct(m.cpuPercent) : '—'}</span>
          </div>
          <div className="mt-2">
            {metricsFailed ? (
              <div className="h-[42px] text-[10px] text-red-400/80">metrics unavailable</div>
            ) : cpuSeries.length > 1 ? (
              <Sparkline values={cpuSeries} height={42} />
            ) : (
              <div className="h-[42px] text-[10px] text-zinc-600">collecting…</div>
            )}
          </div>
          <div className="mt-1 text-[10px] text-zinc-600">load {m ? m.load1.toFixed(2) : '—'}</div>
        </Card>

        <Card className="p-4">
          <div className="flex items-center justify-between text-[10px] tracking-widest text-zinc-500 uppercase">
            <span className="flex items-center gap-1.5">
              <Thermometer size={12} /> Thermals
            </span>
            <span className="font-mono text-zinc-300">{m ? temp(m.tempC) : '—'}</span>
          </div>
          <div className="mt-2">
            {metricsFailed ? (
              <div className="h-[42px] text-[10px] text-red-400/80">metrics unavailable</div>
            ) : tempSeries.length > 1 ? (
              <Sparkline
                values={tempSeries}
                height={42}
                stroke={m && m.tempC >= 60 ? '#f87171' : m && m.tempC >= 45 ? '#fbbf24' : 'var(--accent)'}
              />
            ) : (
              <div className="h-[42px] text-[10px] text-zinc-600">collecting…</div>
            )}
          </div>
          <div className="mt-1 text-[10px] text-zinc-600">
            {server.caps?.thermalZones ? `${server.caps.thermalZones} zones` : 'no thermal zones'}
          </div>
        </Card>

        <Card className="p-4">
          <div className="flex items-center justify-between text-[10px] tracking-widest text-zinc-500 uppercase">
            <span className="flex items-center gap-1.5">
              <MemoryStick size={12} /> Memory
            </span>
            <span className="font-mono text-zinc-300">
              {m && m.memTotal > 0 ? pct((m.memUsed / m.memTotal) * 100) : '—'}
            </span>
          </div>
          <div className="mt-3">
            <Bar value={m?.memUsed ?? 0} max={m?.memTotal ?? 1} />
          </div>
          <div className="mt-2 font-mono text-[10px] text-zinc-500">
            {m ? `${bytes(m.memUsed)} / ${bytes(m.memTotal)}` : '—'}
          </div>
        </Card>

        <Card className="p-4">
          <div className="flex items-center justify-between text-[10px] tracking-widest text-zinc-500 uppercase">
            <span className="flex items-center gap-1.5">
              <BatteryCharging size={12} /> Battery
            </span>
            <span className="font-mono text-zinc-300">{m && m.batteryPct >= 0 ? `${m.batteryPct}%` : '—'}</span>
          </div>
          <div className="mt-3">
            <Bar
              value={m?.batteryPct ?? 0}
              max={100}
              tone={(m?.batteryPct ?? 0) < 20 ? 'red' : (m?.batteryPct ?? 0) < 40 ? 'amber' : 'emerald'}
            />
          </div>
          <div className="mt-2 text-[10px] text-zinc-500">
            {m?.charging ? 'charging' : 'discharging'}
            {m?.batteryTemp ? ` · ${m.batteryTemp.toFixed(1)}°C` : ''}
          </div>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_1fr]">
        <Card className="p-4">
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">System</div>
          <div className="mt-2 divide-y divide-zinc-800/60">
            <KeyValue label="Hostname">{server.hostname || '—'}</KeyValue>
            <KeyValue label="LAN">
              {server.caps?.ip ? <span className="font-mono">{server.caps.ip}</span> : '—'}
            </KeyValue>
            <KeyValue label="SSH">
              {server.caps?.sshPort ? (
                <span className="font-mono">:{server.caps.sshPort}</span>
              ) : server.caps?.sshPort === undefined ? (
                '—'
              ) : (
                'not listening'
              )}
            </KeyValue>
            <KeyValue label="OS">{server.os || '—'}</KeyValue>
            <KeyValue label="Arch">{server.arch || '—'}</KeyValue>
            <KeyValue label="CPUs">{String(server.caps?.cpus ?? '—')}</KeyValue>
            <KeyValue label="Home">{String(server.caps?.home ?? '—')}</KeyValue>
            <KeyValue label="Disk">
              {m ? `${bytes(m.diskUsed)} / ${bytes(m.diskTotal)}` : '—'}
            </KeyValue>
            <KeyValue label="Uptime">{m ? uptime(m.uptime) : '—'}</KeyValue>
            <KeyValue label="Last heartbeat">{m ? ago(m.at) : 'never'}</KeyValue>
            <KeyValue label="Registered">{time(server.createdAt)}</KeyValue>
            <KeyValue label="Agent ID">
              <span className="font-mono">{server.id}</span>
            </KeyValue>
          </div>
        </Card>

        <Card className="p-4">
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Capabilities</div>
          <div className="mt-3 flex flex-wrap gap-1.5">
            <Badge tone={server.caps?.root ? 'warn' : 'neutral'}>
              {server.caps?.root ? 'root agent' : 'non-root agent'}
            </Badge>
            {/* Only true capabilities are shown: a grey "termux" badge on a
                Debian chroot reads as if Termux were present. */}
            {server.caps?.termux ? <Badge tone="info">termux</Badge> : null}
            {server.caps?.userland === undefined ? null : (
              <Badge tone={server.caps.userland ? 'good' : 'warn'}>
                {server.caps.userland ? 'userland' : 'no userland'}
              </Badge>
            )}
            <Badge tone={server.caps?.pkgManager ? 'good' : 'neutral'}>
              {server.caps?.pkgManager || 'no package manager'}
            </Badge>
            {server.caps?.termuxApi ? <Badge tone="good">termux:api</Badge> : null}
            {server.caps?.adb ? <Badge tone="good">adb</Badge> : null}
            {server.caps?.battery ? <Badge tone="good">battery sysfs</Badge> : null}
            {server.caps?.chargeControl ? <Badge tone="good">charge control</Badge> : null}
          </div>
          {server.caps?.userland === false ? (
            <p className="mt-3 rounded-lg border border-amber-400/20 bg-amber-400/5 p-2.5 text-[10px] leading-relaxed text-amber-200/80">
              This phone runs the agent without Termux or a chroot. It reports metrics, battery, thermal, shell and
              files, but it cannot build projects or install data addons. Install Termux (or a chroot with git and a
              runtime) to make it a full node.
            </p>
          ) : null}
          {!server.online && server.caps?.sshPort ? (
            <p className="mt-3 rounded-lg border border-sky-400/20 bg-sky-400/5 p-2.5 text-[10px] leading-relaxed text-sky-200/80">
              The last probe saw sshd on <span className="font-mono">:{server.caps.sshPort}</span>. If SSH still
              answers, the phone and its userland are running and the agent is the thing to look at — check its service
              and log before blaming the network.
            </p>
          ) : null}
          <div className="mt-4 flex items-center justify-between">
            <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Toolchain</div>
            <span className="text-[9px] text-zinc-600">re-probed every minute</span>
          </div>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {tools.length === 0 ? (
              <span className="text-[11px] text-zinc-600">no common tools detected</span>
            ) : (
              tools.map(([name]) => (
                <span
                  key={name}
                  className="rounded-md bg-zinc-800/70 px-1.5 py-0.5 font-mono text-[10px] text-zinc-400"
                >
                  {name}
                </span>
              ))
            )}
          </div>
          <div className="mt-4 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Labels</div>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {Object.entries(server.labels ?? {}).length === 0 ? (
              <span className="text-[11px] text-zinc-600">no labels — add them in Settings to target deploys</span>
            ) : (
              Object.entries(server.labels).map(([k, v]) => (
                <span key={k} className="rounded-md bg-zinc-800/70 px-1.5 py-0.5 font-mono text-[10px] text-zinc-400">
                  {k}={v}
                </span>
              ))
            )}
          </div>
          <div className="mt-4 flex items-center gap-2 text-[10px] text-zinc-500">
            <HardDrive size={11} /> storage {m ? pct((m.diskUsed / Math.max(1, m.diskTotal)) * 100) : '—'} used
            {server.drain ? <span className="text-amber-300">· drained (no new traffic or placements)</span> : null}
          </div>
        </Card>
      </div>

      <Card className="p-4">
        <div className="flex items-center justify-between">
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Run a command</div>
          <span className="text-[10px] text-zinc-600">runs as the agent user in its home directory</span>
        </div>
        <div className="mt-2 flex flex-col gap-2 sm:flex-row">
          <input
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && command.trim() && !exec.isPending) exec.mutate(command)
            }}
            placeholder="uname -a"
            className="h-9 min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 font-mono text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40"
          />
          <Button
            variant="primary"
            size="sm"
            className="h-9"
            disabled={!command.trim() || exec.isPending || !server.online}
            onClick={() => exec.mutate(command)}
          >
            <Play size={12} /> Run
          </Button>
        </div>
        {exec.data ? (
          <div className="mt-3">
            <div className="mb-1 flex items-center gap-2 text-[10px] text-zinc-500">
              <span className={exec.data.exitCode === 0 ? 'text-emerald-400' : 'text-red-400'}>
                exit {exec.data.exitCode}
              </span>
              <span>{exec.data.durationMs}ms</span>
            </div>
            <pre className="max-h-64 overflow-auto rounded-lg bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300 ring-1 ring-zinc-800">
              {exec.data.output || '(no output)'}
            </pre>
          </div>
        ) : null}
        {exec.isError ? <div className="mt-2 text-[11px] text-red-400">{String(exec.error)}</div> : null}
      </Card>

      <div className="flex items-center justify-between pb-2 text-[10px] text-zinc-600">
        <span>
          <ChevronRight size={10} className="inline" /> metrics shown for the last 6 hours
        </span>
        <span>{metricsFailed ? 'metrics unavailable' : `${samples.length} samples`}</span>
      </div>
    </div>
  )
}

function ServerSettings({
  server,
  onDeleted,
  onRetired,
  deleting,
}: {
  server: Server
  onDeleted: () => void
  onRetired: () => void
  deleting: boolean
}) {
  const qc = useQueryClient()
  const [name, setName] = useState(server.name)
  const [retireOpen, setRetireOpen] = useState(false)
  const [purgeAddons, setPurgeAddons] = useState(false)
  const [labels, setLabels] = useState<{ k: string; v: string }[]>(
    Object.entries(server.labels ?? {}).map(([k, v]) => ({ k, v })),
  )

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['server', server.id] })
    qc.invalidateQueries({ queryKey: ['servers'] })
    qc.invalidateQueries({ queryKey: ['projects'] })
  }

  const save = useMutation({
    mutationFn: () =>
      api.patchServer(server.id, {
        name: name.trim() || server.name,
        labels: Object.fromEntries(labels.filter((l) => l.k.trim()).map((l) => [l.k.trim(), l.v.trim()])),
      }),
    onSuccess: refresh,
  })
  const drain = useMutation({
    mutationFn: (v: boolean) => api.patchServer(server.id, { drain: v }),
    onSuccess: refresh,
  })
  const update = useMutation({
    mutationFn: () => api.updateAgent(server.id),
    onSuccess: refresh,
  })
  const retire = useMutation({
    mutationFn: (purge: boolean) => api.retireServer(server.id, purge),
    onSuccess: onRetired,
  })

  const field =
    'h-8 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-2.5 font-mono text-[11px] text-zinc-200 outline-none focus:border-emerald-400/40'
  const caption = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
      <Card className="p-4">
        <div className={caption}>Identity and labels</div>
        <p className="mt-1 text-[10px] text-zinc-600">
          Labels drive scheduling: a project with selector <span className="font-mono">tier=web</span> only lands on
          phones labelled <span className="font-mono">tier=web</span>.
        </p>
        <div className="mt-3">
          <div className={caption}>Display name</div>
          <input className={`${field} mt-1`} value={name} onChange={(e) => setName(e.target.value)} />
        </div>

        <div className="mt-4 flex items-center justify-between">
          <div className={caption}>Labels</div>
          <Button size="sm" variant="ghost" onClick={() => setLabels([...labels, { k: '', v: '' }])}>
            <Plus size={12} /> Add
          </Button>
        </div>
        <div className="mt-2 space-y-1.5">
          {labels.length === 0 ? (
            <div className="rounded-lg border border-dashed border-zinc-800 px-3 py-3 text-[11px] text-zinc-600">
              No labels yet.
            </div>
          ) : (
            labels.map((l, i) => (
              <div key={i} className="flex items-center gap-1.5">
                <input
                  className={field}
                  placeholder="key"
                  value={l.k}
                  onChange={(e) => setLabels(labels.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))}
                />
                <span className="text-zinc-600">=</span>
                <input
                  className={field}
                  placeholder="value"
                  value={l.v}
                  onChange={(e) => setLabels(labels.map((x, j) => (j === i ? { ...x, v: e.target.value } : x)))}
                />
                <Button size="icon" variant="ghost" onClick={() => setLabels(labels.filter((_, j) => j !== i))}>
                  <X size={12} />
                </Button>
              </div>
            ))
          )}
        </div>

        {save.isError ? <div className="mt-3 text-[11px] text-red-400">{String(save.error)}</div> : null}
        {save.isSuccess ? <div className="mt-3 text-[11px] text-emerald-300">saved</div> : null}
        <div className="mt-4 flex justify-end">
          <Button variant="primary" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
            Save
          </Button>
        </div>
      </Card>

      <div className="space-y-3">
        <Card className="p-4">
          <div className={caption}>Agent</div>
          <div className="mt-2 flex items-center justify-between gap-2">
            <span className="font-mono text-[11px] text-zinc-400">{server.agentVersion || 'unknown'}</span>
            <Badge tone={server.online ? 'good' : 'neutral'}>{server.online ? 'connected' : 'offline'}</Badge>
          </div>
          <p className="mt-2 text-[10px] leading-relaxed text-zinc-500">
            Asks the phone to check the signed update manifest now instead of waiting for its six-hour timer.
          </p>
          <Button
            size="sm"
            variant="outline"
            className="mt-3 w-full"
            disabled={!server.online || update.isPending}
            onClick={() => update.mutate()}
          >
            {update.isPending ? <Spinner className="h-3 w-3" /> : <RefreshCw size={12} />} Check for update
          </Button>
          {update.isError ? <div className="mt-2 text-[10px] text-red-400">{String(update.error)}</div> : null}
          {update.isSuccess ? <div className="mt-2 text-[10px] text-emerald-300">update check requested</div> : null}
        </Card>

        <Card className="p-4">
          <div className={caption}>Scheduling</div>
          <div className="mt-2 flex items-center justify-between gap-2">
            <span className="text-[11px] text-zinc-400">{server.drain ? 'Drained' : 'Accepting work'}</span>
            <Badge tone={server.drain ? 'warn' : 'good'}>{server.drain ? 'drain' : 'active'}</Badge>
          </div>
          <p className="mt-2 text-[10px] leading-relaxed text-zinc-500">
            A drained phone is removed from routing and receives no new replicas. The reconciler moves its apps to
            other matching phones, then stops them here. Existing processes keep running until that happens.
          </p>
          <Button
            size="sm"
            variant={server.drain ? 'subtle' : 'outline'}
            className="mt-3 w-full"
            disabled={drain.isPending}
            onClick={() => drain.mutate(!server.drain)}
          >
            {drain.isPending ? <Spinner className="h-3 w-3" /> : null}
            {server.drain ? 'Resume traffic and placement' : 'Drain this phone'}
          </Button>
          {drain.isError ? <div className="mt-2 text-[10px] text-red-400">{String(drain.error)}</div> : null}
        </Card>

        <Card className="border-red-900/40 p-4">
          <div className="text-[10px] font-medium tracking-widest text-red-400/80 uppercase">Danger zone</div>
          <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">
            Removes the phone from the panel. The agent keeps running and re-registers on its next connection.
          </p>
          <Button variant="danger" size="sm" className="mt-3 w-full" disabled={deleting} onClick={onDeleted}>
            <Trash2 size={12} /> Remove server
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="mt-2 w-full"
            disabled={retire.isPending}
            onClick={() => setRetireOpen(true)}
          >
            Retire / wipe phone
          </Button>
          {retire.isError ? <div className="mt-2 text-[10px] text-red-400">{String(retire.error)}</div> : null}
        </Card>
      </div>

      <ConfirmDialog
        open={retireOpen}
        onClose={() => setRetireOpen(false)}
        title={`Retire ${server.name}?`}
        description={
          <div className="space-y-2">
            <p>
              Stops its apps, deletes its scheduled tasks and removes it from the panel. Data on the device stays
              unless you also delete its databases.
            </p>
            <label className="flex items-start gap-2 text-[11px] text-zinc-300">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={purgeAddons}
                onChange={(e) => setPurgeAddons(e.target.checked)}
              />
              Also delete every database/service on this phone and their backups. This cannot be undone.
            </label>
          </div>
        }
        confirmLabel="Retire phone"
        danger
        busy={retire.isPending}
        onConfirm={() => retire.mutate(purgeAddons)}
      />
    </div>
  )
}
