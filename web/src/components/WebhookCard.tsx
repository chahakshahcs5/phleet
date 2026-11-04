import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, RefreshCw, Webhook } from 'lucide-react'
import { useState } from 'react'
import { api } from '../api'
import { ConfirmDialog } from './Dialog'
import { Badge, Button, Card, Spinner } from './ui'

export default function WebhookCard({ projectId, branch }: { projectId: string; branch?: string }) {
  const qc = useQueryClient()
  const info = useQuery({ queryKey: ['webhook', projectId], queryFn: () => api.projectWebhook(projectId) })
  const [secret, setSecret] = useState('')
  const [confirm, setConfirm] = useState(false)
  const [copied, setCopied] = useState('')

  const rotate = useMutation({
    mutationFn: () => api.rotateProjectWebhook(projectId),
    onSuccess: (res) => {
      setSecret(res.secret ?? '')
      setConfirm(false)
      qc.invalidateQueries({ queryKey: ['webhook', projectId] })
      qc.invalidateQueries({ queryKey: ['project', projectId] })
    },
  })

  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(label)
      window.setTimeout(() => setCopied(''), 1500)
    } catch {
      /* clipboard may be blocked; the value is still selectable */
    }
  }

  const row = (label: string, value: string) => (
    <div className="flex min-w-0 items-center gap-2">
      <span className="w-14 shrink-0 text-[10px] text-zinc-600">{label}</span>
      <code className="min-w-0 flex-1 truncate rounded-md bg-zinc-950/70 px-2 py-1 font-mono text-[10px] text-zinc-300" title={value}>
        {value}
      </code>
      <button
        type="button"
        onClick={() => copy(label, value)}
        className="rounded p-1 text-zinc-500 transition-colors hover:bg-zinc-800/60 hover:text-zinc-200"
        aria-label={`Copy ${label} webhook URL`}
      >
        {copied === label ? <Check size={12} className="text-emerald-400" /> : <Copy size={12} />}
      </button>
    </div>
  )

  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Webhook size={12} /> Webhooks
        </div>
        {info.data?.configured ? <Badge tone="good">configured</Badge> : <Badge>not configured</Badge>}
      </div>
      <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
        Push to <span className="font-mono">{branch || 'main'}</span> and phleet deploys that commit. The
        secret is stored encrypted; rotate it if it leaks.
      </p>

      {info.isLoading ? (
        <div className="mt-3 text-[11px] text-zinc-500">loading…</div>
      ) : info.isError ? (
        <div className="mt-3 text-[11px] text-red-400">{String(info.error)}</div>
      ) : (
        <div className="mt-3 space-y-1.5">
          {row('GitHub', info.data?.githubUrl ?? '')}
          {row('GitLab', info.data?.gitlabUrl ?? '')}
        </div>
      )}

      {secret ? (
        <div className="mt-3 rounded-lg border border-amber-900/40 bg-amber-500/5 p-2.5">
          <div className="text-[10px] text-amber-300">Copy this secret now — it is shown only once.</div>
          <div className="mt-1 flex items-center gap-2">
            <code className="min-w-0 flex-1 break-all rounded bg-zinc-950/70 px-2 py-1 font-mono text-[10px] text-zinc-200">
              {secret}
            </code>
            <button
              type="button"
              onClick={() => copy('secret', secret)}
              className="rounded p-1 text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100"
              aria-label="Copy webhook secret"
            >
              {copied === 'secret' ? <Check size={12} className="text-emerald-400" /> : <Copy size={12} />}
            </button>
          </div>
        </div>
      ) : null}

      <div className="mt-3 flex items-center justify-between gap-2">
        <span className="text-[10px] text-zinc-600">
          {info.data?.configured
            ? 'GitHub uses X-Hub-Signature-256; GitLab uses the secret token.'
            : 'Generate a secret, then paste it into the provider.'}
        </span>
        <Button
          size="sm"
          variant={info.data?.configured ? 'outline' : 'primary'}
          disabled={rotate.isPending}
          onClick={() => (info.data?.configured ? setConfirm(true) : rotate.mutate())}
        >
          {rotate.isPending ? <Spinner className="h-3 w-3" /> : <RefreshCw size={12} />}
          {info.data?.configured ? 'Rotate secret' : 'Generate secret'}
        </Button>
      </div>
      {rotate.isError ? <div className="mt-2 text-[10px] text-red-400">{String(rotate.error)}</div> : null}

      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title="Rotate webhook secret?"
        description="The current secret stops working immediately. Update GitHub or GitLab with the new one after rotating."
        confirmLabel="Rotate"
        danger
        busy={rotate.isPending}
        onConfirm={() => rotate.mutate()}
      />
    </Card>
  )
}
