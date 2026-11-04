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
  const empty: NotifyConfig = {
    telegramToken: '', telegramChat: '', webhookUrl: '',
    ntfyUrl: '', gotifyUrl: '', gotifyToken: '', discordUrl: '', slackUrl: '',
    smtpHost: '', smtpPort: 587, smtpUser: '', smtpPass: '', emailFrom: '', emailTo: '',
    quietHours: false, quietStart: 22, quietEnd: 7, maintenanceUntil: 0,
  }
  const [cfg, setCfg] = useState<NotifyConfig>(empty)
  useEffect(() => {
    if (cfgQ.data) setCfg({ ...empty, ...cfgQ.data })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cfgQ.data])
  const save = useMutation({
    mutationFn: () => api.saveNotifyConfig(cfg),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notify'] }),
  })
  const test = useMutation({ mutationFn: () => api.testNotify() })
  const input =
    'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 font-mono text-[11px] text-zinc-200 outline-none focus:border-emerald-400/40'
  const label = 'text-[10px] text-zinc-500'
  const maintenanceValue = cfg.maintenanceUntil
    ? new Date(cfg.maintenanceUntil * 1000).toISOString().slice(0, 16)
    : ''
  return (
    <Card className="p-4">
      <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Notifications</div>
      <p className="mt-1 text-[10px] text-zinc-600">
        Alerts open and resolve are posted to every configured channel. Leave a channel blank to disable it.
      </p>

      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div><div className={label}>Telegram bot token</div>
          <input className={`${input} mt-1`} value={cfg.telegramToken} onChange={(e) => setCfg({ ...cfg, telegramToken: e.target.value })} placeholder="123456:ABC..." /></div>
        <div><div className={label}>Telegram chat id</div>
          <input className={`${input} mt-1`} value={cfg.telegramChat} onChange={(e) => setCfg({ ...cfg, telegramChat: e.target.value })} placeholder="-100123456789" /></div>
        <div className="sm:col-span-2"><div className={label}>Generic webhook URL</div>
          <input className={`${input} mt-1`} value={cfg.webhookUrl} onChange={(e) => setCfg({ ...cfg, webhookUrl: e.target.value })} placeholder="https://example.com/phleet-hook" /></div>
        <div className="sm:col-span-2"><div className={label}>ntfy URL (including topic)</div>
          <input className={`${input} mt-1`} value={cfg.ntfyUrl} onChange={(e) => setCfg({ ...cfg, ntfyUrl: e.target.value })} placeholder="https://ntfy.sh/my-phleet-topic" /></div>
        <div><div className={label}>Gotify base URL</div>
          <input className={`${input} mt-1`} value={cfg.gotifyUrl} onChange={(e) => setCfg({ ...cfg, gotifyUrl: e.target.value })} placeholder="https://gotify.example.com" /></div>
        <div><div className={label}>Gotify token</div>
          <input className={`${input} mt-1`} value={cfg.gotifyToken} onChange={(e) => setCfg({ ...cfg, gotifyToken: e.target.value })} placeholder="A..." /></div>
        <div><div className={label}>Discord webhook URL</div>
          <input className={`${input} mt-1`} value={cfg.discordUrl} onChange={(e) => setCfg({ ...cfg, discordUrl: e.target.value })} placeholder="https://discord.com/api/webhooks/..." /></div>
        <div><div className={label}>Slack webhook URL</div>
          <input className={`${input} mt-1`} value={cfg.slackUrl} onChange={(e) => setCfg({ ...cfg, slackUrl: e.target.value })} placeholder="https://hooks.slack.com/services/..." /></div>

        <div><div className={label}>SMTP host</div>
          <input className={`${input} mt-1`} value={cfg.smtpHost} onChange={(e) => setCfg({ ...cfg, smtpHost: e.target.value })} placeholder="smtp.example.com" /></div>
        <div><div className={label}>SMTP port</div>
          <input type="number" className={`${input} mt-1`} value={cfg.smtpPort} onChange={(e) => setCfg({ ...cfg, smtpPort: Number(e.target.value) || 587 })} /></div>
        <div><div className={label}>SMTP user</div>
          <input className={`${input} mt-1`} value={cfg.smtpUser} onChange={(e) => setCfg({ ...cfg, smtpUser: e.target.value })} /></div>
        <div><div className={label}>SMTP password</div>
          <input type="password" className={`${input} mt-1`} value={cfg.smtpPass} onChange={(e) => setCfg({ ...cfg, smtpPass: e.target.value })} /></div>
        <div><div className={label}>Email from</div>
          <input className={`${input} mt-1`} value={cfg.emailFrom} onChange={(e) => setCfg({ ...cfg, emailFrom: e.target.value })} placeholder="phleet@example.com" /></div>
        <div><div className={label}>Email to (comma separated)</div>
          <input className={`${input} mt-1`} value={cfg.emailTo} onChange={(e) => setCfg({ ...cfg, emailTo: e.target.value })} placeholder="me@example.com" /></div>

        <div className="sm:col-span-2 border-t border-zinc-800/60 pt-3">
          <label className="flex items-center gap-2 text-[11px] text-zinc-300">
            <input type="checkbox" checked={cfg.quietHours} onChange={(e) => setCfg({ ...cfg, quietHours: e.target.checked })} />
            Quiet hours (no external notifications)
          </label>
          <div className="mt-2 grid grid-cols-2 gap-3">
            <div><div className={label}>From hour (0–23)</div>
              <input type="number" min={0} max={23} className={`${input} mt-1`} value={cfg.quietStart} onChange={(e) => setCfg({ ...cfg, quietStart: Math.max(0, Math.min(23, Number(e.target.value) || 0)) })} /></div>
            <div><div className={label}>To hour (0–23)</div>
              <input type="number" min={0} max={23} className={`${input} mt-1`} value={cfg.quietEnd} onChange={(e) => setCfg({ ...cfg, quietEnd: Math.max(0, Math.min(23, Number(e.target.value) || 0)) })} /></div>
          </div>
        </div>
        <div className="sm:col-span-2">
          <div className={label}>Maintenance window until (silences external notifications)</div>
          <div className="mt-1 flex items-center gap-2">
            <input
              type="datetime-local"
              className={input}
              value={maintenanceValue}
              onChange={(e) => {
                const ts = e.target.value ? Math.floor(new Date(e.target.value).getTime() / 1000) : 0
                setCfg({ ...cfg, maintenanceUntil: ts })
              }}
            />
            <Button type="button" size="sm" variant="ghost" onClick={() => setCfg({ ...cfg, maintenanceUntil: 0 })}>
              Clear
            </Button>
          </div>
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
