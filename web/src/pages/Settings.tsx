import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Cloud, KeyRound, Palette, Server, Shield, Smartphone, TerminalSquare, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { api } from '../api'
import type { APIToken, User } from '../types'
import ThemeToggle from '../components/ThemeToggle'
import ThemeStudio from '../components/ThemeStudio'
import TunnelCard from '../components/TunnelCard'
import { Badge, Button, Card, KeyValue } from '../components/ui'

export default function Settings() {
  const me = useQuery({ queryKey: ['me'], queryFn: api.me })
  const qc = useQueryClient()
  const [studioOpen, setStudioOpen] = useState(false)
  const [tokenName, setTokenName] = useState('')
  const [tokenRole, setTokenRole] = useState<'admin' | 'deployer' | 'viewer'>('viewer')
  const [createdToken, setCreatedToken] = useState('')
  const [totpSetup, setTotpSetup] = useState<{ secret: string; url: string } | null>(null)
  const [totpCode, setTotpCode] = useState('')
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState<'admin' | 'deployer' | 'viewer'>('viewer')
  const [resetUser, setResetUser] = useState<User | null>(null)
  const [resetPassword, setResetPassword] = useState('')
  const base = me.data?.publicUrl || 'https://fleet.example.com'
  const dataDir = me.data?.dataDir || '%ProgramData%\\phleet'

  const tokens = useQuery({ queryKey: ['api-tokens'], queryFn: api.apiTokens })
  const createToken = useMutation({
    mutationFn: () => api.createAPIToken({ name: tokenName.trim(), role: tokenRole }),
    onSuccess: (res) => {
      setCreatedToken(res.token)
      setTokenName('')
      qc.invalidateQueries({ queryKey: ['api-tokens'] })
    },
  })
  const deleteToken = useMutation({
    mutationFn: (id: string) => api.deleteAPIToken(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-tokens'] }),
  })
  const tokenList: APIToken[] = tokens.data?.tokens ?? []

  const users = useQuery({ queryKey: ['users'], queryFn: api.users })
  const createUser = useMutation({
    mutationFn: () =>
      api.createUser({ username: newUsername.trim(), password: newPassword, role: newRole }),
    onSuccess: () => {
      setNewUsername('')
      setNewPassword('')
      qc.invalidateQueries({ queryKey: ['users'] })
    },
  })
  const updateUser = useMutation({
    mutationFn: (args: { id: string; role?: 'admin' | 'deployer' | 'viewer'; disabled?: boolean }) =>
      api.updateUser(args.id, { role: args.role, disabled: args.disabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
  const deleteUser = useMutation({
    mutationFn: (id: string) => api.deleteUser(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
  const resetUserPassword = useMutation({
    mutationFn: () => (resetUser ? api.resetUserPassword(resetUser.id, resetPassword) : Promise.resolve({ ok: false })),
    onSuccess: () => {
      setResetUser(null)
      setResetPassword('')
    },
  })
  const userList: User[] = users.data?.users ?? []

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

      <Card className="p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <Shield size={12} /> Users
        </div>
        <p className="mt-2 text-[11px] leading-relaxed text-zinc-500">
          Admins can mutate everything. Viewers are read-only. Each account can enable its own TOTP under Security.
        </p>
        <div className="mt-3 flex flex-wrap items-end gap-2">
          <label className="flex-1">
            <span className="text-[10px] text-zinc-500">Username</span>
            <input
              className="mt-1 h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none focus:border-emerald-400/40"
              value={newUsername}
              onChange={(e) => setNewUsername(e.target.value)}
              placeholder="alex"
            />
          </label>
          <label className="flex-1">
            <span className="text-[10px] text-zinc-500">Password</span>
            <input
              type="password"
              className="mt-1 h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none focus:border-emerald-400/40"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="at least 8 characters"
            />
          </label>
          <label>
            <span className="text-[10px] text-zinc-500">Role</span>
            <select
              className="mt-1 h-9 rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none"
              value={newRole}
              onChange={(e) => setNewRole(e.target.value as 'admin' | 'deployer' | 'viewer')}
            >
              <option value="viewer">viewer</option>
              <option value="deployer">deployer</option>
              <option value="admin">admin</option>
            </select>
          </label>
          <Button
            size="sm"
            variant="primary"
            disabled={!newUsername.trim() || newPassword.length < 8 || createUser.isPending}
            onClick={() => createUser.mutate()}
          >
            Add user
          </Button>
        </div>
        {createUser.isError ? <div className="mt-2 text-[11px] text-red-400">{String(createUser.error)}</div> : null}
        <div className="mt-3 divide-y divide-zinc-800/60">
          {userList.map((u) => (
            <div key={u.id} className="flex flex-wrap items-center justify-between gap-2 py-2">
              <div className="flex items-center gap-2 text-[11px] text-zinc-300">
                <span className="font-medium">{u.username}</span>
                <Badge tone={u.role === 'admin' ? 'warn' : u.role === 'deployer' ? 'info' : 'neutral'}>
                  {u.role}
                </Badge>
                {u.disabled ? <Badge tone="bad">disabled</Badge> : null}
              </div>
              <div className="flex items-center gap-1">
                <select
                  className="h-8 rounded-md border border-zinc-800 bg-zinc-950 px-2 text-[10px] text-zinc-300 outline-none"
                  value={u.role}
                  onChange={(e) =>
                    updateUser.mutate({
                      id: u.id,
                      role: e.target.value as 'admin' | 'deployer' | 'viewer',
                    })
                  }
                >
                  <option value="viewer">viewer</option>
                  <option value="deployer">deployer</option>
                  <option value="admin">admin</option>
                </select>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => updateUser.mutate({ id: u.id, disabled: !u.disabled })}
                >
                  {u.disabled ? 'Enable' : 'Disable'}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setResetUser(u)}>
                  Password
                </Button>
                <Button size="icon" variant="ghost" title="Delete user" onClick={() => deleteUser.mutate(u.id)}>
                  <Trash2 size={12} />
                </Button>
              </div>
            </div>
          ))}
          {userList.length === 0 ? <div className="py-3 text-[11px] text-zinc-600">No users yet.</div> : null}
        </div>
        {resetUser ? (
          <div className="mt-3 rounded-lg border border-zinc-800 bg-zinc-950/70 p-3">
            <div className="text-[10px] text-zinc-500">New password for {resetUser.username}</div>
            <div className="mt-2 flex gap-2">
              <input
                type="password"
                value={resetPassword}
                onChange={(e) => setResetPassword(e.target.value)}
                placeholder="at least 8 characters"
                className="h-9 flex-1 rounded-lg border border-zinc-800 bg-zinc-950 px-3 text-xs text-zinc-200 outline-none"
              />
              <Button
                size="sm"
                variant="primary"
                disabled={resetPassword.length < 8 || resetUserPassword.isPending}
                onClick={() => resetUserPassword.mutate()}
              >
                Save
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setResetUser(null)}>
                Cancel
              </Button>
            </div>
            {resetUserPassword.isError ? (
              <div className="mt-2 text-[11px] text-red-400">{String(resetUserPassword.error)}</div>
            ) : null}
          </div>
        ) : null}
        {deleteUser.isError ? <div className="mt-2 text-[11px] text-red-400">{String(deleteUser.error)}</div> : null}
      </Card>

      <Card className="p-4">
        <div className="flex items-center gap-2 text-[10px] font-medium tracking-widest text-zinc-500 uppercase">
          <KeyRound size={12} /> API tokens
        </div>
        <p className="mt-2 text-[11px] leading-relaxed text-zinc-500">
          Viewer tokens can read fleet, project and alert data but cannot mutate anything or read credentials,
          source archives, backups or file contents. Admin tokens have the same access as the admin token.
        </p>
        <div className="mt-3 flex flex-wrap items-end gap-2">
          <label className="flex-1">
            <span className="text-[10px] text-zinc-500">Name</span>
            <input
              className="mt-1 h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none focus:border-emerald-400/40"
              value={tokenName}
              onChange={(e) => setTokenName(e.target.value)}
              placeholder="monitoring"
            />
          </label>
          <label>
            <span className="text-[10px] text-zinc-500">Role</span>
            <select
              className="mt-1 h-9 rounded-lg border border-zinc-800 bg-zinc-950/70 px-3 text-xs text-zinc-200 outline-none"
              value={tokenRole}
              onChange={(e) => setTokenRole(e.target.value as 'admin' | 'deployer' | 'viewer')}
            >
              <option value="viewer">viewer</option>
              <option value="deployer">deployer</option>
              <option value="admin">admin</option>
            </select>
          </label>
          <Button
            size="sm"
            variant="primary"
            disabled={!tokenName.trim() || createToken.isPending}
            onClick={() => {
              setCreatedToken('')
              createToken.mutate()
            }}
          >
            Create token
          </Button>
        </div>
        {createToken.isError ? (
          <div className="mt-2 text-[11px] text-red-400">{String(createToken.error)}</div>
        ) : null}
        {createdToken ? (
          <div className="mt-3 rounded-lg border border-amber-900/40 bg-amber-500/5 p-3">
            <div className="text-[10px] text-amber-300">Copy this token now; it is shown once.</div>
            <code className="mt-1 block break-all font-mono text-[10px] text-amber-200">{createdToken}</code>
            <Button
              size="sm"
              variant="ghost"
              className="mt-2"
              onClick={() => navigator.clipboard?.writeText(createdToken)}
            >
              Copy
            </Button>
          </div>
        ) : null}
        <div className="mt-3 divide-y divide-zinc-800/60">
          {tokenList.map((t) => (
            <div key={t.id} className="flex items-center justify-between gap-3 py-2">
              <div className="min-w-0">
                <div className="flex items-center gap-2 text-[11px] text-zinc-300">
                  <span className="truncate">{t.name}</span>
                  <Badge tone={t.role === 'admin' ? 'warn' : t.role === 'deployer' ? 'info' : 'neutral'}>
                    {t.role}
                  </Badge>
                </div>
                <div className="mt-0.5 text-[10px] text-zinc-600">
                  created {new Date(t.createdAt * 1000).toLocaleDateString()}
                  {t.lastUsedAt ? ` · last used ${new Date(t.lastUsedAt * 1000).toLocaleDateString()}` : ' · never used'}
                </div>
              </div>
              <Button
                size="icon"
                variant="ghost"
                title="Revoke token"
                disabled={deleteToken.isPending}
                onClick={() => deleteToken.mutate(t.id)}
              >
                <Trash2 size={12} />
              </Button>
            </div>
          ))}
          {tokenList.length === 0 ? (
            <div className="py-3 text-[11px] text-zinc-600">No scoped tokens yet.</div>
          ) : null}
        </div>
        {deleteToken.isError ? (
          <div className="mt-2 text-[11px] text-red-400">{String(deleteToken.error)}</div>
        ) : null}
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
            The shared agent token is only an enrollment token; phones receive a per-device token on first connect.
            Scoped API tokens above can be revoked at any time.
          </p>
          <div className="border-t border-zinc-800/60 pt-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <div className="flex items-center gap-2 text-[11px] text-zinc-300">
                  <KeyRound size={12} className="text-zinc-500" /> Admin two-factor authentication
                  <Badge tone={totp.data?.enabled ? 'good' : 'warn'}>
                    {totp.data?.enabled ? 'enabled' : 'off'}
                  </Badge>
                </div>
                <p className="mt-1 text-[10px] text-zinc-600">
                  TOTP is required after the admin token. If you lose the device, run{' '}
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
                      className="mt-1 h-9 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 text-center font-mono text-sm tracking-[0.35em] text-zinc-200 outline-none focus:border-emerald-400/40"
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
                {enableTOTP.isError ? (
                  <div className="mt-2 text-[11px] text-red-400">{String(enableTOTP.error)}</div>
                ) : null}
              </div>
            ) : null}
            {setupTOTP.isError ? (
              <div className="mt-2 text-[11px] text-red-400">{String(setupTOTP.error)}</div>
            ) : null}
            {disableTOTP.isError ? (
              <div className="mt-2 text-[11px] text-red-400">{String(disableTOTP.error)}</div>
            ) : null}
          </div>
        </div>
      </Card>

      <ThemeStudio open={studioOpen} onClose={() => setStudioOpen(false)} />
    </div>
  )
}
