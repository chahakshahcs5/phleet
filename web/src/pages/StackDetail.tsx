import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  Database,
  ExternalLink,
  Globe,
  Layers,
  Play,
  RefreshCw,
  RotateCw,
  Server,
  Smartphone,
  Square,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ApiError, api } from '../api'
import { ConfirmDialog } from '../components/Dialog'
import { Badge, Button, Card, DeployBadge, EmptyState, PageLoader, Spinner } from '../components/ui'
import WebhookCard from '../components/WebhookCard'
import { ago } from '../lib/format'
import { aggregateStatus } from '../lib/projects'
import type { ProjectView } from '../types'

function ServiceCard({ service }: { service: ProjectView }) {
  const placements = service.placements
  const serverNames = [...new Set(placements.map((p) => p.serverName || p.serverId))].join(', ')
  const ports = placements.map((p) => p.port).filter((p) => p > 0)
  const pids = placements.map((p) => p.pid).filter(Boolean)
  const restarts = placements.reduce((n, p) => n + p.restarts, 0)
  const lastError = placements.map((p) => p.lastError).find(Boolean)
  const kind = service.kind ?? 'app'

  return (
    <Card className="flex flex-col p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate text-sm font-medium text-zinc-100">{service.name}</span>
            <Badge>{service.slug}</Badge>
            {kind !== 'app' ? <Badge tone="info">{kind}</Badge> : null}
          </div>
          {service.hostname ? (
            <a
              href={service.url}
              target="_blank"
              rel="noreferrer"
              title={service.hostname}
              className="mt-0.5 flex min-w-0 items-center gap-1 font-mono text-[10px] text-zinc-500 hover:text-emerald-300"
            >
              <Globe size={10} className="shrink-0" />
              <span className="truncate">{service.hostname}</span>
              <ExternalLink size={9} className="shrink-0" />
            </a>
          ) : (
            <div className="mt-0.5 flex min-w-0 items-center gap-1 font-mono text-[10px] text-zinc-600">
              <Server size={10} className="shrink-0" /> no public URL
            </div>
          )}
        </div>
        <DeployBadge status={aggregateStatus(service)} />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2 text-[10px] text-zinc-500">
        <span className="flex items-center gap-1 truncate" title={serverNames}>
          <Smartphone size={10} /> {serverNames || 'not placed'}
        </span>
        <span className="flex items-center gap-1 truncate">
          <Server size={10} /> {ports.length ? `port ${ports.join(', ')}` : 'no port'}
        </span>
        <span className="truncate">pid {pids.length ? pids.join(', ') : '—'}</span>
        <span className="truncate">restarts {restarts}</span>
        <span className="col-span-2 truncate" title={service.latest?.startedAt ? ago(service.latest.startedAt) : ''}>
          deploy {service.latest ? ago(service.latest.startedAt) : 'never'}
        </span>
      </div>

      {lastError ? (
        <div className="mt-2 rounded-lg border border-red-900/40 bg-red-500/5 px-2 py-1.5 text-[10px] text-red-300">
          {lastError}
        </div>
      ) : null}

      <div className="mt-3 flex items-center justify-between border-t border-zinc-800/70 pt-2.5">
        <span className="text-[10px] text-zinc-600">
          {kind === 'static' ? 'static files' : kind === 'worker' ? 'worker (no port)' : service.healthPath || '/'}
        </span>
        <Link
          to={`/projects/${service.id}`}
          className="inline-flex items-center gap-1 text-[10px] text-zinc-400 hover:text-zinc-100"
        >
          open service <span aria-hidden>→</span>
        </Link>
      </div>
    </Card>
  )
}

