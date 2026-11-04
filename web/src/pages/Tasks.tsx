import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Clock, FileText, Pencil, Play, Plus, RefreshCw, Server, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import { ConfirmDialog, Dialog } from '../components/Dialog'
import { Badge, Button, Card, EmptyState, Spinner, cn } from '../components/ui'
import { ago, bytes, time } from '../lib/format'
import type { ProjectView, Server as Phone, Task } from '../types'

const input =
  'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40'
const label = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

const presets = [
  { label: 'Every 15 minutes', expr: '*/15 * * * *' },
  { label: 'Every hour', expr: '0 * * * *' },
  { label: 'Every day at 03:00', expr: '0 3 * * *' },
  { label: 'Every Monday at 03:00', expr: '0 3 * * 1' },
  { label: 'First day of the month', expr: '0 4 1 * *' },
]

type ProjectOption = {
  id: string
  name: string
  kind?: string
  serverIds: Set<string>
}

function flattenProjects(projects: ProjectView[]): ProjectOption[] {
  const out: ProjectOption[] = []
  for (const p of projects) {
    if (p.kind === 'stack') {
      for (const svc of p.services ?? []) {
        out.push({
          id: svc.id,
          name: `${p.name} / ${svc.name}`,
          kind: svc.kind,
          serverIds: new Set(svc.placements.map((pl) => pl.serverId)),
        })
      }
      continue
    }
    out.push({
      id: p.id,
      name: p.name,
      kind: p.kind,
      serverIds: new Set(p.placements.map((pl) => pl.serverId)),
    })
  }
  return out.sort((a, b) => a.name.localeCompare(b.name))
}

type Draft = {
  name: string
  target: 'project' | 'server'
  serverId: string
  projectId: string
  command: string
  cron: string
  timeoutSec: number
  enabled: boolean
}

function draftFrom(task: Task | null, servers: Phone[]): Draft {
  return {
    name: task?.name ?? '',
    target: task?.projectId ? 'project' : 'server',
    serverId: task?.serverId ?? servers[0]?.id ?? '',
    projectId: task?.projectId ?? '',
    command: task?.command ?? '',
    cron: task?.cron ?? '0 3 * * *',
    timeoutSec: task?.timeoutSec ?? 300,
    enabled: task?.enabled ?? true,
  }
}

