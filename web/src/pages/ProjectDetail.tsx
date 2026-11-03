import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  Database,
  ExternalLink,
  FileText,
  GitBranch,
  Globe,
  History,
  Layers,
  Play,
  RefreshCw,
  RotateCw,
  Save,
  Server,
  Settings as SettingsIcon,
  Shuffle,
  Smartphone,
  Square,
  Trash2,
  X,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../api'
import type { Deployment, ProjectView } from '../types'
import { ConfirmDialog } from '../components/Dialog'
import { Badge, Button, Card, DeployBadge, EmptyState, KeyValue, Spinner, cn } from '../components/ui'
import { ago, bytes, time } from '../lib/format'
import { envToString, parseEnv } from '../lib/env'
import { aggregateStatus, onlineReplicas } from '../lib/projects'
import { zipSelection } from '../lib/zip'

type Tab = 'overview' | 'deployments' | 'logs' | 'settings'

const NON_TERMINAL = new Set(['queued', 'building', 'starting', 'health_check'])

function duration(d: Deployment): string {
  if (!d.finishedAt) return NON_TERMINAL.has(d.status) ? 'running…' : '—'
  const s = Math.max(0, d.finishedAt - d.startedAt)
  if (s < 60) return `${s}s`
  return `${Math.floor(s / 60)}m ${s % 60}s`
}