export default function StackDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [addonWarning, setAddonWarning] = useState<{
    addons: { id: string; name: string }[]
    shared: { id: string; name: string }[]
  } | null>(null)

  const query = useQuery({
    queryKey: ['project', id],
    queryFn: () => api.project(id),
    enabled: !!id,
    refetchInterval: 5000,
  })

  const deploy = useMutation({
    mutationFn: () => api.deployProject(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
  const action = useMutation({
    mutationFn: (a: 'start' | 'stop' | 'restart') => api.projectAction(id, a),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
  const remove = useMutation({
    mutationFn: (deleteAddons: boolean) => api.deleteProject(id, { deleteAddons }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      navigate('/projects')
    },
    onError: (err) => {
      if (!(err instanceof ApiError) || err.status !== 409) return
      const data = err.data as {
        code?: string
        addons?: { id: string; name: string }[]
        shared?: { id: string; name: string }[]
      }
      if (data?.code !== 'addons_attached') return
      setDeleteOpen(false)
      setAddonWarning({ addons: data.addons ?? [], shared: data.shared ?? [] })
    },
  })

  if (query.isLoading) return <PageLoader label="Loading stack" />
  if (query.isError || !query.data) {
    return (
      <div className="p-8">
        <div className="text-xs text-red-300">{String(query.error || 'stack not found')}</div>
        <Link to="/projects" className="mt-3 inline-block text-xs text-zinc-400 underline">
          back to projects
        </Link>
      </div>
    )
  }

  const project = query.data.project
  const services = project.services ?? []
  const busy = deploy.isPending || action.isPending || remove.isPending
  const primary = services[0]
  const mutError = deploy.error || action.error || (addonWarning ? undefined : remove.error)

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
      <Link to="/projects" className="mb-4 inline-flex items-center gap-1.5 text-[11px] text-zinc-500 hover:text-zinc-200">
        <ArrowLeft size={12} /> projects
      </Link>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold tracking-tight text-zinc-100">{project.name}</h1>
            <Badge tone="info">
              <Layers size={10} /> stack · {services.length} services
            </Badge>
            <DeployBadge status={aggregateStatus(project)} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-zinc-500">
            {primary?.hostname || project.hostname ? (
              <a
                href={primary?.url || project.url}
                target="_blank"
                rel="noreferrer"
                title={primary?.hostname || project.hostname}
                className="inline-flex min-w-0 items-center gap-1 font-mono hover:text-emerald-300"
              >
                <Globe size={10} className="shrink-0" />
                <span className="truncate">{primary?.hostname || project.hostname}</span>
                <ExternalLink size={9} className="shrink-0" />
              </a>
            ) : (
              <span className="inline-flex items-center gap-1 font-mono text-zinc-600">
                <Server size={10} className="shrink-0" /> no public URL
              </span>
            )}
            <span>{services.length} services · one phone</span>
            <span className="font-mono">{project.branch || 'main'}</span>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-1.5">
          <Button size="sm" variant="primary" disabled={busy} onClick={() => deploy.mutate()}>
            {deploy.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Play size={12} />}
            Deploy stack
          </Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => action.mutate('restart')}>
            {action.isPending ? <Spinner className="h-3 w-3" /> : <RotateCw size={12} />} Restart
          </Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => action.mutate('stop')}>
            <Square size={12} /> Stop
          </Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => query.refetch()}>
            <RefreshCw size={12} /> Refresh
          </Button>
          <Button size="sm" variant="danger" disabled={busy} onClick={() => setDeleteOpen(true)}>
            <Trash2 size={12} />
          </Button>
        </div>
      </div>

      {mutError ? (
        <div className="mt-3 rounded-lg border border-red-900/40 bg-red-500/5 px-3 py-2 text-[11px] text-red-300">
          {String(mutError)}
        </div>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2 text-[10px] text-zinc-500">
        <span className="inline-flex items-center gap-1 rounded-md bg-zinc-900/60 px-2 py-1">
          <Smartphone size={10} /> all services pinned to the same phone
        </span>
        <span className="inline-flex items-center gap-1 rounded-md bg-zinc-900/60 px-2 py-1">
          <Globe size={10} /> each service has its own subdomain
        </span>
        <span className="inline-flex items-center gap-1 rounded-md bg-zinc-900/60 px-2 py-1">
          <Server size={10} /> service URLs are injected as <span className="font-mono">PHLEET_SERVICE_*_URL</span>
        </span>
      </div>

      {services.length === 0 ? (
        <div className="mt-6">
          <EmptyState icon={<Layers size={22} />} title="No services in this stack">
            This stack has no child services. Delete it and create a new multi-service stack.
          </EmptyState>
        </div>
      ) : (
        <div className="mt-5 grid gap-3 md:grid-cols-2">
          {services.map((service) => (
            <ServiceCard key={service.id} service={service} />
          ))}
        </div>
      )}

      <Card className="mt-5 p-4">
        <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Stack notes</div>
        <p className="mt-1 text-[11px] leading-relaxed text-zinc-500">
          Each service is a normal phleet project underneath: it has its own release, process, port (unless it
          is a worker), health check and logs. Open a service to edit its commands, environment or view logs.
          The stack shares the repository, branch and encrypted environment; per-service values win at deploy time.
        </p>
        {Object.keys(project.env ?? {}).length > 0 ? (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {Object.keys(project.env ?? {}).map((key) => (
              <span key={key} className="rounded-md bg-zinc-800/70 px-2 py-1 font-mono text-[10px] text-zinc-400">
                {key}
              </span>
            ))}
          </div>
        ) : null}
        {project.addons?.length ? (
          <div className="mt-3 border-t border-zinc-800/70 pt-3">
            <div className="flex items-center gap-1.5 text-[10px] tracking-widest text-zinc-500 uppercase">
              <Database size={11} /> Addons
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              {project.addons.map((a) => (
                <Link
                  key={a.id}
                  to={`/addons/${a.id}`}
                  className="flex items-center gap-1.5 rounded-md bg-zinc-900/60 px-2 py-1 text-[10px] text-zinc-300 hover:text-emerald-300"
                >
                  {a.name}
                  <DeployBadge status={a.status} />
                </Link>
              ))}
            </div>
            <p className="mt-1 text-[10px] text-zinc-600">
              Available to every service over loopback as <span className="font-mono">DATABASE_URL</span>.
            </p>
          </div>
        ) : null}
      </Card>

      {project.sourceType === 'git' ? (
        <div className="mt-5">
          <WebhookCard projectId={project.id} branch={project.branch} />
        </div>
      ) : null}

      <ConfirmDialog
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        title={`Delete ${project.name}?`}
        description="This stops every service, removes the stack and forgets all deployment history. Attached databases are handled next. This cannot be undone."
        confirmLabel="Delete stack"
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate(false)}
      />

      <ConfirmDialog
        open={!!addonWarning}
        onClose={() => setAddonWarning(null)}
        title={`Delete ${project.name} and its databases?`}
        description={
          <div className="space-y-2">
            <p>This stack owns these data services:</p>
            <ul className="list-disc pl-4 font-mono text-[11px] text-zinc-300">
              {addonWarning?.addons.map((a) => (
                <li key={a.id}>{a.name}</li>
              ))}
            </ul>
            <p>
              Deleting the stack also deletes them and <strong>all of their data</strong>. Take a
              backup first if you need it.
            </p>
            {addonWarning && addonWarning.shared.length > 0 ? (
              <p>
                These are also used by another project and will be kept:{' '}
                <span className="font-mono">{addonWarning.shared.map((a) => a.name).join(', ')}</span>.
              </p>
            ) : null}
          </div>
        }
        confirmLabel="Delete stack and databases"
        danger
        busy={remove.isPending}
        onConfirm={() => {
          remove.mutate(true)
          setAddonWarning(null)
        }}
      />
    </div>
  )
}
