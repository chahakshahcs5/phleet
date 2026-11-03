import { useQuery } from '@tanstack/react-query'
import { Cloud, KeyRound, Palette, Server, Shield, Smartphone, TerminalSquare } from 'lucide-react'
import { useState } from 'react'
import { api } from '../api'
import ThemeToggle from '../components/ThemeToggle'
import ThemeStudio from '../components/ThemeStudio'
import TunnelCard from '../components/TunnelCard'
import { Badge, Button, Card, KeyValue } from '../components/ui'

export default function Settings() {
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const [studioOpen, setStudioOpen] = useState(false)
  const base = me.data?.publicUrl || 'https://fleet.example.com'
  const dataDir = me.data?.dataDir || '%ProgramData%\\phleet'

  return (
    <div className="mx-auto max-w-4xl space-y-4 px-4 py-6 md:px-8 md:py-8">
      <div>
        <h1 className="text-xl font-semibold tracking-tight text-zinc-100">Settings</h1>
        <p className="mt-0.5 text-xs text-zinc-500">Control plane, edge and onboarding reference.</p>
      </div>

      <Card className="flex flex-wrap items-center justify-between gap-3 p-4">
        <div>
          <div className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">Appearance</div>
          <p className="mt-1 text-[11px] text-zinc-500">
            Light is the default. Pick a preset or custom accent in the theme studio.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <ThemeToggle />
          <Button size="sm" variant="outline" onClick={() => setStudioOpen(true)}>
            <Palette size={13} /> Theme studio
          </Button>
        </div>
      </Card>

      <Card className="p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Server size={12} /> Control plane
        </div>
        <div className="mt-3 grid gap-x-8 sm:grid-cols-2">
          <div className="divide-y divide-zinc-800/60">
            <KeyValue label="Version">{me.data?.version ?? '—'}</KeyValue>
            <KeyValue label="Data directory">
              <span className="font-mono">{dataDir}</span>
            </KeyValue>
            <KeyValue label="Agents online">{String(me.data?.agentsOnline ?? 0)}</KeyValue>
          </div>
          <div className="divide-y divide-zinc-800/60">
            <KeyValue label="Public URL">{me.data?.publicUrl || <Badge tone="warn">not configured</Badge>}</KeyValue>
            <KeyValue label="Admin token">
              <span className="font-mono">{dataDir}\\admin_token</span>
            </KeyValue>
            <KeyValue label="Agent token">
              <span className="font-mono">{dataDir}\\agent_token</span>
            </KeyValue>
          </div>
        </div>
        <p className="mt-3 text-[10px] leading-relaxed text-zinc-600">
          Tokens are generated once and never leave the host through the API. Rotate by deleting the file and restarting
          phleetd — every agent must be re-bootstrapped afterwards.
        </p>
      </Card>

      <TunnelCard />

      <Card className="p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Cloud size={12} /> Edge and DNS
        </div>
        <div className="mt-3 space-y-2 text-[11px] leading-relaxed text-zinc-400">
          <p>
            phleet routes every project by hostname. One wildcard record and one catch-all tunnel rule cover the whole
            fleet forever — add as many projects as you want without touching Cloudflare again.
          </p>
          <pre className="overflow-x-auto rounded-lg bg-zinc-950 p-3 font-mono text-[10px] text-zinc-400 ring-1 ring-zinc-800">
{`phleet setup-cloudflare -domain example.com

# creates, once:
#   CNAME fleet.example.com   -> <tunnel>.cfargotunnel.com
#   CNAME *.example.com       -> <tunnel>.cfargotunnel.com
#   tunnel ingress: fleet + * -> http://127.0.0.1:8080`}
          </pre>
          <p className="text-zinc-500">
            Project URLs use first-level subdomains (<span className="font-mono">api.example.com</span>) because
            Cloudflare's free Universal SSL covers <span className="font-mono">*.example.com</span> but not{' '}
            <span className="font-mono">*.apps.example.com</span>.
          </p>
        </div>
      </Card>

      <Card className="p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Smartphone size={12} /> Add a phone
        </div>
        <ol className="mt-3 list-decimal space-y-2 pl-4 text-[11px] leading-relaxed text-zinc-400">
          <li>
            Install Termux (F-Droid build) and open it. Optional but recommended: install Termux:API for battery data.
          </li>
          <li>
            Fetch the agent token from <span className="font-mono text-zinc-300">{dataDir}\\agent_token</span>.
          </li>
          <li>
            Run:
            <pre className="mt-2 overflow-x-auto rounded-lg bg-zinc-950 p-3 font-mono text-[10px] text-zinc-300 ring-1 ring-zinc-800">
{`curl -fsSL ${base}/download/bootstrap.sh \\
  | bash -s -- --server ${base} --token <agent-token>`}
            </pre>
          </li>
          <li>
            Hold a Termux wake-lock (<span className="font-mono">termux-wake-lock</span>) and disable battery
            optimisation for Termux, so Android does not freeze the agent.
          </li>
        </ol>
      </Card>

      <Card className="p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Shield size={12} /> Security
        </div>
        <div className="mt-3 space-y-2 text-[11px] leading-relaxed text-zinc-400">
          <p className="flex items-start gap-2">
            <KeyRound size={12} className="mt-0.5 shrink-0 text-zinc-500" />
            The panel handles auth itself, but put Cloudflare Access in front of{' '}
            <span className="font-mono text-zinc-300">fleet.example.com</span> for SSO and MFA, with a path bypass for{' '}
            <span className="font-mono text-zinc-300">/agent</span>.
          </p>
          <p className="flex items-start gap-2">
            <TerminalSquare size={12} className="mt-0.5 shrink-0 text-zinc-500" />
            Phones never accept inbound connections: the agent dials out over TLS, and every terminal or file operation
            travels over that connection.
          </p>
          <p className="flex items-start gap-2">
            <Server size={12} className="mt-0.5 shrink-0 text-zinc-500" />
            One agent token covers the fleet today. Use per-device tokens before sharing the panel with other people.
          </p>
        </div>
      </Card>

      <ThemeStudio open={studioOpen} onClose={() => setStudioOpen(false)} />
    </div>
  )
}