function TaskForm({
  open,
  task,
  servers,
  projects,
  onClose,
}: {
  open: boolean
  task: Task | null
  servers: Phone[]
  projects: ProjectOption[]
  onClose: () => void
}) {
  const qc = useQueryClient()
  const [form, setForm] = useState<Draft>(() => draftFrom(task, servers))
  const [error, setError] = useState('')
  const taskKey = task?.id ?? 'new'

  useEffect(() => {
    if (!open) return
    setForm(draftFrom(task, servers))
    setError('')
    // Reset only when the dialog opens or switches task; a background servers
    // refetch must not erase what the user is typing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, taskKey])

  const availableProjects = useMemo(
    () => projects.filter((p) => p.serverIds.has(form.serverId)),
    [projects, form.serverId],
  )
  const projectOk = form.target === 'server' || availableProjects.some((p) => p.id === form.projectId)

  const save = useMutation({
    mutationFn: () => {
      const body = {
        name: form.name.trim(),
        serverId: form.serverId,
        projectId: form.target === 'project' ? form.projectId : '',
        command: form.command.trim(),
        cron: form.cron.trim(),
        timeoutSec: form.timeoutSec,
        enabled: form.enabled,
      }
      return task ? api.updateTask(task.id, body) : api.createTask(body)
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tasks'] })
      onClose()
    },
    onError: (e) => setError(String(e)),
  })

  const ready =
    !!form.name.trim() &&
    !!form.serverId &&
    !!form.command.trim() &&
    !!form.cron.trim() &&
    projectOk

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={task ? 'Edit scheduled task' : 'New scheduled task'}
      description="Runs on the phone as the Termux user. The agent keeps the schedule locally, so a panel outage does not skip runs."
      width="max-w-2xl"
      busy={save.isPending}
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="task-form"
            variant="primary"
            disabled={save.isPending || !ready}
          >
            {save.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : null}
            {task ? 'Save' : 'Create task'}
          </Button>
        </>
      }
    >
      <form
        id="task-form"
        onSubmit={(e) => {
          e.preventDefault()
          if (ready) save.mutate()
        }}
        className="grid gap-3 sm:grid-cols-2"
      >
        <div className="sm:col-span-2">
          <div className={label}>Name</div>
          <input
            className={`${input} mt-1`}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder="Nightly prune"
            data-autofocus
          />
        </div>

        <div className="sm:col-span-2">
          <div className={label}>Runs on</div>
          <div className="mt-1 grid grid-cols-2 gap-1.5">
            {(
              [
                { id: 'server', label: 'Phone (any command)' },
                { id: 'project', label: 'Project (release + env)' },
              ] as const
            ).map((t) => (
              <button
                key={t.id}
                type="button"
                onClick={() => setForm({ ...form, target: t.id })}
                className={`h-8 rounded-lg border text-xs transition-colors ${
                  form.target === t.id
                    ? 'border-emerald-400/50 bg-emerald-400/10 text-emerald-300'
                    : 'border-zinc-800 bg-zinc-950/70 text-zinc-400 hover:text-zinc-200'
                }`}
              >
                {t.label}
              </button>
            ))}
          </div>
        </div>

        <div>
          <div className={label}>Phone</div>
          <select
            className={`${input} mt-1`}
            value={form.serverId}
            onChange={(e) => setForm({ ...form, serverId: e.target.value })}
          >
            <option value="">Select a phone</option>
            {servers.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name} {s.online ? '· online' : '· offline'}
              </option>
            ))}
          </select>
        </div>

        {form.target === 'project' ? (
          <div>
            <div className={label}>Project</div>
            <select
              className={`${input} mt-1`}
              value={form.projectId}
              onChange={(e) => setForm({ ...form, projectId: e.target.value })}
            >
              <option value="">Select a project on this phone</option>
              {form.projectId && !availableProjects.some((p) => p.id === form.projectId) ? (
                <option value={form.projectId}>project is not on this phone</option>
              ) : null}
              {availableProjects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                  {p.kind && p.kind !== 'app' ? ` · ${p.kind}` : ''}
                </option>
              ))}
            </select>
            {!projectOk ? (
              <p className="mt-1 text-[10px] text-amber-300/80">
                Pick a project that currently runs on this phone, or choose another phone.
              </p>
            ) : availableProjects.length === 0 ? (
              <p className="mt-1 text-[10px] text-amber-300/80">
                This phone runs no projects yet. Deploy one first, or use a phone-level task.
              </p>
            ) : null}
          </div>
        ) : (
          <div className="flex items-end">
            <p className="text-[10px] leading-relaxed text-zinc-600">
              Runs in <span className="font-mono">$HOME</span> with the agent environment, but never receives
              agent tokens.
            </p>
          </div>
        )}

        <div className="sm:col-span-2">
          <div className={label}>Command</div>
          <textarea
            className="mt-1 min-h-20 w-full resize-y rounded-lg border border-zinc-800 bg-zinc-950/70 p-3 font-mono text-[11px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40"
            value={form.command}
            onChange={(e) => setForm({ ...form, command: e.target.value })}
            placeholder="npm run prune"
          />
        </div>

        <div className="sm:col-span-2">
          <div className="flex items-center justify-between">
            <div className={label}>Schedule</div>
            <select
              className="h-7 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-[10px] text-zinc-300 outline-none"
              value={presets.some((p) => p.expr === form.cron) ? form.cron : ''}
              onChange={(e) => e.target.value && setForm({ ...form, cron: e.target.value })}
            >
              <option value="">Custom</option>
              {presets.map((p) => (
                <option key={p.expr} value={p.expr}>
                  {p.label}
                </option>
              ))}
            </select>
          </div>
          <input
            className={`${input} mt-1 font-mono`}
            value={form.cron}
            onChange={(e) => setForm({ ...form, cron: e.target.value })}
            placeholder="0 3 * * *"
          />
          <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
            Five fields: minute hour day-of-month month weekday. Runs in the phone&apos;s local time. Names
            like <span className="font-mono">mon</span> and <span className="font-mono">jan</span>, ranges,
            lists and steps are supported.
          </p>
        </div>

        <div>
          <div className={label}>Timeout (seconds)</div>
          <input
            type="number"
            min={1}
            max={3600}
            className={`${input} mt-1`}
            value={form.timeoutSec}
            onChange={(e) =>
              setForm({ ...form, timeoutSec: Math.max(1, Math.min(3600, Number(e.target.value) || 1)) })
            }
          />
        </div>
        <div className="flex items-end pb-1">
          <label className="flex cursor-pointer items-center gap-2 text-[11px] text-zinc-300">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            Enabled
          </label>
        </div>

        {error ? <div className="text-[11px] text-red-400 sm:col-span-2">{error}</div> : null}
      </form>
    </Dialog>
  )
}

function TaskBadge({ task }: { task: Task }) {
  if (task.lastStatus === 'running') return <Badge tone="info">running</Badge>
  if (task.lastStatus === 'succeeded') return <Badge tone="good">ok</Badge>
  if (task.lastStatus === 'failed') return <Badge tone="bad">failed</Badge>
  if (task.lastStatus === 'interrupted') return <Badge tone="warn">interrupted</Badge>
  if (task.lastStatus) return <Badge>{task.lastStatus}</Badge>
  return <Badge>never run</Badge>
}

