import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Boxes, GitBranch, Globe, Plus, Server, Smartphone, X } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../api'
import type { ProjectView } from '../types'
import { Badge, Button, Card, DeployBadge, EmptyState, Spinner } from '../components/ui'
import { ago, bytes, time } from '../lib/format'
import { parseEnv } from '../lib/env'
import { aggregateStatus, primaryService } from '../lib/projects'
import { zipSelection } from '../lib/zip'

const input =
  'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40'
const label = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

type WorkloadKind = 'app' | 'static' | 'worker'

type ServiceDraft = {
  name: string
  slug: string
  kind: WorkloadKind
  buildCmd: string
  startCmd: string
  staticDir: string
  healthPath: string
  envText: string
}

const emptyService = (): ServiceDraft => ({
  name: '',
  slug: '',
  kind: 'app',
  buildCmd: '',
  startCmd: '',
  staticDir: '',
  healthPath: '/',
  envText: '',
})

function draftSlug(name: string) {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
}

function NewProject({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers })
  const engineCatalog = useQuery({ queryKey: ['addon-engines'], queryFn: api.addonEngines })
  const [name, setName] = useState('')
  const [repo, setRepo] = useState('')
  const [branch, setBranch] = useState('main')
  const [sourceType, setSourceType] = useState<'git' | 'upload'>('git')
  const [folderFiles, setFolderFiles] = useState<FileList | null>(null)
  const [createdId, setCreatedId] = useState<string | null>(null)
  const [serverId, setServerId] = useState('')
  const [selector, setSelector] = useState('')
  const [replicas, setReplicas] = useState(1)
  const [buildCmd, setBuildCmd] = useState('')
  const [startCmd, setStartCmd] = useState('')
  const [staticDir, setStaticDir] = useState('')
  const [healthPath, setHealthPath] = useState('/')
  const [kind, setKind] = useState<WorkloadKind>('app')
  const [envText, setEnvText] = useState('')
  const [stackMode, setStackMode] = useState(false)
  const [services, setServices] = useState<ServiceDraft[]>([emptyService()])
  const [dbEnabled, setDbEnabled] = useState(false)
  const [dbEngine, setDbEngine] = useState('postgres')
  const [dbCreated, setDbCreated] = useState(false)

  const folderBytes = folderFiles
    ? Array.from(folderFiles).reduce((sum, f) => sum + f.size, 0)
    : 0

  const updateService = (index: number, patch: Partial<ServiceDraft>) => {
    setServices((prev) => prev.map((s, i) => (i === index ? { ...s, ...patch } : s)))
  }
  const addService = () => setServices((prev) => [...prev, emptyService()])
  const removeService = (index: number) => {
    setServices((prev) => (prev.length > 1 ? prev.filter((_, i) => i !== index) : prev))
  }

  const create = useMutation({
    mutationFn: async () => {
      let id = createdId
      if (!id) {
        const res = await api.createProject({
          name,
          kind: stackMode ? undefined : kind,
          repo: sourceType === 'git' ? repo : '',
          branch,
          buildCmd: stackMode ? '' : buildCmd,
          startCmd: stackMode || kind === 'static' ? '' : startCmd,
          staticDir: stackMode || kind !== 'static' ? '' : staticDir.trim(),
          healthPath: stackMode || kind !== 'app' ? '/' : healthPath,
          serverId: serverId || undefined,
          selector: stackMode || serverId ? undefined : selector.trim() || undefined,
          replicas: stackMode ? 1 : replicas,
          sourceType,
          env: parseEnv(envText),
          stack: stackMode || undefined,
          services: stackMode
            ? services.map((s) => ({
                name: s.name.trim(),
                slug: s.slug.trim(),
                kind: s.kind,
                buildCmd: s.buildCmd.trim(),
                startCmd: s.kind === 'static' ? '' : s.startCmd.trim(),
                staticDir: s.kind === 'static' ? s.staticDir.trim() : '',
                healthPath: s.kind === 'app' ? s.healthPath.trim() || '/' : '/',
                env: parseEnv(s.envText),
              }))
            : undefined,
        })
        id = res.project.id
        setCreatedId(id)
      }
      if (dbEnabled && serverId && !dbCreated) {
        await api.createAddon({
          name: `${name.slice(0, 32)} database`,
          engine: dbEngine,
          serverId,
          projectId: id,
        })
        setDbCreated(true)
      }
      if (sourceType === 'upload') {
        if (!folderFiles || folderFiles.length === 0) throw new Error('select a project folder to upload')
        const z = await zipSelection(folderFiles, { stripRoot: true })
        await api.uploadProjectSource(id, z.blob)
      }
      return id
    },
    onSuccess: (id) => {
      qc.invalidateQueries({ queryKey: ['projects'] })
      navigate(stackMode ? `/stacks/${id}` : `/projects/${id}`)
    },
  })

  const servicesReady =
    services.length > 0 &&
    services.every((s) => !!s.name.trim() && (s.kind === 'static' || !!s.startCmd.trim()))
  const workloadReady = stackMode || kind === 'static' || !!startCmd.trim()
  const ready =
    !!name.trim() &&
    (sourceType === 'git' ? !!repo.trim() : !!folderFiles && folderFiles.length > 0) &&
    (!stackMode || (!!serverId && servicesReady)) &&
    workloadReady &&
    (!dbEnabled || (!!serverId && kind !== 'static'))

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
        className="max-h-[90vh] w-full max-w-2xl overflow-y-auto rounded-xl border border-zinc-800 bg-zinc-950 p-5 shadow-2xl"
      >
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-sm font-semibold text-zinc-100">New project</h2>
            <p className="mt-0.5 text-[11px] text-zinc-500">
              One service, or a stack where every service gets its own subdomain.
            </p>
          </div>
          <button type="button" onClick={onClose} className="text-zinc-500 hover:text-zinc-200">
            ✕
          </button>
        </div>

        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <div className={label}>Name</div>
            <input className={`${input} mt-1`} value={name} onChange={(e) => setName(e.target.value)} placeholder="Status API" autoFocus />
          </div>
          <div className="sm:col-span-2">
            <div className={label}>Deployment type</div>
            <div className="mt-1 grid grid-cols-2 gap-1.5">
              <button
                type="button"
                disabled={!!createdId}
                onClick={() => setStackMode(false)}
                className={`h-8 rounded-lg border text-xs transition-colors ${
                  !stackMode
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200'
                }`}
              >
                Single service
              </button>
              <button
                type="button"
                disabled={!!createdId}
                onClick={() => setStackMode(true)}
                className={`h-8 rounded-lg border text-xs transition-colors ${
                  stackMode
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200'
                }`}
              >
                Multi-service stack
              </button>
            </div>
          </div>
          {!stackMode ? (
            <div className="sm:col-span-2">
              <div className={label}>Workload</div>
              <div className="mt-1 grid grid-cols-3 gap-1.5">
                {(
                  [
                    { id: 'app', label: 'Web app', hint: 'process + port + hostname' },
                    { id: 'static', label: 'Static site', hint: 'agent serves files' },
                    { id: 'worker', label: 'Worker', hint: 'no port, no hostname' },
                  ] as const
                ).map((w) => (
                  <button
                    key={w.id}
                    type="button"
                    disabled={!!createdId}
                    onClick={() => {
                      setKind(w.id)
                      if (w.id === 'static') setDbEnabled(false)
                    }}
                    title={w.hint}
                    className={`h-8 rounded-lg border text-xs transition-colors ${
                      kind === w.id
                        ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                        : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200'
                    }`}
                  >
                    {w.label}
                  </button>
                ))}
              </div>
              <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
                {kind === 'static'
                  ? 'The phone serves the release directory directly. Set a build command to produce a dist folder, then point the static directory at it.'
                  : kind === 'worker'
                    ? 'A worker runs as a supervised process with no port or hostname. Use it for queue consumers, cron-like work and background jobs.'
                    : 'A single supervised process behind a hostname and health check.'}
              </p>
            </div>
          ) : null}
          <div className="sm:col-span-2">
            <div className={label}>Source</div>
            <div className="mt-1 grid grid-cols-2 gap-1.5">
              <button
                type="button"
                disabled={!!createdId}
                onClick={() => setSourceType('git')}
                className={`h-8 rounded-lg border text-xs transition-colors ${
                  sourceType === 'git'
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200'
                }`}
              >
                Git repository
              </button>
              <button
                type="button"
                disabled={!!createdId}
                onClick={() => setSourceType('upload')}
                className={`h-8 rounded-lg border text-xs transition-colors ${
                  sourceType === 'upload'
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200'
                }`}
              >
                Upload folder
              </button>
            </div>
          </div>

          {sourceType === 'git' ? (
            <>
              <div className="sm:col-span-2">
                <div className={label}>Git repository</div>
                <input className={`${input} mt-1`} value={repo} onChange={(e) => setRepo(e.target.value)} placeholder="https://github.com/you/app.git" />
              </div>
              <div>
                <div className={label}>Branch</div>
                <input className={`${input} mt-1`} value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="main" />
              </div>
            </>
          ) : (
            <div className="sm:col-span-2">
              <div className={label}>Project folder</div>
              <input
                type="file"
                multiple
                disabled={!!createdId}
                ref={(el) => {
                  if (el) {
                    el.setAttribute('webkitdirectory', '')
                    el.setAttribute('directory', '')
                  }
                }}
                onChange={(e) => setFolderFiles(e.target.files)}
                className="mt-1 block w-full text-[11px] text-zinc-400 file:mr-3 file:rounded-md file:border-0 file:bg-zinc-800 file:px-3 file:py-1.5 file:text-xs file:text-zinc-200 hover:file:bg-zinc-700"
              />
              <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
                The selected top-level folder is removed; its contents become the project root.
                {folderFiles ? ` ${folderFiles.length} files · ${bytes(folderBytes)}.` : ' Nothing selected yet.'}
              </p>
            </div>
          )}
          <div>
            <div className={label}>{stackMode ? 'Pin the stack to a phone' : 'Pin to phone (optional)'}</div>
            <select className={`${input} mt-1`} value={serverId} onChange={(e) => setServerId(e.target.value)}>
              <option value="">{stackMode ? 'Select a phone — required' : 'No pin — scheduler decides'}</option>
              {servers.data?.servers.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name} {s.online ? '· online' : '· offline'} {s.drain ? '· drained' : ''}
                </option>
              ))}
            </select>
            {stackMode ? (
              <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
                All services run on this phone so they can reach each other over localhost.
              </p>
            ) : null}
          </div>

          {!stackMode && kind === 'static' ? null : (
          <div className="sm:col-span-2 rounded-lg border border-zinc-800/80 bg-zinc-900/30 p-3">
            <label className="flex cursor-pointer items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={dbEnabled}
                disabled={!!createdId || (!stackMode && replicas > 1)}
                onChange={(e) => setDbEnabled(e.target.checked)}
              />
              <span className="text-[11px] text-zinc-300">
                Create a database for this {stackMode ? 'stack' : 'project'}
                <span className="mt-0.5 block text-[10px] leading-relaxed text-zinc-600">
                  {!stackMode && replicas > 1
                    ? 'A pinned database needs one replica. Reduce replicas to 1, or create the database as standalone and attach it later.'
                    : 'Installs the engine on the pinned phone and injects its connection URL at deploy time. Requires a phone pin, because the database must share loopback with the app.'}
                </span>
              </span>
            </label>
            {dbEnabled ? (
              <div className="mt-3 grid gap-2 sm:grid-cols-2">
                <div>
                  <div className={label}>Engine</div>
                  <select
                    className={`${input} mt-1`}
                    value={dbEngine}
                    disabled={!!createdId}
                    onChange={(e) => setDbEngine(e.target.value)}
                  >
                    {engineCatalog.data?.engines.map((e) => (
                      <option key={e.id} value={e.id}>
                        {e.name}
                        {e.embedded ? ' · file' : ` · ~${e.minRamMb} MB`}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="flex items-end">
                  <p className="text-[10px] leading-relaxed text-zinc-600">
                    {serverId
                      ? 'The database will be created with this project and attached to it.'
                      : 'Select a phone above to host the database.'}
                  </p>
                </div>
              </div>
            ) : null}
          </div>
          )}

          {!stackMode ? (
            <>
              <div>
                <div className={label}>Replicas</div>
                <input
                  type="number"
                  min={1}
                  max={5}
                  className={`${input} mt-1`}
                  value={replicas}
                  onChange={(e) => setReplicas(Math.max(1, Math.min(5, Number(e.target.value) || 1)))}
                />
              </div>
              <div>
                <div className={label}>Label selector (optional)</div>
                <input
                  className={`${input} mt-1 font-mono`}
                  value={selector}
                  onChange={(e) => setSelector(e.target.value)}
                  placeholder="tier=web, region=home"
                  disabled={!!serverId}
                />
              </div>
              {kind === 'static' ? (
                <div>
                  <div className={label}>Static directory</div>
                  <input
                    className={`${input} mt-1 font-mono`}
                    value={staticDir}
                    onChange={(e) => setStaticDir(e.target.value)}
                    placeholder="dist (blank = repository root)"
                  />
                </div>
              ) : (
                <div>
                  <div className={label}>Start command</div>
                  <input className={`${input} mt-1 font-mono`} value={startCmd} onChange={(e) => setStartCmd(e.target.value)} placeholder="node server.js" />
                </div>
              )}
              {kind === 'app' ? (
                <div>
                  <div className={label}>Health path</div>
                  <input className={`${input} mt-1 font-mono`} value={healthPath} onChange={(e) => setHealthPath(e.target.value)} placeholder="/" />
                </div>
              ) : null}
              <div className="sm:col-span-2">
                <div className={label}>Build command (optional)</div>
                <input className={`${input} mt-1 font-mono`} value={buildCmd} onChange={(e) => setBuildCmd(e.target.value)} placeholder="npm ci --include=dev && npm run build" />
              </div>
            </>
          ) : (
            <div className="sm:col-span-2">
              <div className="flex items-center justify-between">
                <div className={label}>Services</div>
                <Button size="sm" variant="ghost" type="button" disabled={!!createdId} onClick={addService}>
                  <Plus size={12} /> Add service
                </Button>
              </div>
              <div className="mt-2 space-y-3">
                {services.map((svc, i) => (
                  <div key={i} className="rounded-lg border border-zinc-800/80 bg-zinc-950/40 p-3">
                    <div className="flex items-center justify-between">
                      <span className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
                        Service {i + 1}
                      </span>
                      {services.length > 1 ? (
                        <button
                          type="button"
                          disabled={!!createdId}
                          onClick={() => removeService(i)}
                          className="rounded p-1 text-zinc-500 hover:bg-red-500/10 hover:text-red-300 disabled:opacity-40"
                          aria-label={`Remove service ${i + 1}`}
                        >
                          <X size={12} />
                        </button>
                      ) : null}
                    </div>
                    <div className="mt-2 grid gap-2 sm:grid-cols-2">
                      <div>
                        <div className={label}>Name</div>
                        <input
                          className={`${input} mt-1`}
                          value={svc.name}
                          disabled={!!createdId}
                          onChange={(e) => updateService(i, { name: e.target.value })}
                          placeholder={i === 0 ? 'Web' : 'API'}
                        />
                      </div>
                      <div>
                        <div className={label}>Kind</div>
                        <select
                          className={`${input} mt-1`}
                          value={svc.kind}
                          disabled={!!createdId}
                          onChange={(e) => updateService(i, { kind: e.target.value as WorkloadKind })}
                        >
                          <option value="app">Web app</option>
                          <option value="static">Static site</option>
                          <option value="worker">Worker</option>
                        </select>
                      </div>
                      {svc.kind !== 'worker' ? (
                        <div className="sm:col-span-2">
                          <div className={label}>Subdomain</div>
                          <input
                            className={`${input} mt-1 font-mono`}
                            value={svc.slug}
                            disabled={!!createdId}
                            onChange={(e) => updateService(i, { slug: e.target.value })}
                            placeholder={
                              i === 0
                                ? draftSlug(name) || 'app'
                                : `${draftSlug(name) || 'app'}-${draftSlug(svc.name) || 'service'}`
                            }
                          />
                        </div>
                      ) : null}
                      <div className="sm:col-span-2">
                        <div className={label}>Build command (optional)</div>
                        <input
                          className={`${input} mt-1 font-mono`}
                          value={svc.buildCmd}
                          disabled={!!createdId}
                          onChange={(e) => updateService(i, { buildCmd: e.target.value })}
                          placeholder="npm ci --include=dev && npm run build"
                        />
                      </div>
                      {svc.kind === 'static' ? (
                        <div className="sm:col-span-2">
                          <div className={label}>Static directory</div>
                          <input
                            className={`${input} mt-1 font-mono`}
                            value={svc.staticDir}
                            disabled={!!createdId}
                            onChange={(e) => updateService(i, { staticDir: e.target.value })}
                            placeholder="dist (blank = repository root)"
                          />
                        </div>
                      ) : (
                        <div className="sm:col-span-2">
                          <div className={label}>Start command</div>
                          <input
                            className={`${input} mt-1 font-mono`}
                            value={svc.startCmd}
                            disabled={!!createdId}
                            onChange={(e) => updateService(i, { startCmd: e.target.value })}
                            placeholder="node server.js"
                          />
                        </div>
                      )}
                      {svc.kind === 'app' ? (
                        <div>
                          <div className={label}>Health path</div>
                          <input
                            className={`${input} mt-1 font-mono`}
                            value={svc.healthPath}
                            disabled={!!createdId}
                            onChange={(e) => updateService(i, { healthPath: e.target.value })}
                            placeholder="/"
                          />
                        </div>
                      ) : null}
                      <div>
                        <div className={label}>Service env (optional)</div>
                        <textarea
                          className="mt-1 min-h-9 w-full resize-y rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 py-2 font-mono text-[10px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40"
                          value={svc.envText}
                          disabled={!!createdId}
                          onChange={(e) => updateService(i, { envText: e.target.value })}
                          placeholder={'KEY=value'}
                        />
                      </div>
                    </div>
                  </div>
                ))}
              </div>
              <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
                Each web app gets its own subdomain, port and process. Static sites are served from the
                release directory; workers run without a port. Pinned services reach each other over
                localhost.
              </p>
            </div>
          )}

          <div className="sm:col-span-2">
            <div className={label}>{stackMode ? 'Shared environment (optional)' : 'Environment (optional)'}</div>
            <textarea
              className="mt-1 min-h-20 w-full resize-y rounded-lg border border-zinc-800 bg-zinc-950/70 p-3 font-mono text-[11px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40"
              value={envText}
              onChange={(e) => setEnvText(e.target.value)}
              placeholder={'NODE_ENV=production\nDATABASE_URL=...'}
            />
            <p className="mt-1 text-[10px] text-zinc-600">
              One KEY=value per line. Encrypted at rest.{' '}
              {stackMode
                ? 'Shared by every service; per-service values override it.'
                : 'Replicas share the same env; the scheduler spreads them across matching phones.'}
            </p>
          </div>
        </div>

        {create.isError ? (
          <div className="mt-3 text-[11px] text-red-400">
            {createdId ? (
              <>
                Project created, but the source upload failed: {String(create.error)}. Retry below or{' '}
                <Link to={stackMode ? `/stacks/${createdId}` : `/projects/${createdId}`} className="underline">
                  open {stackMode ? 'the stack' : 'the project'}
                </Link>
                .
              </>
            ) : (
              String(create.error)
            )}
          </div>
        ) : null}

        <div className="mt-5 flex items-center justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" disabled={create.isPending || !ready}>
            {create.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Plus size={13} />}
            {createdId
              ? 'Retry source upload'
              : stackMode
                ? sourceType === 'upload'
                  ? 'Create stack & upload'
                  : 'Create stack'
                : sourceType === 'upload'
                  ? 'Create & upload'
                  : 'Create project'}
          </Button>
        </div>
      </form>
    </div>
  )
}

function ProjectCard({ project }: { project: ProjectView }) {
  const qc = useQueryClient()
  const deploy = useMutation({
    mutationFn: () => api.deployProject(project.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['project', project.id] })
      qc.invalidateQueries({ queryKey: ['projects'] })
    },
  })
  const status = aggregateStatus(project)
  const services = project.services ?? []
  const isStack = project.kind === 'stack' && services.length > 0
  const primary = primaryService(project)
  const allPlacements = isStack ? services.flatMap((s) => s.placements) : project.placements
  const serverNames = [...new Set(allPlacements.map((p) => p.serverName || p.serverId))].join(', ')
  const ports = allPlacements.map((p) => p.port).filter((p) => p > 0)
  const latestAt = isStack
    ? services.reduce((best, s) => Math.max(best, s.latest?.startedAt ?? 0), 0)
    : project.latest?.startedAt ?? 0
  const detailPath = isStack ? `/stacks/${project.id}` : `/projects/${project.id}`

  return (
    <Card className="group flex flex-col p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-1.5">
            <Link to={detailPath} className="truncate text-sm font-medium text-zinc-100 hover:text-emerald-300">
              {project.name}
            </Link>
            {isStack ? (
              <Badge tone="info" className="shrink-0">
                stack · {services.length}
              </Badge>
            ) : project.kind === 'static' ? (
              <Badge tone="info" className="shrink-0">
                static
              </Badge>
            ) : project.kind === 'worker' ? (
              <Badge className="shrink-0">worker</Badge>
            ) : null}
          </div>
          {primary?.hostname || project.hostname ? (
            <a
              href={primary?.url || project.url}
              target="_blank"
              rel="noreferrer"
              title={`Open ${primary?.hostname || project.hostname}`}
              className="mt-0.5 flex min-w-0 items-center gap-1 font-mono text-[10px] text-zinc-500 hover:text-emerald-300 hover:underline"
            >
              <Globe size={10} className="shrink-0" />
              <span className="truncate">{primary?.hostname || project.hostname}</span>
            </a>
          ) : (
            <div className="mt-0.5 flex min-w-0 items-center gap-1 font-mono text-[10px] text-zinc-600">
              <Server size={10} className="shrink-0" /> no public URL
            </div>
          )}
        </div>
        <DeployBadge status={status} />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-2 text-[10px] text-zinc-500">
        <span className="flex items-center gap-1 truncate" title={serverNames}>
          <Smartphone size={10} /> {serverNames || 'no phone yet'}
        </span>
        <span className="flex items-center gap-1 truncate">
          <GitBranch size={10} /> {project.branch || 'main'}
        </span>
        <span className="flex items-center gap-1 min-w-0">
          <Server size={10} className="shrink-0" />
          <span className="truncate">
            {isStack ? `${services.length} services` : `${project.placements.length}/${project.replicas} replicas`}
          </span>
        </span>
        <span className="truncate" title={ports.join(', ')}>
          {ports.length ? `ports ${ports.join(', ')}` : project.kind === 'worker' ? 'no port' : 'ports —'}
        </span>
        <span className="col-span-2 truncate" title={latestAt ? time(latestAt) : ''}>
          deploy {latestAt ? ago(latestAt) : 'never'}
        </span>
      </div>

      <div className="mt-3 flex items-center justify-between border-t border-zinc-800/70 pt-2.5">
        <Link to={detailPath} className="text-[10px] text-zinc-500 hover:text-zinc-200">
          manage →
        </Link>
        <div className="flex items-center gap-1">
          <Button
            size="sm"
            variant="ghost"
            disabled={deploy.isPending}
            onClick={() => deploy.mutate()}
            title="Deploy latest commit"
          >
            {deploy.isPending ? <Spinner className="h-3 w-3" /> : <ArrowRight size={12} />} Deploy
          </Button>
        </div>
      </div>
      {deploy.isError ? <div className="mt-1 text-[10px] text-red-400">{String(deploy.error)}</div> : null}
    </Card>
  )
}

