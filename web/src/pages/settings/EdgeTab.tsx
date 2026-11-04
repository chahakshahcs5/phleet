import { Cloud } from 'lucide-react'
import TunnelCard from '../../components/TunnelCard'
import { Card } from '../../components/ui'
import { sectionLabelClass } from './shared'

export default function EdgeTab() {
  return (
    <div className="space-y-4">
      <TunnelCard />

      <Card className="p-4">
        <div className={sectionLabelClass}>
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
    </div>
  )
}
