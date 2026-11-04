import { Palette, Server } from 'lucide-react'
import { useState } from 'react'
import type { Me } from '../../types'
import ThemeStudio from '../../components/ThemeStudio'
import ThemeToggle from '../../components/ThemeToggle'
import { Badge, Button, Card, KeyValue } from '../../components/ui'
import { sectionLabelClass } from './shared'

export default function GeneralTab({ me }: { me?: Me }) {
  const [studioOpen, setStudioOpen] = useState(false)
  const dataDir = me?.dataDir || '%ProgramData%\\phleet'
  const publicUrl = me?.publicUrl || ''

  return (
    <div className="space-y-4">
      <Card className="flex flex-wrap items-center justify-between gap-3 p-4">
        <div>
          <div className={sectionLabelClass}>Appearance</div>
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
        <div className={sectionLabelClass}>
          <Server size={12} /> Control plane
        </div>
        <div className="mt-3 grid gap-x-8 sm:grid-cols-2">
          <div className="divide-y divide-zinc-800/60">
            <KeyValue label="Version">{me?.version ?? '—'}</KeyValue>
            <KeyValue label="Data directory">
              <span className="font-mono">{dataDir}</span>
            </KeyValue>
            <KeyValue label="Agents online">{String(me?.agentsOnline ?? 0)}</KeyValue>
          </div>
          <div className="divide-y divide-zinc-800/60">
            <KeyValue label="Public URL">
              {publicUrl || <Badge tone="warn">not configured</Badge>}
            </KeyValue>
            <KeyValue label="Admin token">
              <span className="font-mono">{dataDir}\\admin_token</span>
            </KeyValue>
            <KeyValue label="Agent token">
              <span className="font-mono">{dataDir}\\agent_token</span>
            </KeyValue>
          </div>
        </div>
        <p className="mt-3 text-[10px] leading-relaxed text-zinc-600">
          Tokens are generated once. Run <span className="font-mono">phleet token -rotate</span> to write a new
          token file, then restart phleetd. Rotating the agent token does not affect phones that are already
          enrolled (they use per-device tokens); new phones need the new token.
        </p>
      </Card>

      <ThemeStudio open={studioOpen} onClose={() => setStudioOpen(false)} />
    </div>
  )
}
