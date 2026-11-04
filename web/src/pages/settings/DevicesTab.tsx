import { Smartphone } from 'lucide-react'
import { Card } from '../../components/ui'
import { sectionLabelClass } from './shared'

export default function DevicesTab({ base, dataDir }: { base: string; dataDir: string }) {
  return (
    <Card className="p-4">
      <div className={sectionLabelClass}>
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
  )
}
