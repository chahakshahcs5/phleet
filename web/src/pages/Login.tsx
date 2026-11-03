import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, KeyRound, Smartphone } from 'lucide-react'
import { useState } from 'react'
import { api } from '../api'
import ThemeToggle from '../components/ThemeToggle'
import { Button, Spinner } from '../components/ui'

export default function Login() {
  const [token, setToken] = useState('')
  const [code, setCode] = useState('')
  const qc = useQueryClient()
  const meta = useQuery({ queryKey: ['meta'], queryFn: api.meta, retry: false })
  const totpRequired = !!meta.data?.totpRequired

  const login = useMutation({
    mutationFn: () => api.login(token.trim(), code.trim()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['me'] })
    },
  })

  return (
    <div className="dotted relative grid min-h-full place-items-center bg-zinc-950 px-5 py-16">
      <div className="absolute top-4 right-4">
        <ThemeToggle />
      </div>
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <div className="grid h-12 w-12 place-items-center rounded-2xl bg-emerald-400/10 ring-1 ring-emerald-400/25">
            <Smartphone className="h-5 w-5 text-emerald-400" />
          </div>
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-zinc-100">phleet</h1>
            <p className="mt-0.5 text-xs text-zinc-500">
              your phones, as a platform{meta.data?.publicUrl ? ` · ${meta.data.publicUrl.replace(/^https?:\/\//, '')}` : ''}
            </p>
          </div>
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (token.trim()) login.mutate()
          }}
          className="rounded-xl border border-zinc-800/80 bg-zinc-900/50 p-5 shadow-2xl shadow-black/40"
        >
          <label htmlFor="token" className="text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
            Admin token
          </label>
          <div className="relative mt-2">
            <KeyRound size={14} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-zinc-600" />
            <input
              id="token"
              type="password"
              autoFocus
              autoComplete="current-password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="paste admin_token"
              className="h-10 w-full rounded-lg border border-zinc-800 bg-zinc-950/80 pr-3 pl-9 font-mono text-xs text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/50"
            />
          </div>
          {totpRequired ? (
            <>
              <label htmlFor="code" className="mt-3 block text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
                Two-factor code
              </label>
              <input
                id="code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                placeholder="123456"
                className="mt-2 h-10 w-full rounded-lg border border-zinc-800 bg-zinc-950/80 px-3 text-center font-mono text-sm tracking-[0.4em] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-emerald-400/50"
              />
            </>
          ) : null}
          {login.isError ? (
            <p className="mt-2 text-[11px] text-red-400">{String((login.error as Error).message)}</p>
          ) : null}
          <Button
            type="submit"
            variant="primary"
            className="mt-4 w-full"
            disabled={login.isPending || token.trim() === '' || (totpRequired && code.length !== 6)}
          >
            {login.isPending ? <Spinner className="h-4 w-4 border-zinc-900/40 border-t-zinc-900" /> : null}
            Enter <ArrowRight size={14} />
          </Button>
        </form>

        <p className="mt-4 text-center text-[10px] leading-relaxed text-zinc-600">
          The token lives on the control plane at{' '}
          <span className="font-mono text-zinc-500">%ProgramData%\phleet\admin_token</span>
          <br />
          Rotate it with <span className="font-mono text-zinc-500">phleet token</span>.
        </p>
      </div>
    </div>
  )
}
