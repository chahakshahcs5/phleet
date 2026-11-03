import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Bell, BellRing, Check, CheckCheck, RefreshCw, Save, Send } from 'lucide-react'
import { useEffect, useState } from 'react'
import { api } from '../api'
import type { AlertRules, NotifyConfig } from '../types'
import { Badge, Button, Card, EmptyState, Spinner, cn } from '../components/ui'
import { ago, time } from '../lib/format'

type Tab = 'active' | 'history' | 'settings'

function severityTone(sev: string): 'bad' | 'warn' | 'info' | 'neutral' {
  if (sev === 'critical') return 'bad'
  if (sev === 'warning') return 'warn'
  if (sev === 'info') return 'info'
  return 'neutral'
}

function AlertRow({ alert, onAck }: { alert: import('../types').Alert; onAck?: () => void }) {
  return (
    <div
      className={cn(
        'flex flex-wrap items-start justify-between gap-3 rounded-xl border px-4 py-3',
        alert.severity === 'critical'
          ? 'border-red-900/50 bg-red-500/5'
          : alert.severity === 'warning'
            ? 'border-amber-900/40 bg-amber-500/5'
            : 'border-zinc-800 bg-zinc-900/40',
      )}
    >
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={severityTone(alert.severity)}>{alert.severity}</Badge>
          <span className="font-mono text-[10px] text-zinc-500">{alert.kind}</span>
          {alert.status === 'ack' ? <Badge tone="info">acknowledged</Badge> : null}
          {alert.status === 'resolved' ? <Badge>resolved</Badge> : null}
        </div>
        <div className="mt-1 text-[12px] text-zinc-200">{alert.message}</div>
        <div className="mt-1 text-[10px] text-zinc-600" title={time(alert.createdAt)}>
          {ago(alert.createdAt)} · target {alert.target || '—'}
        </div>
      </div>
      {onAck && alert.status === 'firing' ? (
        <Button size="sm" variant="ghost" onClick={onAck}>
          <Check size={12} /> Ack
        </Button>
      ) : null}
    </div>
  )
}

function RulesForm() {
  const qc = useQueryClient()
  const rulesQ = useQuery({ queryKey: ['alert-rules'], queryFn: api.alertRules })
  const [rules, setRules] = useState<AlertRules>({ tempC: 60, batteryPct: 20, diskPct: 90, offlineSec: 120 })
  useEffect(() => {
    if (rulesQ.data) setRules(rulesQ.data)
  }, [rulesQ.data])
  const save = useMutation({
    mutationFn: () => api.saveAlertRules(rules),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alert-rules'] }),
  })
  const input =
    'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 font-mono text-xs text-zinc-200 outline-none focus:border-emerald-400/40'
  return (
    <Card className="p-4">
      <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Thresholds</div>
      <div className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div>
          <div className="text-[10px] text-zinc-500">Temp °C</div>
          <input type="number" className={`${input} mt-1`} value={rules.tempC} onChange={(e) => setRules({ ...rules, tempC: Number(e.target.value) })} />
        </div>
        <div>
          <div className="text-[10px] text-zinc-500">Battery %</div>
          <input type="number" className={`${input} mt-1`} value={rules.batteryPct} onChange={(e) => setRules({ ...rules, batteryPct: Number(e.target.value) })} />
        </div>
        <div>
          <div className="text-[10px] text-zinc-500">Disk %</div>
          <input type="number" className={`${input} mt-1`} value={rules.diskPct} onChange={(e) => setRules({ ...rules, diskPct: Number(e.target.value) })} />
        </div>
        <div>
          <div className="text-[10px] text-zinc-500">Offline seconds</div>
          <input type="number" className={`${input} mt-1`} value={rules.offlineSec} onChange={(e) => setRules({ ...rules, offlineSec: Number(e.target.value) })} />
        </div>
      </div>
      <p className="mt-2 text-[10px] text-zinc-600">
        Battery alerts are skipped while charging. App crashes and failed deploys always raise a warning.
      </p>
      {save.isError ? <div className="mt-2 text-[11px] text-red-400">{String(save.error)}</div> : null}
      <div className="mt-3 flex justify-end">
        <Button size="sm" variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
          Save thresholds
        </Button>
      </div>
    </Card>
  )
}

