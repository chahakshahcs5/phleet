import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Check, Copy, KeyRound, Server, Smartphone, Terminal } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, ApiError } from '../api'
import type { ProvisionResult } from '../types'
import { Button, Card, Spinner, cn } from '../components/ui'

const field =
  'h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/40'
const caption = 'text-[10px] font-medium tracking-widest text-zinc-500 uppercase'

function shq(v: string): string {
  return `'${v.replace(/'/g, "'\\''")}'`
}

export default function AddPhone() {
  const qc = useQueryClient()
  const setup = useQuery({ queryKey: ['setup'], queryFn: api.setupInfo, staleTime: 0 })
  const [tab, setTab] = useState<'ssh' | 'manual'>('ssh')
  const [name, setName] = useState('')
  const [target, setTarget] = useState('')
  const [port, setPort] = useState('')
  const [identity, setIdentity] = useState('')
  const [serverUrl, setServerUrl] = useState('')
  const [copied, setCopied] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; output: string } | null>(null)

  useEffect(() => {
    if (!serverUrl && setup.data?.serverUrl) setServerUrl(setup.data.serverUrl)
  }, [setup.data, serverUrl])

  const manualName = name.trim() || 'phone'
  const command =
    serverUrl && setup.data?.agentToken
      ? `curl -fsSL ${shq(serverUrl)}/download/bootstrap.sh \\\n  | bash -s -- --server ${shq(serverUrl)} --token ${shq(setup.data.agentToken)} --name ${shq(manualName)}`
      : ''

  const provision = useMutation({
    mutationFn: () =>
      api.provisionServer({
        name: name.trim(),
        target: target.trim(),
        port: Number(port) || 0,
        identityFile: identity.trim(),
        serverUrl,
      }),
    onSuccess: (res) => {
      setResult({ ok: true, output: res.output || '' })
      qc.invalidateQueries({ queryKey: ['servers'] })
      qc.invalidateQueries({ queryKey: ['events'] })
    },
    onError: (err) => {
      const data = (err as ApiError).data as ProvisionResult | undefined
      setResult({ ok: false, output: data?.output || String(err) })
    },
  })

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-6 md:px-8 md:py-8">
      <Link to="/" className="inline-flex items-center gap-1 text-[11px] text-zinc-500 hover:text-zinc-300">
        <ArrowLeft size={12} /> fleet
      </Link>
      <div className="mt-2 flex items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Add phone</h1>
          <p className="mt-0.5 text-xs text-zinc-500">
            Install the agent once. After that the phone dials out; no ports are opened on it.
          </p>
        </div>
        <Smartphone size={20} className="text-zinc-600" />
      </div>

      <Card className="mt-4 p-4">
        <div className={caption}>Control plane URL</div>
        <p className="mt-1 text-[10px] leading-relaxed text-zinc-600">
          The address the phone will use to download the agent and stay connected. Use your LAN URL
          (<span className="font-mono">http://192.168.1.2:8080</span>) or the public Cloudflare URL.
        </p>
        <input
          className={`${field} mt-2 font-mono`}
          value={serverUrl}
          onChange={(e) => setServerUrl(e.target.value)}
          placeholder="http://192.168.1.2:8080"
        />
      </Card>

      <div className="mt-4 flex gap-1 border-b border-zinc-800/70">
        {(
          [
            { id: 'ssh', label: 'Install over SSH', icon: <Terminal size={13} /> },
            { id: 'manual', label: 'Manual (Termux)', icon: <KeyRound size={13} /> },
          ] as { id: typeof tab; label: string; icon: React.ReactNode }[]
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

      {tab === 'ssh' ? (
        <Card className="mt-4 p-4">
          <p className="text-[11px] leading-relaxed text-zinc-400">
            Runs the bootstrap over SSH from this control plane. The phone needs Termux with <span className="font-mono">sshd</span>{' '}
            running. Key-based login only (BatchMode).
          </p>
          {setup.data && !setup.data.sshAvailable ? (
            <div className="mt-2 rounded-lg border border-amber-900/40 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-300">
              OpenSSH client not found on the control plane machine.
            </div>
          ) : null}

          <div className="mt-3 grid gap-3 sm:grid-cols-2">
            <div>
              <div className={caption}>Name</div>
              <input className={`${field} mt-1`} value={name} onChange={(e) => setName(e.target.value)} placeholder="pixel-4a" />
            </div>
            <div>
              <div className={caption}>SSH target</div>
              <input
                className={`${field} mt-1 font-mono`}
                value={target}
                onChange={(e) => setTarget(e.target.value)}
                placeholder="pixel-4a-termux or user@192.168.1.34"
              />
            </div>
            <div>
              <div className={caption}>Port</div>
              <input
                className={`${field} mt-1 font-mono`}
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="from SSH config (e.g. 8022)"
                inputMode="numeric"
              />
            </div>
            <div>
              <div className={caption}>Identity file (optional)</div>
              <input
                className={`${field} mt-1 font-mono`}
                value={identity}
                onChange={(e) => setIdentity(e.target.value)}
                placeholder="use SSH config / default keys"
              />
            </div>
          </div>

          <p className="mt-2 text-[10px] leading-relaxed text-zinc-600">
            With a config alias, leave port and identity empty. If phleetd runs as a service (SYSTEM), the
            user's <span className="font-mono">~/.ssh/config</span> and keys are not visible — use{' '}
            <span className="font-mono">user@host</span> plus an explicit identity file readable by SYSTEM.
          </p>

          {result ? (
            <div
              className={cn(
                'mt-3 rounded-lg border px-3 py-2 text-[11px]',
                result.ok
                  ? 'border-emerald-900/40 bg-emerald-500/5 text-emerald-300'
                  : 'border-red-900/40 bg-red-500/5 text-red-300',
              )}
            >
              {result.ok ? 'Installed. The phone should appear in the fleet within a few seconds.' : 'Install failed.'}
            </div>
          ) : null}
          {result?.output ? (
            <pre className="mt-2 max-h-72 overflow-auto rounded-lg bg-zinc-950 p-3 font-mono text-[10px] leading-relaxed whitespace-pre-wrap text-zinc-400 ring-1 ring-zinc-800">
              {result.output}
            </pre>
          ) : null}

          <div className="mt-4 flex justify-end">
            <Button
              variant="primary"
              size="sm"
              disabled={provision.isPending || !target.trim() || !serverUrl.trim() || setup.data?.sshAvailable === false}
              onClick={() => {
                setResult(null)
                provision.mutate()
              }}
            >
              {provision.isPending ? <Spinner className="h-3 w-3 border-zinc-900/40 border-t-zinc-900" /> : <Server size={12} />}
              {provision.isPending ? 'Installing…' : 'Install agent over SSH'}
            </Button>
          </div>
        </Card>
      ) : (
        <Card className="mt-4 p-4">
          <p className="text-[11px] leading-relaxed text-zinc-400">
            Open Termux on the phone (F-Droid build) and paste this. It installs git, the agent, a wake lock and a
            Termux:Boot hook.
          </p>
          <div className="mt-3">
            <div className={caption}>Name</div>
            <input className={`${field} mt-1`} value={name} onChange={(e) => setName(e.target.value)} placeholder="pixel-4a" />
          </div>
          <div className="mt-3 flex items-center justify-between">
            <div className={caption}>Command</div>
            <Button size="sm" variant="ghost" disabled={!command} onClick={copy}>
              {copied ? <Check size={12} /> : <Copy size={12} />} {copied ? 'Copied' : 'Copy'}
            </Button>
          </div>
          <pre className="mt-1 overflow-x-auto rounded-lg bg-zinc-950 p-3 font-mono text-[10px] leading-relaxed whitespace-pre-wrap text-zinc-300 ring-1 ring-zinc-800">
            {command || 'waiting for the control plane URL…'}
          </pre>
          <p className="mt-2 text-[10px] leading-relaxed text-zinc-600">
            The command contains the shared enrollment token. Treat it as a secret; anyone with it can register a new phone, but not take over an already-enrolled one.
          </p>
        </Card>
      )}

      <Card className="mt-4 p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          No Termux? Rooted Android
        </div>
        <p className="mt-2 text-[11px] leading-relaxed text-zinc-400">
          A rooted phone can run the agent straight from <span className="font-mono">/data/local/tmp</span> with no
          Linux userland. It becomes a monitoring node: metrics, battery, thermal, shell and files, but no deploys or
          data addons (there is no git or package manager for the agent to use).
        </p>
        <pre className="mt-2 overflow-x-auto rounded-lg bg-zinc-950 p-3 font-mono text-[10px] leading-relaxed whitespace-pre-wrap text-zinc-300 ring-1 ring-zinc-800">
{`adb push dist/bin/phleet-agent-linux-arm64 /data/local/tmp/phleet/bin/phleet-agent
adb push scripts/bootstrap-android-root.sh /data/local/tmp/phleet/
adb shell "su -c 'sh /data/local/tmp/phleet/bootstrap-android-root.sh \\
  --server ${serverUrl || 'https://fleet.example.com'} --token ${setup.data?.agentToken ?? '<agent-token>'} --name ${manualName || 'pixel-4a'}'"`}
        </pre>
        <p className="mt-2 text-[10px] leading-relaxed text-zinc-600">
          The script also writes a Magisk <span className="font-mono">service.d</span> hook for boot. The same file is
          served at <span className="font-mono">/download/bootstrap-root.sh</span>. To make the phone a full node
          later, stop the root agent and run the standard Termux bootstrap instead.
        </p>
      </Card>

      <p className="mt-4 text-[10px] leading-relaxed text-zinc-600">
        After the phone appears, set it to battery “Unrestricted” in Android settings and keep Termux's wake lock.
        Install Termux:API for battery metrics. Then create a project and deploy to it.
      </p>
    </div>
  )
}