function TaskRow({
  task,
  phoneOnline,
  onEdit,
  onLogs,
  onDelete,
}: {
  task: Task
  phoneOnline: boolean
  onEdit: () => void
  onLogs: () => void
  onDelete: () => void
}) {
  const qc = useQueryClient()
  const run = useMutation({
    mutationFn: () => api.runTask(task.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks'] }),
  })
  const toggle = useMutation({
    mutationFn: () => api.updateTask(task.id, { enabled: !task.enabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks'] }),
  })

  return (
    <tr className="hover:bg-zinc-800/20">
      <td className="px-4 py-3">
        <div className="flex flex-wrap items-center gap-1.5 text-zinc-200">
          <span className="font-medium">{task.name}</span>
          {!task.enabled ? <Badge>paused</Badge> : null}
          {!task.available ? <Badge tone="warn">project not on this phone</Badge> : null}
        </div>
        <div className="mt-0.5 truncate font-mono text-[10px] text-zinc-500" title={task.command}>
          {task.command}
        </div>
      </td>
      <td className="px-3 py-3">
        <div className="flex items-center gap-1 text-zinc-300">
          <Server size={10} className="text-zinc-500" />
          {task.serverName || task.serverId.slice(0, 8)}
        </div>
        <div className="mt-0.5 text-[10px] text-zinc-600">
          {task.projectName ? `project · ${task.projectName}` : 'phone'}
        </div>
      </td>
      <td className="px-3 py-3">
        <div className="font-mono text-[10px] text-zinc-300">{task.cron}</div>
        <div className="mt-0.5 text-[10px] text-zinc-600">
          {task.enabled && task.nextRun ? `next ${time(task.nextRun)}` : '—'}
        </div>
      </td>
      <td className="px-3 py-3">
        <TaskBadge task={task} />
        <div className="mt-0.5 text-[10px] text-zinc-600">
          {task.lastRun ? ago(task.lastRun) : 'never'}
          {task.lastExit ? ` · exit ${task.lastExit}` : ''}
        </div>
      </td>
      <td className="px-3 py-3">
        <button
          type="button"
          onClick={() => toggle.mutate()}
          disabled={toggle.isPending}
          className={cn(
            'relative h-5 w-9 rounded-full transition-colors',
            task.enabled ? 'bg-emerald-500/70' : 'bg-zinc-700',
          )}
          aria-label={task.enabled ? 'Pause task' : 'Enable task'}
        >
          <span
            className={cn(
              'absolute top-0.5 h-4 w-4 rounded-full bg-zinc-100 transition-all',
              task.enabled ? 'left-[18px]' : 'left-0.5',
            )}
          />
        </button>
      </td>
      <td className="px-4 py-3">
        <div className="flex items-center justify-end gap-1">
          <Button
            size="sm"
            variant="ghost"
            disabled={run.isPending || !task.available || !phoneOnline || task.lastStatus === 'running'}
            onClick={() => run.mutate()}
            title={!phoneOnline ? 'The phone is offline' : 'Run now'}
          >
            {run.isPending ? <Spinner className="h-3 w-3" /> : <Play size={12} />}
          </Button>
          <Button size="sm" variant="ghost" onClick={onLogs} title="View logs">
            <FileText size={12} />
          </Button>
          <Button size="sm" variant="ghost" onClick={onEdit} title="Edit">
            <Pencil size={12} />
          </Button>
          <Button size="sm" variant="ghost" onClick={onDelete} title="Delete">
            <Trash2 size={12} className="text-red-300/80" />
          </Button>
        </div>
        {run.isError ? <div className="mt-1 text-right text-[10px] text-red-400">{String(run.error)}</div> : null}
      </td>
    </tr>
  )
}

function TaskLogsDialog({ task, onClose }: { task: Task | null; onClose: () => void }) {
  const logs = useQuery({
    queryKey: ['task-logs', task?.id],
    queryFn: () => api.taskLogs(task!.id),
    enabled: !!task,
    retry: false,
  })
  return (
    <Dialog
      open={!!task}
      onClose={onClose}
      title={task ? `${task.name} · logs` : 'Task logs'}
      description="Tail of the last run stored on the phone. Full history is rotated at 1 MB."
      width="max-w-3xl"
      footer={
        <>
          <Button type="button" variant="ghost" onClick={() => logs.refetch()} disabled={logs.isFetching}>
            <RefreshCw size={12} /> Refresh
          </Button>
          <Button type="button" variant="primary" onClick={onClose}>
            Close
          </Button>
        </>
      }
    >
      {logs.isLoading ? (
        <div className="py-6 text-center text-xs text-zinc-500">loading…</div>
      ) : logs.isError ? (
        <div className="rounded-lg border border-red-900/40 bg-red-500/5 p-3 text-[11px] text-red-300">
          {String(logs.error)}
        </div>
      ) : (
        <>
          <div className="flex items-center justify-between text-[10px] text-zinc-600">
            <span className="truncate font-mono">{logs.data?.path}</span>
            <span>
              {logs.data ? bytes(logs.data.size) : ''} {logs.data?.truncated ? '· tail' : ''}
            </span>
          </div>
          <pre className="mt-2 max-h-[60vh] overflow-auto rounded-lg bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed text-zinc-300 ring-1 ring-zinc-800">
            {logs.data?.logs || 'No log output yet.'}
          </pre>
        </>
      )}
    </Dialog>
  )
}

export default function Tasks() {
  const qc = useQueryClient()
  const tasks = useQuery({ queryKey: ['tasks'], queryFn: api.tasks, refetchInterval: 5000 })
  const servers = useQuery({ queryKey: ['servers'], queryFn: api.servers, refetchInterval: 15000 })
  const projects = useQuery({ queryKey: ['projects'], queryFn: api.projects, refetchInterval: 15000 })
  const [editing, setEditing] = useState<Task | 'new' | null>(null)
  const [logsFor, setLogsFor] = useState<Task | null>(null)
  const [deleteFor, setDeleteFor] = useState<Task | null>(null)

  const projectOptions = useMemo(
    () => flattenProjects(projects.data?.projects ?? []),
    [projects.data],
  )
  const list = tasks.data?.tasks ?? []
  const online = useMemo(() => {
    const set = new Set<string>()
    for (const s of servers.data?.servers ?? []) if (s.online) set.add(s.id)
    return set
  }, [servers.data])

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteTask(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['tasks'] })
      setDeleteFor(null)
    },
  })

  const active = list.filter((t) => t.enabled).length
  const failing = list.filter((t) => t.lastStatus === 'failed').length

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 md:px-8 md:py-8">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Scheduled tasks</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            {list.length === 0
              ? 'no tasks yet'
              : `${list.length} task${list.length === 1 ? '' : 's'} · ${active} active${failing ? ` · ${failing} failing` : ''}`}
          </p>
        </div>
        <Button variant="primary" onClick={() => setEditing('new')}>
          <Plus size={14} /> New task
        </Button>
      </div>

      {tasks.isLoading ? (
        <div className="mt-6 grid gap-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-16 animate-pulse rounded-xl bg-zinc-900/40" />
          ))}
        </div>
      ) : list.length === 0 ? (
        <div className="mt-6">
          <EmptyState icon={<Clock size={22} />} title="No scheduled tasks">
            <p>
              Run a command on a phone on a cron schedule: database maintenance, cache cleanup, a nightly
              job, or a phone-level script. Schedules live on the device, so they keep running when the panel
              is asleep.
            </p>
          </EmptyState>
        </div>
      ) : (
        <Card className="mt-5 overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[11px]">
              <thead className="bg-zinc-950/40 text-[9px] tracking-wider text-zinc-500 uppercase">
                <tr>
                  <th className="px-4 py-2 font-medium">Task</th>
                  <th className="px-3 py-2 font-medium">Target</th>
                  <th className="px-3 py-2 font-medium">Schedule</th>
                  <th className="px-3 py-2 font-medium">Last run</th>
                  <th className="px-3 py-2 font-medium">On</th>
                  <th className="px-4 py-2 text-right font-medium">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/50">
                {list.map((task) => (
                  <TaskRow
                    key={task.id}
                    task={task}
                    phoneOnline={online.has(task.serverId)}
                    onEdit={() => setEditing(task)}
                    onLogs={() => setLogsFor(task)}
                    onDelete={() => setDeleteFor(task)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}

      <p className="mt-3 text-[10px] leading-relaxed text-zinc-600">
        Project tasks run in the project&apos;s current release with its environment and{' '}
        <span className="font-mono">PHLEET_DATA_DIR</span>. Phone tasks run in{' '}
        <span className="font-mono">$HOME</span>. Both run as the Termux user, without root.
      </p>

      <TaskForm
        open={editing !== null}
        task={editing && editing !== 'new' ? editing : null}
        servers={servers.data?.servers ?? []}
        projects={projectOptions}
        onClose={() => setEditing(null)}
      />
      <TaskLogsDialog task={logsFor} onClose={() => setLogsFor(null)} />
      <ConfirmDialog
        open={!!deleteFor}
        onClose={() => setDeleteFor(null)}
        title="Delete scheduled task?"
        description={
          deleteFor
            ? `${deleteFor.name} will stop running on ${deleteFor.serverName || deleteFor.serverId}. Logs stay on the phone.`
            : ''
        }
        confirmLabel="Delete task"
        danger
        busy={remove.isPending}
        onConfirm={() => deleteFor && remove.mutate(deleteFor.id)}
      />
    </div>
  )
}