function NotifyForm() {
  const qc = useQueryClient()
  const cfgQ = useQuery({ queryKey: ['notify'], queryFn: api.notifyConfig })
  const [cfg, setCfg] = useState<NotifyConfig>({ telegramToken: '', telegramChat: '', webhookUrl: '' })
  useEffect(() => {
    if (cfgQ.data) setCfg(cfgQ.data)
  }, [cfgQ.data])
  const save = useMutation({
    mutationFn: () => api.saveNotifyConfig(cfg),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notify'] }),
  })
  const test = useMutation({ mutationFn: () => api.testNotify() })
  const input =
    'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 font-mono text-[11px] text-zinc-200 outline-none focus:border-emerald-400/40'
  return (
    <Card className="p-4">
      <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Notifications</div>
      <p className="mt-1 text-[10px] text-zinc-600">Alerts open and resolve are posted here. Leave blank to disable.</p>
      <div className="mt-3 space-y-3">
        <div>
          <div className="text-[10px] text-zinc-500">Telegram bot token</div>
          <input className={`${input} mt-1`} value={cfg.telegramToken} onChange={(e) => setCfg({ ...cfg, telegramToken: e.target.value })} placeholder="123456:ABC..." />
        </div>
        <div>
          <div className="text-[10px] text-zinc-500">Telegram chat id</div>
          <input className={`${input} mt-1`} value={cfg.telegramChat} onChange={(e) => setCfg({ ...cfg, telegramChat: e.target.value })} placeholder="-100123456789" />
        </div>
        <div>
          <div className="text-[10px] text-zinc-500">Webhook URL</div>
          <input className={`${input} mt-1`} value={cfg.webhookUrl} onChange={(e) => setCfg({ ...cfg, webhookUrl: e.target.value })} placeholder="https://example.com/phleet-hook" />
        </div>
      </div>
      {save.isError ? <div className="mt-2 text-[11px] text-red-400">{String(save.error)}</div> : null}
      {test.isError ? <div className="mt-2 text-[11px] text-red-400">{String(test.error)}</div> : null}
      {test.isSuccess ? <div className="mt-2 text-[11px] text-emerald-300">test sent</div> : null}
      <div className="mt-3 flex justify-end gap-2">
        <Button size="sm" variant="ghost" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? <Spinner className="h-3 w-3" /> : <Send size={12} />} Send test
        </Button>
        <Button size="sm" variant="primary" disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
          Save
        </Button>
      </div>
    </Card>
  )
}

export default function Alerts() {
  const [tab, setTab] = useState<Tab>('active')
  const qc = useQueryClient()
  const active = useQuery({ queryKey: ['alerts', 'active'], queryFn: () => api.alerts(true), refetchInterval: 10000 })
  const history = useQuery({ queryKey: ['alerts', 'history'], queryFn: () => api.alerts(false), refetchInterval: 30000 })
  const ack = useMutation({
    mutationFn: (id: number) => api.ackAlert(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['alerts'] })
      qc.invalidateQueries({ queryKey: ['alert-count'] })
    },
  })

  const activeList = active.data?.alerts ?? []
  const historyList = history.data?.alerts ?? []

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Alerts</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            {activeList.length === 0 ? 'nothing firing' : `${activeList.length} active alert${activeList.length === 1 ? '' : 's'}`}
          </p>
        </div>
        <Button size="sm" variant="ghost" onClick={() => qc.invalidateQueries({ queryKey: ['alerts'] })}>
          {active.isFetching ? <Spinner className="h-3 w-3" /> : <RefreshCw size={13} />} Refresh
        </Button>
      </div>

      <div className="mt-4 flex gap-1 border-b border-zinc-800/70">
        {(
          [
            { id: 'active', label: `Active${activeList.length ? ` (${activeList.length})` : ''}`, icon: <BellRing size={13} /> },
            { id: 'history', label: 'History', icon: <Bell size={13} /> },
            { id: 'settings', label: 'Rules & notifications', icon: <AlertTriangle size={13} /> },
          ] as { id: Tab; label: string; icon: React.ReactNode }[]
        ).map((t) => (
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

      <div className="mt-4">
        {tab === 'active' ? (
          <div className="space-y-2">
            {active.isLoading ? (
              <div className="flex items-center gap-2 p-6 text-xs text-zinc-500">
                <Spinner /> loading…
              </div>
            ) : activeList.length === 0 ? (
              <EmptyState icon={<CheckCheck size={22} />} title="All clear">
                Phones, apps and deploys are inside their thresholds. Alerts appear here when something crosses a limit.
              </EmptyState>
            ) : (
              activeList.map((a) => (
                <AlertRow key={a.id} alert={a} onAck={() => ack.mutate(a.id)} />
              ))
            )}
          </div>
        ) : null}

        {tab === 'history' ? (
          <div className="space-y-2">
            {historyList.length === 0 ? (
              <EmptyState icon={<Bell size={20} />} title="No alert history yet" />
            ) : (
              historyList.map((a) => <AlertRow key={a.id} alert={a} />)
            )}
          </div>
        ) : null}

        {tab === 'settings' ? (
          <div className="space-y-3">
            <RulesForm />
            <NotifyForm />
            <Card className="p-4">
              <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">How evaluation works</div>
              <ul className="mt-2 list-disc space-y-1 pl-4 text-[11px] leading-relaxed text-zinc-500">
                <li>Every 30 seconds the control plane checks all phones and placements.</li>
                <li>Alerts open once, then update in place; they resolve automatically when the condition clears.</li>
                <li>Deploy failures resolve on the next successful deploy or rollback; app crashes resolve when the app runs again.</li>
                <li>Resolved alerts are kept for 90 days.</li>
              </ul>
            </Card>
          </div>
        ) : null}
      </div>
    </div>
  )
}
