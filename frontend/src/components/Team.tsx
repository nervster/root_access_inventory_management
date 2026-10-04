import { useState, type FormEvent } from 'react'
import { ROLE_LABELS, type InvitationDto, type MemberDto, type OrgRole } from '../api/types'
import { formatDate } from '../lib/format'
import { Badge, Button, ErrorBanner, Field, Input, Notice, Select } from './ui'

/** Who may do what in a team list. Nursery members and platform admins get different rules. */
export interface TeamRules {
  canChangeRole: (member: MemberDto) => boolean
  canRemove: (member: MemberDto) => boolean
  canManageInvitation: (invitation: InvitationDto) => boolean
  /** Roles offered when inviting or changing a role. */
  roles: OrgRole[]
  /** The signed-in user's ID, so their own row shows "Leave" instead of "Remove". */
  selfUserId?: number
}

interface Mutation<TArgs> {
  mutate: (args: TArgs, options?: { onSuccess?: () => void }) => void
  isPending: boolean
  error: unknown
}

export function MemberList({
  members,
  rules,
  changeRole,
  remove,
}: {
  members: MemberDto[]
  rules: TeamRules
  changeRole: Mutation<{ id: number; role: OrgRole }>
  remove: Mutation<number>
}) {
  return (
    <div className="space-y-3">
      <ErrorBanner error={changeRole.error ?? remove.error} />
      <ul className="divide-y divide-stone-200 dark:divide-stone-800">
        {members.map((member) => {
          const isSelf = member.userId === rules.selfUserId
          return (
            <li key={member.id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <p className="truncate font-medium">
                  {member.displayName ?? member.email} {isSelf && <span className="text-sm font-normal text-stone-500">(you)</span>}
                </p>
                <p className="truncate text-sm text-stone-500">
                  {member.displayName && `${member.email} · `}joined {formatDate(member.joinedAt)}
                </p>
              </div>
              <div className="flex items-center gap-2">
                {rules.canChangeRole(member) ? (
                  <div className="w-32">
                    <Select
                      aria-label={`Role for ${member.email}`}
                      value={member.role}
                      disabled={changeRole.isPending}
                      onChange={(e) => changeRole.mutate({ id: member.id, role: e.target.value as OrgRole })}
                    >
                      {rules.roles.map((role) => (
                        <option key={role} value={role}>
                          {ROLE_LABELS[role]}
                        </option>
                      ))}
                    </Select>
                  </div>
                ) : (
                  <Badge tone={member.role === 'owner' ? 'green' : 'neutral'}>{ROLE_LABELS[member.role]}</Badge>
                )}
                {(isSelf || rules.canRemove(member)) && (
                  <Button
                    variant="danger"
                    disabled={remove.isPending}
                    onClick={() => {
                      const prompt = isSelf ? 'Leave this nursery?' : `Remove ${member.email}?`
                      if (window.confirm(prompt)) remove.mutate(member.id)
                    }}
                  >
                    {isSelf ? 'Leave' : 'Remove'}
                  </Button>
                )}
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export function InvitationList({
  invitations,
  rules,
  resend,
  revoke,
}: {
  invitations: InvitationDto[]
  rules: TeamRules
  resend: Mutation<number>
  revoke: Mutation<number>
}) {
  if (invitations.length === 0) return <p className="text-sm text-stone-500">No pending invitations.</p>
  return (
    <div className="space-y-3">
      <ErrorBanner error={resend.error ?? revoke.error} />
      <ul className="divide-y divide-stone-200 dark:divide-stone-800">
        {invitations.map((invitation) => (
          <li key={invitation.id} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <p className="truncate font-medium">{invitation.email}</p>
              <p className="flex flex-wrap items-center gap-2 text-sm text-stone-500">
                <Badge>{ROLE_LABELS[invitation.role]}</Badge>
                {invitation.expired ? <Badge tone="red">Expired</Badge> : <span>expires {formatDate(invitation.expiresAt)}</span>}
              </p>
            </div>
            {rules.canManageInvitation(invitation) && (
              <div className="flex gap-2">
                <Button variant="secondary" disabled={resend.isPending} onClick={() => resend.mutate(invitation.id)}>
                  Resend
                </Button>
                <Button
                  variant="danger"
                  disabled={revoke.isPending}
                  onClick={() => window.confirm(`Revoke the invitation for ${invitation.email}?`) && revoke.mutate(invitation.id)}
                >
                  Revoke
                </Button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function InviteForm({ roles, invite }: { roles: OrgRole[]; invite: Mutation<{ email: string; role: OrgRole }> }) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<OrgRole>(roles.includes('staff') ? 'staff' : roles[0])
  const [sentTo, setSentTo] = useState<string | null>(null)

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setSentTo(null)
    invite.mutate(
      { email, role },
      {
        onSuccess: () => {
          setSentTo(email)
          setEmail('')
        },
      },
    )
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-[1fr_10rem]">
        <Field label="Email">
          <Input type="email" required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="name@example.com" />
        </Field>
        <Field label="Role">
          <Select value={role} onChange={(e) => setRole(e.target.value as OrgRole)}>
            {roles.map((r) => (
              <option key={r} value={r}>
                {ROLE_LABELS[r]}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <ErrorBanner error={invite.error} />
      {sentTo && <Notice>Invitation sent to {sentTo}.</Notice>}
      <Button type="submit" disabled={invite.isPending}>
        {invite.isPending ? 'Sending…' : 'Send invitation'}
      </Button>
    </form>
  )
}
