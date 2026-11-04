import { useMutation, useQuery } from '@tanstack/react-query'
import {
  Activity,
  BatteryCharging,
  Bell,
  ChevronRight,
  Cpu,
  Globe,
  HardDrive,
  MemoryStick,
  Play,
  Plus,
  RefreshCw,
  Smartphone,
  Thermometer,
  Wifi,
} from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import type { FleetResult, Server } from '../types'
import { ago, bytes, pct, temp, uptime } from '../lib/format'
import { Badge, Button, Card, EmptyState, Sparkline, Spinner, StatusDot, cn } from '../components/ui'

function tempTone(t: number): 'emerald' | 'amber' | 'red' {
  if (t >= 60) return 'red'
  if (t >= 45) return 'amber'
  return 'emerald'
}

function StatCard({
  label,
  value,
  hint,
  icon,
}: {
  label: string
  value: string
  hint?: string
  icon: React.ReactNode
}) {
  return (
    <Card className="p-4">
      <div className="flex items-start justify-between">
        <span className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">{label}</span>
        <span className="text-zinc-600">{icon}</span>
      </div>
      <div className="mt-2 text-2xl font-semibold tracking-tight text-zinc-100">{value}</div>
      {hint ? <div className="mt-0.5 text-[11px] text-zinc-500">{hint}</div> : null}
    </Card>
  )
}

function ServerCard({ server }: { server: Server }) {
  const m = server.latest
  const cpu = m?.cpuPercent ?? 0
  const memPct = m && m.memTotal > 0 ? (m.memUsed / m.memTotal) * 100 : 0
  const battery = m?.batteryPct ?? -1

  return (
    <Link
      to={`/servers/${server.id}`}
      className="group block rounded-xl border border-zinc-800/80 bg-zinc-900/40 p-4 transition-colors hover:border-zinc-700 hover:bg-zinc-900/70"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <StatusDot online={server.online} />
          <div className="min-w-0">
            <div className="truncate text-sm font-medium text-zinc-100">{server.name}</div>
            <div className="truncate text-[11px] text-zinc-500">{server.hostname || server.id.slice(0, 8)}</div>
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          {server.drain ? <Badge tone="warn">drained</Badge> : null}
          {server.caps?.root ? <Badge tone="warn">root</Badge> : null}
          <Badge tone={server.online ? 'good' : 'neutral'}>{server.online ? 'online' : 'offline'}</Badge>
        </div>
      </div>

      <div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3">
        <div>
          <div className="flex items-center justify-between text-[10px] text-zinc-500">
            <span className="flex items-center gap-1">
              <Cpu size={10} /> CPU
            </span>
            <span className="font-mono text-zinc-300">{m ? pct(cpu) : '—'}</span>
          </div>
          <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-zinc-800">
            <div
              className={cn(
                'h-full rounded-full',
                cpu > 85 ? 'bg-red-400' : cpu > 60 ? 'bg-amber-400' : 'bg-emerald-400/80',
              )}
              style={{ width: `${Math.min(100, cpu)}%` }}
            />
          </div>
        </div>
        <div>
          <div className="flex items-center justify-between text-[10px] text-zinc-500">
            <span className="flex items-center gap-1">
              <Thermometer size={10} /> Temp
            </span>
            <span
              className={cn(
                'font-mono',
                m && m.tempC >= 60 ? 'text-red-300' : m && m.tempC >= 45 ? 'text-amber-300' : 'text-zinc-300',
              )}
            >
              {m ? temp(m.tempC) : '—'}
            </span>
          </div>
          <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-zinc-800">
            <div
              className={cn(
                'h-full rounded-full',
                tempTone(m?.tempC ?? 0) === 'red' && 'bg-red-400',
                tempTone(m?.tempC ?? 0) === 'amber' && 'bg-amber-400',
                tempTone(m?.tempC ?? 0) === 'emerald' && 'bg-emerald-400/80',
              )}
              style={{ width: `${Math.min(100, ((m?.tempC ?? 0) / 80) * 100)}%` }}
            />
          </div>
        </div>

        <div className="flex items-center gap-2 text-[11px] text-zinc-500">
          <MemoryStick size={11} />
          <span className="font-mono text-zinc-300">{m ? `${Math.round(memPct)}%` : '—'}</span>
          <span className="truncate">{m ? `${bytes(m.memUsed)} / ${bytes(m.memTotal)}` : 'memory'}</span>
        </div>
        <div className="flex items-center gap-2 text-[11px] text-zinc-500">
          <BatteryCharging size={11} className={m?.charging ? 'text-emerald-400' : undefined} />
          <span className="font-mono text-zinc-300">{battery >= 0 ? `${battery}%` : '—'}</span>
          <span className="truncate">{m?.charging ? 'charging' : 'battery'}</span>
        </div>
      </div>

      <div className="mt-3.5 flex items-center justify-between border-t border-zinc-800/70 pt-2.5 text-[10px] text-zinc-500">
        <span className="flex items-center gap-1">
          <Activity size={10} /> up {m ? uptime(m.uptime) : '—'}
        </span>
        <span className="flex items-center gap-1">
          <Wifi size={10} /> {server.online ? 'live' : ago(server.lastSeen)}
          <ChevronRight size={11} className="ml-1 text-zinc-700 group-hover:text-zinc-400" />
        </span>
      </div>
    </Link>
  )
}

