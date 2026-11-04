import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Shield, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { api } from '../../api'
import type { APIToken, User } from '../../types'
import { Badge, Button, Card } from '../../components/ui'
import { errorClass, fieldClass, sectionLabelClass, selectClass } from './shared'

type Role = 'admin' | 'deployer' | 'viewer'

export default function AccessTab() {
  const qc = useQueryClient()
  const [tokenName, setTokenName] = useState('')
  const [tokenRole, setTokenRole] = useState<Role>('viewer')
  const [createdToken, setCreatedToken] = useState('')
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState<Role>('viewer')
  const [resetUser, setResetUser] = useState<User | null>(null)
  const [resetPassword, setResetPassword] = useState('')

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
    mutationFn: () => api.createUser({ username: newUsername.trim(), password: newPassword, role: newRole }),
    onSuccess: () => {
      setNewUsername('')
      setNewPassword('')
      qc.invalidateQueries({ queryKey: ['users'] })
    },
  })
  const updateUser = useMutation({
    mutationFn: (args: { id: string; role?: Role; disabled?: boolean }) =>
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

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className={sectionLabelClass}>
          <Shield size={12} /> Users
        </div>
        <p className="mt-2 text-[11px] leading-relaxed text-zinc-500">
          Admins can mutate everything. Deployers can drive deploys. Viewers are read-only. Each account can enable
          its own TOTP under Security.
        </p>
        <div className="mt-3 flex flex-wrap items-end gap-2">
          <label className="flex-1">
            <span className="text-[10px] text-zinc-500">Username</span>
            <input
              className={`${fieldClass} mt-1`}
              value={newUsername}
              onChange={(e) => setNewUsername(e.target.value)}
              placeholder="alex"
            />
          </label>
          <label className="flex-1">
            <span className="text-[10px] text-zinc-500">Password</span>
            <input
              type="password"
              className={`${fieldClass} mt-1`}
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="at least 8 characters"
            />
          </label>
          <label>
            <span className="text-[10px] text-zinc-500">Role</span>
            <select
              className={`${selectClass} mt-1`}
              value={newRole}
              onChange={(e) => setNewRole(e.target.value as Role)}
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
        {createUser.isError ? <div className={`mt-2 ${errorClass}`}>{String(createUser.error)}</div> : null}
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
                  onChange={(e) => updateUser.mutate({ id: u.id, role: e.target.value as Role })}
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
              <div className={`mt-2 ${errorClass}`}>{String(resetUserPassword.error)}</div>
            ) : null}
          </div>
        ) : null}
        {deleteUser.isError ? <div className={`mt-2 ${errorClass}`}>{String(deleteUser.error)}</div> : null}
      </Card>

      <Card className="p-4">
        <div className={sectionLabelClass}>
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
              className={`${fieldClass} mt-1`}
              value={tokenName}
              onChange={(e) => setTokenName(e.target.value)}
              placeholder="monitoring"
            />
          </label>
          <label>
            <span className="text-[10px] text-zinc-500">Role</span>
            <select
              className={`${selectClass} mt-1`}
              value={tokenRole}
              onChange={(e) => setTokenRole(e.target.value as Role)}
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
        {createToken.isError ? <div className={`mt-2 ${errorClass}`}>{String(createToken.error)}</div> : null}
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
        {deleteToken.isError ? <div className={`mt-2 ${errorClass}`}>{String(deleteToken.error)}</div> : null}
      </Card>
    </div>
  )
}