export default function ProjectDetail() {
  const { id = '' } = useParams()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [tab, setTab] = useState<Tab>('overview')
  const [selected, setSelected] = useState<string | null>(null)
  const [logServer, setLogServer] = useState('')
  const [follow, setFollow] = useState(true)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const logRef = useRef<HTMLPreElement>(null)
  const deployLogRef = useRef<HTMLPreElement>(null)

  const query = useQuery({
    queryKey: ['project', id],
    queryFn: () => api.project(id),
    enabled: !!id,
    refetchInterval: 5000,
  })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 })
  const deployments = useQuery({
    queryKey: ['deployments', id],
    queryFn: () => api.deployments(id),
    enabled: !!id,
    refetchInterval: 5000,
  })
  const detail = useQuery({
    queryKey: ['deployment', selected],
    queryFn: () => api.deployment(selected!),
    enabled: !!selected,
    refetchInterval: (q) => {
      const d = q.state.data
      return d && !NON_TERMINAL.has(d.deployment.status) ? false : 1500
    },
  })
  const appLogs = useQuery({
    queryKey: ['applogs', id, logServer],
    queryFn: () => api.appLogs(id, logServer || undefined),
    enabled: !!id && tab === 'logs' && !!logServer,
    refetchInterval: tab === 'logs' ? 3000 : false,
    retry: false,
  })

  const project = query.data?.project
  const anyOnline = project?.placements.some((p) => p.online) ?? false

  useEffect(() => {
    if (!selected && deployments.data?.deployments.length) {
      setSelected(deployments.data.deployments[0].id)
    }
  }, [deployments.data, selected])

  useEffect(() => {
    if (!logServer && project?.placements.length) {
      const running = project.placements.find((p) => p.online && p.status === 'running')
      setLogServer((running || project.placements[0]).serverId)
    }
  }, [project?.id, project?.placements.length, logServer])

  useEffect(() => {
    if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight
  }, [appLogs.dataUpdatedAt, follow])

  useEffect(() => {
    if (deployLogRef.current) deployLogRef.current.scrollTop = deployLogRef.current.scrollHeight
  }, [detail.dataUpdatedAt])

  const deploy = useMutation({
    mutationFn: () => api.deployProject(id),
    onSuccess: (res) => {
      if (res.deployments[0]) setSelected(res.deployments[0].id)
      setTab('deployments')
      qc.invalidateQueries({ queryKey: ['deployments', id] })
      qc.invalidateQueries({ queryKey: ['project', id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
  const action = useMutation({
    mutationFn: (a: 'start' | 'stop' | 'restart' | 'rollback') => api.projectAction(id, a),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', id] })
      qc.invalidateQueries({ queryKey: ['deployments', id] })
    },
  })
  const rebalance = useMutation({
    mutationFn: () => api.rebalance(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
  const remove = useMutation({
    mutationFn: () => api.deleteProject(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      navigate('/projects')
    },
  })

  if (query.isLoading) return <div className="p-8 text-xs text-zinc-500">loading project…</div>
  if (query.isError || !project) {
    return (
      <div className="p-8">
        <Link to="/projects" className="text-xs text-zinc-500 hover:text-zinc-300">
          ← projects
        </Link>
        <div className="mt-4 text-sm text-red-400">{String((query.error as Error)?.message || 'project not found')}</div>
      </div>
    )
  }

  const status = aggregateStatus(project)
  const latestBusy = project.latest ? NON_TERMINAL.has(project.latest.status) : false
  const anyServerOnline = servers.data?.servers.some((s) => s.online) ?? false
  const hasSource = project.sourceType === 'upload' ? project.sourceSize > 0 : !!project.repo
  // A phone without Termux/userland can be watched but cannot build or run an
  // app. Do not offer a deploy that the control plane will reject.
  const byId = new Map((servers.data?.servers ?? []).map((s) => [s.id, s]))
  const placementCaps = project.placements.map((p) => byId.get(p.serverId)?.caps)
  const noUserland =
    placementCaps.length > 0
      ? placementCaps.every((c) => c?.userland === false)
      : (servers.data?.servers.filter((s) => s.online).length ?? 0) > 0 &&
        servers.data!.servers.filter((s) => s.online).every((s) => s.caps.userland === false)
  const canDeploy =
    hasSource && !!project.startCmd && anyServerOnline && !noUserland && !deploy.isPending && !latestBusy
  const canAct = project.placements.length > 0 && anyOnline && !action.isPending
  const serverNames = [...new Set(project.placements.map((p) => p.serverName || p.serverId))].join(', ')
  const failedActions = action.data?.results.filter((r) => !r.ok) ?? []

  return (
    <div className="flex min-h-full flex-col">
      <div className="border-b border-zinc-800/70 bg-zinc-950/60 px-4 pt-4 md:px-8">
        <div className="flex items-center gap-1 text-[11px] text-zinc-500">
          <Link to="/projects" className="inline-flex items-center gap-1 hover:text-zinc-300">
            <ArrowLeft size={12} /> projects
          </Link>
          {project.parentId ? (
            <>
              <span className="text-zinc-700">/</span>
              <Link to={`/stacks/${project.parentId}`} className="hover:text-zinc-300">
                stack
              </Link>
            </>
          ) : null}
        </div>
        <div className="mt-2 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="truncate text-lg font-semibold tracking-tight text-zinc-100">{project.name}</h1>
              <DeployBadge status={status} />
              {project.latest ? <DeployBadge status={project.latest.status} /> : null}
              {!anyOnline && project.placements.length > 0 ? <Badge tone="bad">replicas offline</Badge> : null}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-zinc-500">
              <a
                href={project.url}
                target="_blank"
                rel="noreferrer"
                className="flex items-center gap-1 font-mono text-zinc-400 hover:text-emerald-300"
              >
                <Globe size={10} /> {project.hostname} <ExternalLink size={9} />
              </a>
              <span className="flex items-center gap-1">
                <Layers size={10} /> {onlineReplicas(project)}/{project.replicas} replicas online
              </span>
              <span className="flex items-center gap-1 truncate" title={serverNames}>
                <Smartphone size={10} /> {serverNames || 'waiting for a phone'}
              </span>
              <span className="flex items-center gap-1">
                <GitBranch size={10} /> {project.sourceType === 'upload' ? `${bytes(project.sourceSize)} uploaded` : project.branch}
              </span>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button variant="primary" size="sm" disabled={!canDeploy} onClick={() => deploy.mutate()}>
              {deploy.isPending || latestBusy ? (
                <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" />
              ) : (
                <Play size={12} />
              )}
              {latestBusy ? 'Deploying…' : 'Deploy'}
            </Button>
            <Button size="sm" disabled={!canAct} onClick={() => action.mutate('restart')}>
              <RotateCw size={12} /> Restart
            </Button>
            <Button size="sm" disabled={!canAct || status === 'stopped'} onClick={() => action.mutate('stop')}>
              <Square size={11} /> Stop
            </Button>
            <Button size="sm" disabled={!canAct || status !== 'stopped'} onClick={() => action.mutate('start')}>
              <Play size={12} /> Start
            </Button>
            <Button size="sm" variant="ghost" disabled={!canAct} onClick={() => action.mutate('rollback')}>
              <History size={12} /> Rollback
            </Button>
            <Button size="sm" variant="ghost" disabled={rebalance.isPending} onClick={() => rebalance.mutate()} title="Re-run placement and failover">
              <Shuffle size={12} /> Rebalance
            </Button>
          </div>
        </div>
        {deploy.isError ? <div className="mt-2 text-[11px] text-red-400">{String(deploy.error)}</div> : null}
        {noUserland ? (
          <div className="mt-2 text-[10px] text-amber-300">
            {project.placements.length > 0 ? 'Every replica is on a' : 'Every online phone is a'} monitoring-only phone
            (no Termux/userland), so it cannot build or run this project. Install Termux there or pin another phone.
          </div>
        ) : null}
        {deploy.data?.skipped.length ? (
          <div className="mt-2 text-[10px] text-amber-300">skipped offline replicas: {deploy.data.skipped.join(', ')}</div>
        ) : null}
        {action.isError ? <div className="mt-2 text-[11px] text-red-400">{String(action.error)}</div> : null}
        {failedActions.length ? (
          <div className="mt-2 text-[10px] text-amber-300">
            some replicas rejected the action: {failedActions.map((r) => `${r.serverId} (${r.error})`).join(', ')}
          </div>
        ) : null}
        {project.placements.length === 0 ? (
          <div className="mt-2 text-[10px] text-zinc-500">
            No eligible phone yet. Deploy creates replicas on online phones that match the selector; the reconciler
            retries automatically.
          </div>
        ) : null}
        {project.sourceType === 'upload' && project.sourceSize === 0 ? (
          <div className="mt-2 rounded-lg border border-amber-900/40 bg-amber-500/5 px-3 py-2 text-[10px] text-amber-300">
            No source uploaded yet. Open Settings → Source and upload a folder before deploying.
          </div>
        ) : null}

        <div className="mt-4 flex gap-1 overflow-x-auto">
          {(
            [
              { id: 'overview', label: 'Overview', icon: <Globe size={14} /> },
              { id: 'deployments', label: 'Deployments', icon: <History size={14} /> },
              { id: 'logs', label: 'Logs', icon: <FileText size={14} /> },
              { id: 'settings', label: 'Settings', icon: <SettingsIcon size={14} /> },
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
        {tab === 'overview' ? (
          <Overview project={project} onDeploy={() => deploy.mutate()} canDeploy={canDeploy} />
        ) : null}
        {tab === 'deployments' ? (
          <div className="grid gap-3 lg:grid-cols-[300px_minmax(0,1fr)]">
            <Card className="overflow-hidden">
              <div className="border-b border-zinc-800/70 px-3 py-2 text-[10px] tracking-wider text-zinc-500 uppercase">
                history
              </div>
              <div className="max-h-[calc(100vh-330px)] divide-y divide-zinc-800/50 overflow-y-auto">
                {(deployments.data?.deployments ?? []).map((d) => (
                  <button
                    key={d.id}
                    onClick={() => setSelected(d.id)}
                    className={cn(
                      'w-full px-3 py-2.5 text-left transition-colors hover:bg-zinc-800/40',
                      selected === d.id && 'bg-emerald-400/5',
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <DeployBadge status={d.status} />
                      <span className="text-[10px] text-zinc-600">{ago(d.startedAt)}</span>
                    </div>
                    <div className="mt-1 flex items-center justify-between font-mono text-[10px] text-zinc-500">
                      <span className="truncate">{d.serverId.slice(0, 8)}</span>
                      <span>{duration(d)}</span>
                    </div>
                  </button>
                ))}
                {(deployments.data?.deployments ?? []).length === 0 ? (
                  <div className="p-4 text-center text-[11px] text-zinc-600">no deploys yet</div>
                ) : null}
              </div>
            </Card>

            <Card className="flex min-h-[360px] flex-col overflow-hidden">
              {detail.data ? (
                <>
                  <div className="flex flex-wrap items-center justify-between gap-2 border-b border-zinc-800/70 px-3 py-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <DeployBadge status={detail.data.deployment.status} />
                      <span className="font-mono text-[10px] text-zinc-500">{detail.data.deployment.id.slice(0, 8)}</span>
                      <Badge>
                        {project.placements.find((p) => p.serverId === detail.data?.deployment.serverId)?.serverName ||
                          detail.data.deployment.serverId.slice(0, 8)}
                      </Badge>
                      {detail.data.deployment.commitSha ? (
                        <span className="font-mono text-[10px] text-zinc-500" title={detail.data.deployment.commitSha}>
                          {detail.data.deployment.commitSha.slice(0, 8)}
                        </span>
                      ) : null}
                      {detail.data.deployment.release ? (
                        <span className="font-mono text-[10px] text-zinc-600">{detail.data.deployment.release}</span>
                      ) : null}
                      <span className="text-[10px] text-zinc-600">{time(detail.data.deployment.startedAt)}</span>
                    </div>
                    <div className="flex items-center gap-2 text-[10px] text-zinc-500">
                      <span>{duration(detail.data.deployment)}</span>
                      <Button size="icon" variant="ghost" title="Refresh" onClick={() => detail.refetch()}>
                        <RefreshCw size={12} />
                      </Button>
                    </div>
                  </div>
                  {detail.data.deployment.detail ? (
                    <div className="border-b border-red-900/30 bg-red-500/5 px-3 py-2 text-[10px] text-red-300">
                      {detail.data.deployment.detail}
                    </div>
                  ) : null}
                  <pre
                    ref={deployLogRef}
                    className="min-h-0 flex-1 overflow-auto bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300"
                  >
                    {detail.data.logs.length
                      ? detail.data.logs.map((l) => l.line).join('\n')
                      : 'waiting for output…'}
                  </pre>
                </>
              ) : (
                <EmptyState icon={<History size={20} />} title="Select a deployment" />
              )}
            </Card>
          </div>
        ) : null}

        {tab === 'logs' ? (
          <Card className="flex h-[calc(100vh-300px)] min-h-[380px] flex-col overflow-hidden">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-zinc-800/70 px-3 py-2">
              <div className="flex flex-wrap items-center gap-2 text-[11px] text-zinc-400">
                <FileText size={12} className="text-zinc-500" />
                app log
                {project.placements.length > 1 ? (
                  <select
                    value={logServer}
                    onChange={(e) => setLogServer(e.target.value)}
                    className="h-7 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-[10px] text-zinc-300 outline-none"
                  >
                    {project.placements.map((p) => (
                      <option key={p.serverId} value={p.serverId}>
                        {p.serverName || p.serverId} :{p.port}
                      </option>
                    ))}
                  </select>
                ) : null}
                <Badge>{appLogs.data ? bytes(appLogs.data.size) : '—'}</Badge>
                {appLogs.data?.truncated ? <Badge tone="warn">tail only</Badge> : null}
                {!anyOnline ? <Badge tone="bad">offline</Badge> : null}
              </div>
              <div className="flex items-center gap-1">
                <label className="flex cursor-pointer items-center gap-1 text-[10px] text-zinc-500">
                  <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> follow
                </label>
                <Button size="icon" variant="ghost" onClick={() => appLogs.refetch()}>
                  <RefreshCw size={12} />
                </Button>
              </div>
            </div>
            <pre
              ref={logRef}
              onScroll={(e) => {
                const el = e.currentTarget
                setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40)
              }}
              className="min-h-0 flex-1 overflow-auto bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300"
            >
              {project.placements.length === 0
                ? 'no replica yet — deploy the project first'
                : appLogs.data?.logs ||
                  (appLogs.isError ? String(appLogs.error) : 'no output yet — deploy the project first')}
            </pre>
            <div className="border-t border-zinc-800/70 px-3 py-1.5 font-mono text-[10px] text-zinc-600">
              {appLogs.data?.path || '~/apps/' + project.slug + '/logs/app.log'}
            </div>
          </Card>
        ) : null}

        {tab === 'settings' ? (
          <SettingsTab
            key={project.id}
            project={project}
            onDelete={() => setDeleteOpen(true)}
            deleting={remove.isPending}
          />
        ) : null}
      </div>

      <ConfirmDialog
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        title={`Delete ${project.name}?`}
        description="This stops every replica, removes the project and forgets its deployment history. This cannot be undone."
        confirmLabel="Delete project"
        danger
        busy={remove.isPending}
        onConfirm={() => {
          remove.mutate()
          setDeleteOpen(false)
        }}
      />
    </div>
  )
}

function Overview({ project, onDeploy, canDeploy }: { project: ProjectView; onDeploy: () => void; canDeploy: boolean }) {
  const envKeys = Object.keys(project.env ?? {})
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card className="p-4">
        <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Deployment</div>
        <div className="mt-3 flex items-start justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <DeployBadge status={aggregateStatus(project)} />
              {project.latest ? <DeployBadge status={project.latest.status} /> : null}
            </div>
            <p className="mt-2 text-[11px] leading-relaxed text-zinc-500">
              {project.latest
                ? `Last deploy ${ago(project.latest.startedAt)} · ${duration(project.latest)}`
                : 'This project has never been deployed.'}
            </p>
          </div>
          <Button variant="primary" size="sm" disabled={!canDeploy} onClick={onDeploy}>
            <Play size={12} /> Deploy
          </Button>
        </div>
        <div className="mt-4 divide-y divide-zinc-800/60">
          <KeyValue label="URL">
            <a href={project.url} target="_blank" rel="noreferrer" className="text-emerald-300/90 hover:text-emerald-200">
              {project.hostname}
            </a>
          </KeyValue>
          <KeyValue label="Replicas">{`${project.placements.length} / ${project.replicas}`}</KeyValue>
          <KeyValue label="Selector">{project.selector || (project.pinnedServer ? 'pinned' : 'any phone')}</KeyValue>
          <KeyValue label="Pinned to">{project.pinnedServer || '—'}</KeyValue>
        </div>
      </Card>

      <Card className="p-4">
        <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Source</div>
        <div className="mt-3 divide-y divide-zinc-800/60">
          {project.sourceType === 'upload' ? (
            <>
              <KeyValue label="Kind">uploaded folder</KeyValue>
              <KeyValue label="Size">{bytes(project.sourceSize)}</KeyValue>
              <KeyValue label="Updated">{project.sourceUpdatedAt ? time(project.sourceUpdatedAt) : '—'}</KeyValue>
            </>
          ) : (
            <>
              <KeyValue label="Repository">
                <span className="font-mono">{project.repo || '—'}</span>
              </KeyValue>
              <KeyValue label="Branch">{project.branch}</KeyValue>
            </>
          )}
          <KeyValue label="Build">
            <span className="font-mono">{project.buildCmd || '(none)'}</span>
          </KeyValue>
          <KeyValue label="Start">
            <span className="font-mono">{project.startCmd || '(not set)'}</span>
          </KeyValue>
          <KeyValue label="Health">{project.healthPath}</KeyValue>
          <KeyValue label="Created">{time(project.createdAt)}</KeyValue>
        </div>
      </Card>

      <Card className="overflow-hidden lg:col-span-2">
        <div className="flex items-center justify-between border-b border-zinc-800/70 px-4 py-2.5">
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Replicas</div>
          <span className="text-[10px] text-zinc-600">traffic is balanced across running replicas</span>
        </div>
        {project.placements.length === 0 ? (
          <div className="px-4 py-6 text-center text-[11px] text-zinc-600">
            No placements yet. Deploy to schedule replicas onto eligible phones.
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[11px]">
              <thead className="bg-zinc-950/40 text-[9px] tracking-wider text-zinc-500 uppercase">
                <tr>
                  <th className="px-4 py-2 font-medium">phone</th>
                  <th className="px-3 py-2 font-medium">state</th>
                  <th className="px-3 py-2 font-medium">desired</th>
                  <th className="px-3 py-2 font-medium">port</th>
                  <th className="px-3 py-2 font-medium">pid</th>
                  <th className="px-3 py-2 font-medium">restarts</th>
                  <th className="px-4 py-2 font-medium">last error</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/50">
                {project.placements.map((pl) => (
                  <tr key={pl.serverId} className="hover:bg-zinc-800/20">
                    <td className="px-4 py-2">
                      <div className="flex items-center gap-1.5 text-zinc-300">
                        <Server size={11} className="text-zinc-500" />
                        {pl.serverName || pl.serverId.slice(0, 8)}
                        {pl.drained ? <Badge tone="warn">drained</Badge> : null}
                        {!pl.online ? <Badge>offline</Badge> : null}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <DeployBadge status={pl.status} />
                    </td>
                    <td className="px-3 py-2 text-zinc-500">{pl.desired}</td>
                    <td className="px-3 py-2 font-mono text-zinc-400">{pl.port}</td>
                    <td className="px-3 py-2 font-mono text-zinc-500">{pl.pid || '—'}</td>
                    <td className="px-3 py-2 font-mono text-zinc-500">{pl.restarts}</td>
                    <td className="max-w-[240px] truncate px-4 py-2 text-[10px] text-red-300/80" title={pl.lastError}>
                      {pl.lastError || '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card className="p-4 lg:col-span-2">
        <div className="flex items-center justify-between">
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Environment</div>
          <span className="text-[10px] text-zinc-600">{envKeys.length} variables · encrypted at rest</span>
        </div>
        <div className="mt-3 flex flex-wrap gap-1.5">
          {envKeys.length === 0 ? (
            <span className="text-[11px] text-zinc-600">No environment variables. Add them in Settings.</span>
          ) : (
            envKeys.map((k) => (
              <span key={k} className="rounded-md bg-zinc-800/70 px-1.5 py-0.5 font-mono text-[10px] text-zinc-400">
                {k}
              </span>
            ))
          )}
        </div>
        <p className="mt-3 flex items-center gap-1 text-[10px] text-zinc-600">
          <Server size={10} /> apps are started with <span className="font-mono">PORT</span>,{' '}
          <span className="font-mono">HOST=0.0.0.0</span> and <span className="font-mono">NODE_ENV=production</span>.
        </p>
      </Card>

      <DataCard project={project} />
    </div>
  )
}

function DataCard({ project }: { project: ProjectView }) {
  const qc = useQueryClient()
  const addons = useQuery({ queryKey: ['addons'], queryFn: api.addons, refetchInterval: 10000 })
  const [attachId, setAttachId] = useState('')
  const projectServer = project.pinnedServer || project.placements[0]?.serverId || ''
  const attached = project.addons ?? []
  const attachedIds = new Set(attached.map((a) => a.id))
  const candidates = (addons.data?.addons ?? []).filter(
    (a) => !attachedIds.has(a.id) && (!projectServer || a.pinnedServer === projectServer),
  )
  const bind = useMutation({
    mutationFn: (addonId: string) => api.bindAddon(addonId, project.id),
    onSuccess: () => {
      setAttachId('')
      qc.invalidateQueries({ queryKey: ['project', project.id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })
  const unbind = useMutation({
    mutationFn: (addonId: string) => api.unbindAddon(addonId, project.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', project.id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
      qc.invalidateQueries({ queryKey: ['addons'] })
    },
  })

  return (
    <Card className="p-4 lg:col-span-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Database size={11} /> Addons
        </div>
        <Link to="/addons" className="text-[10px] text-zinc-500 hover:text-zinc-200">
          manage all addons →
        </Link>
      </div>
      <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
        Addons attached here must live on the same phone. Their connection variables are injected on every deploy.
      </p>

      <div className="mt-3 space-y-2">
        {attached.map((a) => (
          <div key={a.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-zinc-900/60 px-3 py-2">
            <div className="min-w-0">
              <Link to={`/addons/${a.id}`} className="truncate text-[11px] text-zinc-200 hover:text-emerald-300">
                {a.name}
              </Link>
              <div className="font-mono text-[10px] text-zinc-500">
                {a.engineName || a.engine} · {a.embedded ? 'file' : `port ${a.port}`}
              </div>
            </div>
            <div className="flex items-center gap-2">
              <DeployBadge status={a.status} />
              <Link to={`/addons/${a.id}`} className="text-[10px] text-zinc-500 hover:text-zinc-200">
                open →
              </Link>
              <button
                type="button"
                title="Detach from this project"
                disabled={unbind.isPending}
                onClick={() => unbind.mutate(a.id)}
                className="rounded p-0.5 text-zinc-500 hover:bg-red-500/10 hover:text-red-300 disabled:opacity-40"
              >
                <X size={11} />
              </button>
            </div>
          </div>
        ))}
        {attached.length === 0 ? (
          <p className="text-[11px] text-zinc-600">No addon attached.</p>
        ) : null}
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-zinc-800/70 pt-3">
        <select
          className="h-8 min-w-0 flex-1 rounded-lg border border-zinc-800 bg-zinc-950/70 px-2 text-[11px] text-zinc-300 outline-none"
          value={attachId}
          onChange={(e) => setAttachId(e.target.value)}
          disabled={!projectServer}
        >
          <option value="">{projectServer ? 'Attach an existing addon…' : 'Pin the project to a phone first'}</option>
          {candidates.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} · {a.engineName || a.engine}
            </option>
          ))}
        </select>
        <Button size="sm" disabled={!attachId || bind.isPending} onClick={() => attachId && bind.mutate(attachId)}>
          Attach
        </Button>
        <Link to={`/addons?project=${project.id}&new=1`} className="text-[10px] text-zinc-500 hover:text-zinc-200">
          or create one →
        </Link>
      </div>
      {bind.isError ? <div className="mt-2 text-[10px] text-red-400">{String(bind.error)}</div> : null}
      {unbind.isError ? <div className="mt-2 text-[10px] text-red-400">{String(unbind.error)}</div> : null}
    </Card>
  )
}

function SettingsTab({
  project,
  onDelete,
  deleting,
}: {
  project: ProjectView
  onDelete: () => void
  deleting: boolean
}) {
  const qc = useQueryClient()
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers })
  const [form, setForm] = useState({
    name: project.name,
    repo: project.repo,
    branch: project.branch,
    buildCmd: project.buildCmd,
    startCmd: project.startCmd,
    healthPath: project.healthPath,
    envText: envToString(project.env),
    replicas: project.replicas,
    selector: project.selector,
    pinned: project.pinnedServer,
  })
  const [sourceFiles, setSourceFiles] = useState<FileList | null>(null)
  const [sourceMsg, setSourceMsg] = useState('')
  const [sourceErr, setSourceErr] = useState('')

  const uploadSource = useMutation({
    mutationFn: async () => {
      if (!sourceFiles || sourceFiles.length === 0) throw new Error('select a folder first')
      const z = await zipSelection(sourceFiles, { stripRoot: true })
      return api.uploadProjectSource(project.id, z.blob)
    },
    onSuccess: (res) => {
      setSourceMsg(`uploaded ${bytes(res.bytes)}`)
      setSourceFiles(null)
      qc.invalidateQueries({ queryKey: ['project', project.id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
    onError: (e) => setSourceErr(String(e)),
  })

  const update = useMutation({
    mutationFn: () =>
      api.updateProject(project.id, {
        name: form.name,
        repo: form.repo,
        branch: form.branch,
        buildCmd: form.buildCmd,
        startCmd: form.startCmd,
        healthPath: form.healthPath,
        env: parseEnv(form.envText),
        replicas: form.replicas,
        selector: form.selector,
        pinnedServer: form.pinned,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', project.id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })

  const field = 'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none focus:border-emerald-400/40'
  const caption = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
      <Card className="p-4">
        <div className={caption}>Configuration</div>
        <p className="mt-1 text-[10px] text-zinc-600">Changes apply on the next deploy; placement changes rebalance automatically.</p>
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div>
            <div className={caption}>Name</div>
            <input className={`${field} mt-1`} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          {project.sourceType === 'git' ? (
            <>
              <div>
                <div className={caption}>Branch</div>
                <input className={`${field} mt-1`} value={form.branch} onChange={(e) => setForm({ ...form, branch: e.target.value })} />
              </div>
              <div className="sm:col-span-2">
                <div className={caption}>Git repository</div>
                <input className={`${field} mt-1 font-mono`} value={form.repo} onChange={(e) => setForm({ ...form, repo: e.target.value })} />
              </div>
            </>
          ) : null}
          <div>
            <div className={caption}>Start command</div>
            <input className={`${field} mt-1 font-mono`} value={form.startCmd} onChange={(e) => setForm({ ...form, startCmd: e.target.value })} />
          </div>
          <div>
            <div className={caption}>Health path</div>
            <input className={`${field} mt-1 font-mono`} value={form.healthPath} onChange={(e) => setForm({ ...form, healthPath: e.target.value })} />
          </div>
          <div className="sm:col-span-2">
            <div className={caption}>Build command</div>
            <input className={`${field} mt-1 font-mono`} value={form.buildCmd} onChange={(e) => setForm({ ...form, buildCmd: e.target.value })} />
          </div>

          <div>
            <div className={caption}>Replicas</div>
            <input
              type="number"
              min={1}
              max={5}
              className={`${field} mt-1`}
              value={form.replicas}
              onChange={(e) => setForm({ ...form, replicas: Math.max(1, Math.min(5, Number(e.target.value) || 1)) })}
            />
          </div>
          <div>
            <div className={caption}>Pin to phone</div>
            <select
              className={`${field} mt-1`}
              value={form.pinned}
              onChange={(e) => setForm({ ...form, pinned: e.target.value })}
            >
              <option value="">No pin — scheduler decides</option>
              {servers.data?.servers.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} {s.online ? '· online' : '· offline'} {s.drain ? '· drained' : ''}
                </option>
              ))}
            </select>
          </div>
          <div className="sm:col-span-2">
            <div className={caption}>Label selector</div>
            <input
              className={`${field} mt-1 font-mono`}
              value={form.selector}
              onChange={(e) => setForm({ ...form, selector: e.target.value })}
              placeholder="tier=web, region=home"
              disabled={!!form.pinned}
            />
            <p className="mt-1 text-[10px] text-zinc-600">
              Replicas only land on phones whose labels match every key. Set the labels on each phone's Settings tab.
            </p>
          </div>

          <div className="sm:col-span-2">
            <div className={caption}>Environment</div>
            <textarea
              className="mt-1 min-h-32 w-full resize-y rounded-lg border border-zinc-800 bg-zinc-950/70 p-3 font-mono text-[11px] text-zinc-200 outline-none focus:border-emerald-400/40"
              value={form.envText}
              onChange={(e) => setForm({ ...form, envText: e.target.value })}
            />
            <p className="mt-1 text-[10px] text-zinc-600">KEY=value per line. Values are encrypted with the control plane key.</p>
          </div>
        </div>
        {update.isError ? <div className="mt-3 text-[11px] text-red-400">{String(update.error)}</div> : null}
        {update.isSuccess ? <div className="mt-3 text-[11px] text-emerald-300">saved — reconciler will adjust replicas</div> : null}
        <div className="mt-4 flex justify-end">
          <Button variant="primary" size="sm" disabled={update.isPending} onClick={() => update.mutate()}>
            {update.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Save size={12} />}
            Save changes
          </Button>
        </div>
      </Card>

      <div className="space-y-3">
        <Card className="p-4">
          <div className={caption}>Source</div>
          {project.sourceType === 'upload' ? (
            <>
              <div className="mt-2 text-[11px] text-zinc-400">
                {project.sourceSize > 0 ? `Uploaded folder · ${bytes(project.sourceSize)}` : 'No folder uploaded yet'}
              </div>
              <div className="text-[10px] text-zinc-600">
                {project.sourceUpdatedAt ? `updated ${time(project.sourceUpdatedAt)}` : 'upload a folder to enable deploy'}
              </div>
              <input
                type="file"
                multiple
                disabled={uploadSource.isPending}
                ref={(el) => {
                  if (el) {
                    el.setAttribute('webkitdirectory', '')
                    el.setAttribute('directory', '')
                  }
                }}
                onChange={(e) => {
                  setSourceErr('')
                  setSourceMsg('')
                  setSourceFiles(e.target.files)
                }}
                className="mt-3 block w-full text-[10px] text-zinc-400 file:mr-2 file:rounded-md file:border-0 file:bg-zinc-800 file:px-2.5 file:py-1.5 file:text-[11px] file:text-zinc-200 hover:file:bg-zinc-700"
              />
              {sourceFiles ? <div className="mt-1 text-[10px] text-zinc-500">{sourceFiles.length} files selected</div> : null}
              {sourceMsg ? <div className="mt-2 text-[11px] text-emerald-300">{sourceMsg}</div> : null}
              {sourceErr ? <div className="mt-2 text-[11px] text-red-400">{sourceErr}</div> : null}
              <Button
                size="sm"
                variant="primary"
                className="mt-3 w-full"
                disabled={!sourceFiles || uploadSource.isPending}
                onClick={() => {
                  setSourceErr('')
                  setSourceMsg('')
                  uploadSource.mutate()
                }}
              >
                {uploadSource.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : null}
                {uploadSource.isPending ? 'Uploading…' : 'Replace source folder'}
              </Button>
              <p className="mt-2 text-[10px] leading-relaxed text-zinc-600">
                The top-level folder is stripped. Uploading replaces the source; deploy to roll it out to every replica.
              </p>
            </>
          ) : (
            <p className="mt-2 text-[10px] leading-relaxed text-zinc-600">
              This project is deployed from a git repository. Edit it in Configuration.
            </p>
          )}
        </Card>

        <Card className="p-4">
          <div className={caption}>Current placement</div>
          <div className="mt-2 space-y-2">
            {project.placements.length === 0 ? (
              <p className="text-[11px] text-zinc-600">No replicas scheduled yet.</p>
            ) : (
              project.placements.map((pl) => (
                <div key={pl.serverId} className="rounded-lg bg-zinc-900/60 p-2.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-[11px] text-zinc-300">{pl.serverName || pl.serverId}</span>
                    <DeployBadge status={pl.status} />
                  </div>
                  <div className="mt-1 font-mono text-[10px] text-zinc-500">
                    port {pl.port} · pid {pl.pid || '—'} · {pl.desired}
                    {pl.drained ? ' · drained' : ''}
                    {!pl.online ? ' · offline' : ''}
                  </div>
                </div>
              ))
            )}
          </div>
          <p className="mt-2 text-[10px] leading-relaxed text-zinc-600">
            The reconciler replaces replicas on offline or drained phones once a new replica is healthy, and stops
            orphaned copies when a phone comes back.
          </p>
        </Card>
        <Card className="border-red-900/40 p-4">
          <div className="text-[10px] font-medium tracking-widest text-red-400/80 uppercase">Danger zone</div>
          <p className="mt-1 text-[10px] leading-relaxed text-zinc-500">
            Stops every replica, removes the project and its deployment history from the panel.
          </p>
          <Button variant="danger" size="sm" className="mt-3 w-full" disabled={deleting} onClick={onDelete}>
            <Trash2 size={12} /> Delete project
          </Button>
        </Card>
      </div>
    </div>
  )
}