function FleetTools({ servers }: { servers: Server[] }) {
  const [selected, setSelected] = useState<string[]>([])
  const [command, setCommand] = useState('')
  const [results, setResults] = useState<FleetResult[]>([])
  const exec = useMutation({
    mutationFn: () => api.batchExec(selected, command, 60),
    onSuccess: (res) => setResults(res.results),
  })
  const update = useMutation({
    mutationFn: () => api.updateAllAgents(),
    onSuccess: (res) => setResults(res.results),
  })
  const online = servers.filter((s) => s.online)
  const toggle = (id: string) => {
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
  }
  return (
    <Card className="mt-4 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Fleet commands</div>
        <span className="text-[10px] text-zinc-600">no selection runs on every online phone</span>
      </div>
      <div className="mt-2 flex flex-wrap gap-1.5">
        {online.map((s) => (
          <button
            key={s.id}
            type="button"
            onClick={() => toggle(s.id)}
            className={cn(
              'rounded-md border px-2 py-1 text-[10px] transition-colors',
              selected.includes(s.id)
                ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200',
            )}
          >
            {s.name}
          </button>
        ))}
        {online.length === 0 ? <span className="text-[10px] text-zinc-600">no phone is online</span> : null}
      </div>
      <div className="mt-3 flex flex-col gap-2 sm:flex-row">
        <input
          className="h-9 min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 font-mono text-[11px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40"
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder="uptime && df -h"
        />
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="primary"
            disabled={!command.trim() || exec.isPending}
            onClick={() => exec.mutate()}
          >
            {exec.isPending ? <Spinner className="h-3 w-3" /> : <Play size={12} />} Run
          </Button>
          <Button size="sm" variant="outline" disabled={update.isPending || online.length === 0} onClick={() => update.mutate()}>
            {update.isPending ? <Spinner className="h-3 w-3" /> : <RefreshCw size={12} />} Update agents
          </Button>
        </div>
      </div>
      {results.length > 0 ? (
        <div className="mt-3 max-h-56 space-y-1.5 overflow-y-auto">
          {results.map((r) => (
            <div key={r.serverId} className="rounded-md bg-zinc-950/60 p-2">
              <div className="flex items-center justify-between text-[10px]">
                <span className="text-zinc-300">{r.name || r.serverId.slice(0, 8)}</span>
                <Badge tone={r.ok ? 'good' : 'bad'}>{r.ok ? 'ok' : r.error || `exit ${r.exitCode}`}</Badge>
              </div>
              {r.output ? (
                <pre className="mt-1 max-h-24 overflow-auto font-mono text-[10px] whitespace-pre-wrap text-zinc-500">
                  {r.output}
                </pre>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}
      {exec.isError ? <div className="mt-2 text-[10px] text-red-400">{String(exec.error)}</div> : null}
      {update.isError ? <div className="mt-2 text-[10px] text-red-400">{String(update.error)}</div> : null}
    </Card>
  )
}

export default function Dashboard() {
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 8000 })
  const events = useQuery({ queryKey: ['events', 12], queryFn: () => api.events({ limit: 12 }), refetchInterval: 20000 })
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const alertCount = useQuery({ queryKey: ['alert-count'], queryFn: api.alertCount, refetchInterval: 20000 })

  const list = servers.data?.servers ?? []
  const online = list.filter((s) => s.online)
  const latest = online.map((s) => s.latest).filter((m): m is NonNullable<typeof m> => !!m)
  const avgTemp = latest.length ? latest.reduce((a, m) => a + m.tempC, 0) / latest.length : 0
  const memUsed = latest.reduce((a, m) => a + m.memUsed, 0)
  const memTotal = latest.reduce((a, m) => a + m.memTotal, 0)
  const hot = online.filter((s) => (s.latest?.tempC ?? 0) >= 60).length
  const publicUrl = me.data?.publicUrl || ''

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Fleet</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            {online.length} of {list.length} phones online
            {hot > 0 ? <span className="text-amber-400"> · {hot} running hot</span> : null}
          </p>
        </div>
        <div className="flex items-center gap-2 text-[10px] text-zinc-500">
          <span className="flex items-center gap-1.5 rounded-md bg-zinc-900 px-2 py-1 ring-1 ring-zinc-800">
            <Globe size={11} className={publicUrl ? 'text-emerald-400' : 'text-zinc-600'} />
            {publicUrl ? publicUrl.replace(/^https?:\/\//, '') : 'local only'}
          </span>
          <span className="rounded-md bg-zinc-900 px-2 py-1 ring-1 ring-zinc-800">
            {servers.data ? `synced ${new Date(servers.dataUpdatedAt).toLocaleTimeString()}` : 'syncing…'}
          </span>
          <Link
            to="/servers/add"
            className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-emerald-400 px-3 text-xs font-medium text-[var(--accent-foreground)] transition-colors hover:bg-emerald-300"
          >
            <Plus size={13} /> Add phone
          </Link>
        </div>
      </div>

      {alertCount.data && alertCount.data.count > 0 ? (
        <Link
          to="/alerts"
          className="mt-4 flex items-center justify-between gap-3 rounded-xl border border-amber-900/40 bg-amber-500/5 px-4 py-2.5 text-amber-200 transition-colors hover:bg-amber-500/10"
        >
          <span className="flex items-center gap-2 text-xs">
            <Bell size={14} /> {alertCount.data.count} active alert{alertCount.data.count === 1 ? '' : 's'}
          </span>
          <span className="flex items-center gap-1 text-[10px] text-amber-300/70">
            review <ChevronRight size={11} />
          </span>
        </Link>
      ) : null}

      <div className="mt-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard
          label="Online"
          value={`${online.length}/${list.length}`}
          hint={list.length === 0 ? 'no phones yet' : `${list.length - online.length} offline`}
          icon={<Smartphone size={15} />}
        />
        <StatCard
          label="Avg temp"
          value={latest.length ? temp(avgTemp) : '—'}
          hint={hot > 0 ? `${hot} above 60°C` : 'all within range'}
          icon={<Thermometer size={15} />}
        />
        <StatCard
          label="Memory"
          value={memTotal ? `${Math.round((memUsed / memTotal) * 100)}%` : '—'}
          hint={memTotal ? `${bytes(memUsed)} of ${bytes(memTotal)}` : 'waiting for heartbeats'}
          icon={<MemoryStick size={15} />}
        />
        <StatCard
          label="Agent version"
          value={online[0]?.agentVersion || '—'}
          hint={publicUrl ? 'edge via cloudflare' : 'tunnel not configured'}
          icon={<HardDrive size={15} />}
        />
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-[1fr_320px]">
        <section>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-[11px] font-medium tracking-widest text-zinc-500 uppercase">Phones</h2>
            {list.some((s) => !s.online) ? (
              <span className="text-[10px] text-zinc-600">offline phones keep running their apps</span>
            ) : null}
          </div>
          {servers.isLoading ? (
            <div className="grid gap-3 sm:grid-cols-2">
              {[0, 1].map((i) => (
                <div key={i} className="h-44 animate-pulse rounded-xl bg-zinc-900/40" />
              ))}
            </div>
          ) : list.length === 0 ? (
            <EmptyState icon={<Smartphone size={22} />} title="No phones registered">
              <p>
                Start phleetd, then run the bootstrap script inside Termux with the agent token
                from <span className="font-mono text-zinc-400">%ProgramData%\phleet\agent_token</span>.
              </p>
              <pre className="mt-3 overflow-x-auto rounded-lg bg-zinc-950 p-3 text-left font-mono text-[10px] leading-relaxed text-zinc-400 ring-1 ring-zinc-800">
{`curl -fsSL ${publicUrl || 'https://fleet.example.com'}/download/bootstrap.sh \\
  | bash -s -- --server ${publicUrl || 'https://fleet.example.com'} --token <agent-token>`}
              </pre>
              <p className="mt-3">
                No domain yet? Point the phone at <span className="font-mono text-zinc-400">http://&lt;pc-lan-ip&gt;:8080</span>{' '}
                — the agent dials out, so no ports need to be opened.
              </p>
              <Link
                to="/servers/add"
                className="mt-3 inline-flex h-8 items-center gap-1.5 rounded-lg bg-emerald-400 px-3 text-xs font-medium text-[var(--accent-foreground)] hover:bg-emerald-300"
              >
                <Plus size={13} /> Add your first phone
              </Link>
            </EmptyState>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2">
              {list.map((s) => (
                <ServerCard key={s.id} server={s} />
              ))}
            </div>
          )}
          <FleetTools servers={list} />
        </section>

        <aside>
          <h2 className="mb-3 text-[11px] font-medium tracking-widest text-zinc-500 uppercase">Recent events</h2>
          <Card className="divide-y divide-zinc-800/70">
            {(events.data?.events ?? []).slice(0, 10).map((e) => (
              <div key={e.id} className="flex items-start gap-2.5 px-3.5 py-2.5">
                <span className="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-zinc-700" />
                <div className="min-w-0">
                  <div className="truncate text-[11px] text-zinc-300">
                    <span className="font-mono text-[10px] text-zinc-500">{e.action}</span>
                    {e.detail ? <span className="text-zinc-500"> · {e.detail}</span> : null}
                  </div>
                  <div className="text-[10px] text-zinc-600">
                    {ago(e.ts)} · {e.actor}
                  </div>
                </div>
              </div>
            ))}
            {events.data?.events.length === 0 ? (
              <div className="px-3.5 py-6 text-center text-[11px] text-zinc-600">nothing yet</div>
            ) : null}
          </Card>
          <div className="mt-3 text-right">
            <Link to="/events" className="text-[10px] text-zinc-500 hover:text-zinc-300">
              all events →
            </Link>
          </div>
        </aside>
      </div>
    </div>
  )
}
