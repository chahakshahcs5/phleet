import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Server, Shield, TerminalSquare } from 'lucide-react'
import { useState } from 'react'
import { api } from '../../api'
import { Badge, Button, Card } from '../../components/ui'
import { errorClass, fieldClass, sectionLabelClass } from './shared'

export default function SecurityTab() {
  const qc = useQueryClient()
  const [totpSetup, setTotpSetup] = useState<{ secret: string; url: string } | null>(null)
  const [totpCode, setTotpCode] = useState('')

  const totp = useQuery({ queryKey: ['totp'], queryFn: api.totp })
  const setupTOTP = useMutation({
    mutationFn: () => api.setupTOTP(),
    onSuccess: (res) => setTotpSetup(res),
  })
  const enableTOTP = useMutation({
    mutationFn: () => api.enableTOTP(totpCode.trim()),
    onSuccess: () => {
      setTotpSetup(null)
      setTotpCode('')
      qc.invalidateQueries({ queryKey: ['totp'] })
      qc.invalidateQueries({ queryKey: ['meta'] })
    },
  })
  const disableTOTP = useMutation({
    mutationFn: () => api.disableTOTP(),
    onSuccess: () => {
      setTotpSetup(null)
      setTotpCode('')
      qc.invalidateQueries({ queryKey: ['totp'] })
      qc.invalidateQueries({ queryKey: ['meta'] })
    },
  })

  return (
    <Card className="p-4">
      <div className={sectionLabelClass}>
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
          The shared agent token is only an enrollment token; phones receive a per-device token on first connect.
          Scoped API tokens under Access can be revoked at any time.
        </p>
        <div className="border-t border-zinc-800/60 pt-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <div className="flex items-center gap-2 text-[11px] text-zinc-300">
                <KeyRound size={12} className="text-zinc-500" /> Two-factor authentication
                <Badge tone={totp.data?.enabled ? 'good' : 'warn'}>{totp.data?.enabled ? 'enabled' : 'off'}</Badge>
              </div>
              <p className="mt-1 text-[10px] text-zinc-600">
                TOTP is required after your password or the admin token. If you lose the device, run{' '}
                <span className="font-mono">phleet totp -disable</span> on the control-plane host.
              </p>
            </div>
            {totp.data?.enabled ? (
              <Button size="sm" variant="outline" disabled={disableTOTP.isPending} onClick={() => disableTOTP.mutate()}>
                Disable 2FA
              </Button>
            ) : (
              <Button size="sm" variant="outline" disabled={setupTOTP.isPending} onClick={() => setupTOTP.mutate()}>
                Set up 2FA
              </Button>
            )}
          </div>
          {totpSetup ? (
            <div className="mt-3 rounded-lg border border-zinc-800 bg-zinc-950/70 p-3">
              <div className="text-[10px] text-zinc-500">Secret</div>
              <code className="mt-1 block break-all font-mono text-[11px] text-emerald-300">{totpSetup.secret}</code>
              <div className="mt-2 break-all text-[10px] text-zinc-600">{totpSetup.url}</div>
              <div className="mt-3 flex items-end gap-2">
                <label className="flex-1">
                  <span className="text-[10px] text-zinc-500">Code from authenticator</span>
                  <input
                    inputMode="numeric"
                    maxLength={6}
                    value={totpCode}
                    onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))}
                    placeholder="123456"
                    className={`${fieldClass} mt-1 text-center font-mono text-sm tracking-[0.35em]`}
                  />
                </label>
                <Button
                  size="sm"
                  variant="primary"
                  disabled={totpCode.length !== 6 || enableTOTP.isPending}
                  onClick={() => enableTOTP.mutate()}
                >
                  Enable
                </Button>
              </div>
              {enableTOTP.isError ? <div className={`mt-2 ${errorClass}`}>{String(enableTOTP.error)}</div> : null}
            </div>
          ) : null}
          {setupTOTP.isError ? <div className={`mt-2 ${errorClass}`}>{String(setupTOTP.error)}</div> : null}
          {disableTOTP.isError ? <div className={`mt-2 ${errorClass}`}>{String(disableTOTP.error)}</div> : null}
        </div>
      </div>
    </Card>
  )
}
