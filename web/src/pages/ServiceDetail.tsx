import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  ExternalLink,
  HardDrive,
  Pencil,
  Play,
  RotateCw,
  Save,
  Square,
  Terminal,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../api'
import { ConfirmDialog, PromptDialog } from '../components/Dialog'
import { Badge, Button, Card, DeployBadge, KeyValue, PageLoader, Spinner, cn } from '../components/ui'
import { ago, bytes, time } from '../lib/format'
import type { Backup } from '../types'

type Tab = 'overview' | 'logs' | 'backups'

function serviceURL(raw?: string) {
  if (!raw) return ''
  try {
    const u = new URL(raw)
    if (u.hostname.endsWith('.localhost') && !u.port) u.port = window.location.port || '80'
    return u.toString()
  } catch {
    return raw
  }
}

export default function ServiceDetail() {
  const { id = '' } = useParams()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [tab, setTab] = useState<Tab>('overview')
  const [renameOpen, setRenameOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [restoreId, setRestoreId] = useState('')

  const query = useQuery({
    queryKey: ['service', id],
    queryFn: () => api.service(id),
    refetchInterval: 8000,
    enabled: !!id,
  })
  const logs = useQuery({
    queryKey: ['service-logs', id],
    queryFn: () => api.serviceLogs(id, 300),
    refetchInterval: 5000,
    enabled: !!id && tab === 'logs',
  })
  const backups = useQuery({
    queryKey: ['service-backups', id],
    queryFn: () => api.serviceBackups(id),
    refetchInterval: 10000,
    enabled: !!id,
  })

  const action = useMutation({
    mutationFn: (a: 'start' | 'stop' | 'restart') => api.serviceAction(id, a),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['service', id] })
      qc.invalidateQueries({ queryKey: ['services'] })
    },
  })
  const rename = useMutation({
    mutationFn: (name: string) => api.renameService(id, name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['service', id] })
      qc.invalidateQueries({ queryKey: ['services'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => api.deleteService(id, { purge: true }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['services'] })
      navigate('/services')
    },
  })
  const backup = useMutation({
    mutationFn: () => api.createServiceBackup(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['service-backups', id] }),
  })
  const restore = useMutation({
    mutationFn: (backupId: string) => api.restoreServiceBackup(id, backupId),
    onSuccess: () => {
      setRestoreId('')
      qc.invalidateQueries({ queryKey: ['service', id] })
      qc.invalidateQueries({ queryKey: ['service-backups', id] })
    },
  })

  if (query.isLoading) return <PageLoader label="Loading service" />
  if (query.isError || !query.data) {
    return (
      <div className="p-8">
        <Link to="/services" className="text-xs text-zinc-500 hover:text-zinc-300">
          ← back to services
        </Link>
        <div className="mt-4 text-sm text-red-400">{String((query.error as Error)?.message || 'service not found')}</div>
      </div>
    )
  }

  const service = query.data.service
  const placement = service.placements?.find((p) => p.role === 'primary' || !p.role) ?? service.placements?.[0]
  const url = serviceURL(service.url)
  const busy = action.isPending
  const backupList = backups.data?.backups ?? []

  return (
    <div className="flex min-h-full flex-col">
      <div className="border-b border-zinc-800/70 bg-zinc-950/60 px-4 pt-4 md:px-8">
        <Link to="/services" className="inline-flex items-center gap-1 text-[11px] text-zinc-500 hover:text-zinc-300">
          <ArrowLeft size={12} /> services
        </Link>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="truncate text-lg font-semibold tracking-tight text-zinc-100">{service.name}</h1>
              <Badge tone="info">{service.engineName}</Badge>
              {service.version ? <Badge tone="neutral">{service.version}</Badge> : null}
              <DeployBadge status={service.status} />
              {!placement?.online ? <Badge tone="bad">phone offline</Badge> : null}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-zinc-500">
              <span className="font-mono">{service.hostname || service.slug}</span>
              <span>{placement?.serverName || service.pinnedServer}</span>
              <span>port {service.port}</span>
              {url ? (
                <a href={url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-emerald-300/80 hover:text-emerald-200">
                  open <ExternalLink size={10} />
                </a>
              ) : null}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => action.mutate('restart')}>
              <RotateCw size={12} /> Restart
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy || service.status === 'stopped'}
              onClick={() => action.mutate('stop')}
            >
              <Square size={11} /> Stop
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy || service.status !== 'stopped'}
              onClick={() => action.mutate('start')}
            >
              <Play size={12} /> Start
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setRenameOpen(true)}>
              <Pencil size={12} /> Rename
            </Button>
            <Button size="sm" variant="primary" disabled={!placement?.online || backup.isPending} onClick={() => backup.mutate()}>
              {backup.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
              Backup now
            </Button>
          </div>
        </div>

        <div className="mt-4 flex gap-1 overflow-x-auto">
          {(
            [
              { id: 'overview', label: 'Overview', icon: <HardDrive size={14} /> },
              { id: 'logs', label: 'Logs', icon: <Terminal size={14} /> },
              { id: 'backups', label: 'Backups', icon: <Save size={14} /> },
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
      </div>

      <div className="min-h-0 flex-1 px-4 py-5 md:px-8">
        {service.lastError ? (
          <div className="mb-4 rounded-lg border border-red-900/30 bg-red-500/5 px-3 py-2 text-[11px] text-red-300">
            {service.lastError}
          </div>
        ) : null}
        {action.isError ? <div className="mb-4 text-[11px] text-red-400">{String(action.error)}</div> : null}

        {tab === 'overview' ? (
          <div className="grid gap-4 lg:grid-cols-[1fr_360px]">
            <Card className="p-4">
              <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Service</div>
              <div className="mt-2 divide-y divide-zinc-800/60">
                <KeyValue label="URL">
                  {url ? (
                    <a href={url} target="_blank" rel="noreferrer" className="font-mono text-emerald-300/80 hover:text-emerald-200">
                      {url}
                    </a>
                  ) : (
                    '—'
                  )}
                </KeyValue>
                <KeyValue label="Phone">{placement?.serverName || service.pinnedServer || '—'}</KeyValue>
                <KeyValue label="Protocol port">{service.port || '—'}</KeyValue>
                <KeyValue label="Bridge on this PC">
                  {service.bridgePort ? `127.0.0.1:${service.bridgePort}` : '—'}
                </KeyValue>
                <KeyValue label="Data directory">
                  <span className="font-mono text-[10px]">{service.dataDir || '—'}</span>
                </KeyValue>
                <KeyValue label="Version">{service.version || '—'}</KeyValue>
                <KeyValue label="Category">{service.category || '—'}</KeyValue>
                <KeyValue label="Created">{time(service.createdAt)}</KeyValue>
              </div>
            </Card>

            <div className="space-y-3">
              <Card className="p-4">
                <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">About</div>
                <p className="mt-2 text-[11px] leading-relaxed text-zinc-500">
                  {service.website || service.source ? (
                    <>
                      {service.website ? (
                        <a href={service.website} target="_blank" rel="noreferrer" className="text-emerald-300/80 hover:text-emerald-200">
                          {service.website}
                        </a>
                      ) : null}
                      {service.source ? (
                        <>
                          {' · '}
                          <a href={service.source} target="_blank" rel="noreferrer" className="text-emerald-300/80 hover:text-emerald-200">
                            source
                          </a>
                        </>
                      ) : null}
                    </>
                  ) : (
                    'Prebuilt self-hosted service.'
                  )}
                </p>
                {placement ? (
                  <div className="mt-3 divide-y divide-zinc-800/60">
                    <KeyValue label="Placement">{placement.status}</KeyValue>
                    <KeyValue label="PID">{placement.pid || '—'}</KeyValue>
                    <KeyValue label="Restarts">{placement.restarts}</KeyValue>
                  </div>
                ) : null}
              </Card>

              <Card className="border-red-900/40 p-4">
                <div className="text-[10px] font-medium tracking-widest text-red-400/80 uppercase">Danger zone</div>
                <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">
                  Stops the service and deletes its data directory on the phone. Back up first if the data matters.
                </p>
                <Button variant="danger" size="sm" className="mt-3 w-full" onClick={() => setDeleteOpen(true)}>
                  <Trash2 size={12} /> Delete service
                </Button>
              </Card>
            </div>
          </div>
        ) : null}

        {tab === 'logs' ? (
          <Card className="p-4">
            <div className="flex items-center justify-between">
              <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Service logs</div>
              <span className="text-[10px] text-zinc-600">
                {logs.data?.logs ? `${logs.data.logs.split('\n').length} lines` : '0 lines'}
                {logs.data?.truncated ? ' · truncated' : ''}
              </span>
            </div>
            <pre className="mt-2 max-h-[65vh] overflow-auto rounded-lg bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300 ring-1 ring-zinc-800">
              {logs.data?.logs || 'no log output yet'}
            </pre>
          </Card>
        ) : null}

        {tab === 'backups' ? (
          <Card className="p-4">
            <div className="flex items-center justify-between">
              <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Backups</div>
              <Button size="sm" disabled={!placement?.online || backup.isPending} onClick={() => backup.mutate()}>
                {backup.isPending ? <Spinner className="h-3 w-3" /> : <Save size={12} />} Backup now
              </Button>
            </div>
            <div className="mt-3 space-y-1.5">
              {backupList.length === 0 ? (
                <div className="rounded-lg border border-dashed border-zinc-800 px-3 py-6 text-center text-[11px] text-zinc-600">
                  No backups yet.
                </div>
              ) : (
                backupList.map((b) => (
                  <BackupRow
                    key={b.id}
                    backup={b}
                    onRestore={() => setRestoreId(b.id)}
                    restoring={restore.isPending && restoreId === b.id}
                  />
                ))
              )}
            </div>
            {backup.isError ? <div className="mt-2 text-[11px] text-red-400">{String(backup.error)}</div> : null}
            {restore.isError ? <div className="mt-2 text-[11px] text-red-400">{String(restore.error)}</div> : null}
          </Card>
        ) : null}
      </div>

      <PromptDialog
        open={renameOpen}
        onClose={() => setRenameOpen(false)}
        title="Rename service"
        description={<>Give <span className="font-mono text-zinc-400">{service.name}</span> a new display name.</>}
        label="Service name"
        initialValue={service.name}
        confirmLabel="Save name"
        onSubmit={(name) => {
          if (name.trim() && name !== service.name) rename.mutate(name.trim())
          setRenameOpen(false)
        }}
      />

      <ConfirmDialog
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        title="Delete this service?"
        description="The process is stopped and its data directory is deleted on the phone. This cannot be undone."
        confirmLabel="Delete service"
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
      />

      <ConfirmDialog
        open={!!restoreId}
        onClose={() => setRestoreId('')}
        title="Restore this backup?"
        description="The service is stopped while the backup is applied. The current data is replaced."
        confirmLabel="Restore"
        danger
        busy={restore.isPending}
        onConfirm={() => restoreId && restore.mutate(restoreId)}
      />
    </div>
  )
}

function BackupRow({
  backup,
  onRestore,
  restoring,
}: {
  backup: Backup
  onRestore: () => void
  restoring: boolean
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-zinc-900/60 px-3 py-2">
      <div className="min-w-0">
        <div className="flex items-center gap-2 text-[11px] text-zinc-300">
          <span className="font-mono">{backup.id.slice(0, 8)}</span>
          <Badge tone={backup.status === 'complete' ? 'good' : backup.status === 'failed' ? 'bad' : 'neutral'}>
            {backup.status}
          </Badge>
          <span className="text-zinc-600">{bytes(backup.size)}</span>
        </div>
        <div className="text-[10px] text-zinc-600">
          {ago(backup.createdAt)} · {backup.kind}
          {backup.error ? ` · ${backup.error}` : ''}
        </div>
      </div>
      <div className="flex items-center gap-1">
        {backup.status === 'complete' ? (
          <>
            <a
              href={`/api/services/${backup.addonId}/backups/${backup.id}/download`}
              className="rounded-md px-2 py-1 text-[10px] text-zinc-400 hover:bg-zinc-800/70 hover:text-zinc-200"
            >
              Download
            </a>
            <Button size="sm" variant="ghost" disabled={restoring} onClick={onRestore}>
              {restoring ? <Spinner className="h-3 w-3" /> : null} Restore
            </Button>
          </>
        ) : null}
      </div>
    </div>
  )
}
