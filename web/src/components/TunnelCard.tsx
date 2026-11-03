import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Cloud, Play, RotateCw, Square } from 'lucide-react'
import { useState } from 'react'
import { api } from '../api'
import { ConfirmDialog } from './Dialog'
import { Badge, Button, Card, KeyValue, Spinner } from './ui'

export default function TunnelCard() {
  const qc = useQueryClient()
  const [stopOpen, setStopOpen] = useState(false)
  const query = useQuery({
    queryKey: ['edge-tunnel'],
    queryFn: api.tunnelStatus,
    refetchInterval: 10000,
  })
  const action = useMutation({
    mutationFn: (a: 'start' | 'stop' | 'restart') => api.tunnelAction(a),
    onSuccess: (data) => {
      qc.setQueryData(['edge-tunnel'], data)
      qc.invalidateQueries({ queryKey: ['edge-tunnel'] })
    },
    onError: () => {
      qc.invalidateQueries({ queryKey: ['edge-tunnel'] })
    },
  })

  const st = query.data
  const serviceState = st?.serviceStatus || ''
  const transitioning = serviceState === 'Starting' || serviceState === 'Stopping'
  const busy = query.isLoading || action.isPending || transitioning
  const running = !!st?.running
  const service = !!st?.serviceInstalled
  const configured = !!st?.configured
  const canStart = !!st && !running && !transitioning && (service || configured)
  const canRestart = !!st && !transitioning && (service || configured)
  const canStop = !!st && running && !transitioning
  const statusLabel = transitioning
    ? serviceState
    : running
      ? 'Running'
      : service
        ? 'Stopped'
        : 'Not running'
  const statusTone = running
    ? 'good'
    : transitioning
      ? 'warn'
      : service
        ? 'bad'
        : 'neutral'
  const connector = service
    ? 'Windows service'
    : st?.mode === 'process'
      ? 'Supervised process'
      : 'No connector'
  const error = st?.error || (action.error ? String(action.error) : '')

  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
            <Cloud size={12} /> Edge tunnel
          </div>
          <p className="mt-1 text-[11px] text-zinc-500">
            The public connector for the panel and every project hostname.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Badge tone={statusTone}>{statusLabel}</Badge>
          {st?.startType ? <span className="text-[10px] text-zinc-600">{st.startType}</span> : null}
        </div>
      </div>

      <div className="mt-3 grid gap-x-8 sm:grid-cols-2">
        <div className="divide-y divide-zinc-800/60">
          <KeyValue label="Public URL">
            {st?.publicUrl ? (
              <a
                href={st.publicUrl}
                target="_blank"
                rel="noreferrer"
                className="font-mono text-emerald-300 hover:underline"
              >
                {st.publicUrl.replace(/^https?:\/\//, '')}
              </a>
            ) : (
              <Badge tone="warn">not configured</Badge>
            )}
          </KeyValue>
          <KeyValue label="Tunnel">
            <span className="font-mono">{st?.tunnelName || st?.tunnelId || '—'}</span>
          </KeyValue>
          <KeyValue label="Connector">{connector}</KeyValue>
        </div>
        <div className="divide-y divide-zinc-800/60">
          <KeyValue label="Mode">{service ? 'Service control' : st?.mode === 'process' ? 'Child process' : '—'}</KeyValue>
          <KeyValue label="Process">{st?.pid ? <span className="font-mono">pid {st.pid}</span> : '—'}</KeyValue>
          <KeyValue label="Log">
            {service ? (
              <span className="text-[10px] text-zinc-500">Windows service log</span>
            ) : (
              <span className="truncate font-mono text-[10px]" title={st?.logPath}>
                {st?.logPath || '—'}
              </span>
            )}
          </KeyValue>
        </div>
      </div>

      {error ? (
        <div className="mt-3 rounded-lg border border-red-900/40 bg-red-500/5 px-3 py-2 text-[10px] text-red-300">
          {error}
        </div>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-zinc-800/70 pt-3">
        <p className="max-w-xl text-[10px] leading-relaxed text-zinc-600">
          {service
            ? 'Starts or stops the installed cloudflared Windows service. The tunnel token stays in cloudflare.json.'
            : configured
              ? 'The Windows service is not installed. Start launches cloudflared with the saved tunnel token and supervises it.'
              : 'Run phleet setup-cloudflare first, then the tunnel can be started here.'}
        </p>
        <div className="flex items-center gap-1.5">
          <Button
            size="sm"
            variant="primary"
            disabled={!canStart || busy}
            onClick={() => action.mutate('start')}
          >
            {action.isPending && action.variables === 'start' ? <Spinner className="h-3 w-3" /> : <Play size={12} />}
            {service ? 'Start' : 'Start connector'}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={!canRestart || busy}
            onClick={() => action.mutate('restart')}
          >
            {action.isPending && action.variables === 'restart' ? <Spinner className="h-3 w-3" /> : <RotateCw size={12} />}
            Restart
          </Button>
          <Button
            size="sm"
            variant="danger"
            disabled={!canStop || busy}
            onClick={() => setStopOpen(true)}
          >
            <Square size={11} /> Stop
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={stopOpen}
        onClose={() => setStopOpen(false)}
        title="Stop the public tunnel?"
        description="The panel and every project hostname will only be reachable on loopback or LAN until the connector is started again."
        confirmLabel="Stop tunnel"
        danger
        busy={action.isPending}
        onConfirm={() => {
          action.mutate('stop')
          setStopOpen(false)
        }}
      />
    </Card>
  )
}