export default function Projects() {
  const [creating, setCreating] = useState(false)
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, refetchInterval: 8000 })
  const list = projects.data?.projects ?? []

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Projects</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            {list.length === 0 ? 'nothing deployed yet' : `${list.length} project${list.length === 1 ? '' : 's'} across your phones`}
          </p>
        </div>
        <Button variant="primary" onClick={() => setCreating(true)}>
          <Plus size={14} /> New project
        </Button>
      </div>

      {projects.isLoading ? (
        <div className="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-36 animate-pulse rounded-xl bg-zinc-900/40" />
          ))}
        </div>
      ) : list.length === 0 ? (
        <div className="mt-6">
          <EmptyState icon={<Boxes size={22} />} title="No projects yet">
            <p>
              Point phleet at a git repo, give it a start command, and it clones, builds and supervises it on a phone.
              Every project gets its own subdomain automatically.
            </p>
            <pre className="mt-3 overflow-x-auto rounded-lg bg-zinc-950 p-3 text-left font-mono text-[10px] leading-relaxed text-zinc-400 ring-1 ring-zinc-800">
{`start:  node server.js      # PORT is injected
build:  npm ci && npm run build
health: /health`}
            </pre>
          </EmptyState>
        </div>
      ) : (
        <div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((p) => (
            <ProjectCard key={p.id} project={p} />
          ))}
        </div>
      )}

      {creating ? <NewProject onClose={() => setCreating(false)} /> : null}
    </div>
  )
}
