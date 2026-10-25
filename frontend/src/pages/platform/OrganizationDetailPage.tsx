import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { queryKeys, usePlatformOrganization, useTeamActions, useUpdatePlatformOrganization } from '../../api/hooks'
import { ORG_ROLES, STATUS_LABELS } from '../../api/types'
import { InvitationList, InviteForm, MemberList, type TeamRules } from '../../components/Team'
import { Badge, Button, Card, ErrorBanner, Input, Loading, Notice, PageHeader } from '../../components/ui'
import { formatDate } from '../../lib/format'

/** Platform admins can fix any membership problem, so every action is allowed (the API still guards the last Owner). */
const platformRules: TeamRules = {
  canChangeRole: () => true,
  canRemove: () => true,
  canManageInvitation: () => true,
  roles: ORG_ROLES,
}

export function OrganizationDetailPage() {
  const id = Number(useParams().id)
  const detail = usePlatformOrganization(id)
  const update = useUpdatePlatformOrganization(id)
  const actions = useTeamActions(`/platform/organizations/${id}`, [queryKeys.platformOrganization(id), queryKeys.platformOrganizations])
  const [renaming, setRenaming] = useState<string | null>(null)

  if (detail.isPending) return <Loading />
  if (detail.error) return <ErrorBanner error={detail.error} />

  const { organization: org, members, pendingInvitations } = detail.data
  const suspended = org.status === 'suspended'

  return (
    <div className="space-y-4">
      <Link to="/platform" className="text-sm text-stone-500 hover:underline">
        ← All nurseries
      </Link>
      <PageHeader
        title={org.name}
        subtitle={
          <span className="flex flex-wrap items-center gap-2">
            {org.slug} · {org.timeZone} · created {formatDate(org.createdAt)}
            <Badge tone={suspended ? 'red' : 'green'}>{STATUS_LABELS[org.status]}</Badge>
          </span>
        }
        actions={
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setRenaming(org.name)}>
              Rename
            </Button>
            <Button
              variant={suspended ? 'primary' : 'danger'}
              disabled={update.isPending}
              onClick={() => {
                const prompt = suspended ? `Reactivate ${org.name}?` : `Suspend ${org.name}? Its members will lose access until it's reactivated.`
                if (window.confirm(prompt)) update.mutate({ status: suspended ? 'active' : 'suspended' })
              }}
            >
              {suspended ? 'Reactivate' : 'Suspend'}
            </Button>
          </div>
        }
      />
      <Notice>You can see this nursery's account (members and invitations), not its plants, costs, or sales.</Notice>
      <ErrorBanner error={update.error} />

      {renaming !== null && (
        <Card title="Rename">
          <form
            className="flex flex-col gap-2 sm:flex-row"
            onSubmit={(e) => {
              e.preventDefault()
              update.mutate({ name: renaming }, { onSuccess: () => setRenaming(null) })
            }}
          >
            <Input required maxLength={200} value={renaming} onChange={(e) => setRenaming(e.target.value)} aria-label="New name" />
            <div className="flex gap-2">
              <Button type="submit" disabled={update.isPending}>
                Save
              </Button>
              <Button type="button" variant="ghost" onClick={() => setRenaming(null)}>
                Cancel
              </Button>
            </div>
          </form>
        </Card>
      )}

      <Card title={`Members (${members.length})`}>
        {members.length === 0 ? (
          <p className="text-sm text-stone-500">No members yet. The Owner's invitation is below.</p>
        ) : (
          <MemberList members={members} rules={platformRules} changeRole={actions.changeRole} remove={actions.remove} />
        )}
      </Card>
      <Card title="Invite someone" >
        <InviteForm roles={ORG_ROLES} invite={actions.invite} />
      </Card>
      <Card title="Pending invitations">
        <InvitationList invitations={pendingInvitations} rules={platformRules} resend={actions.resend} revoke={actions.revoke} />
      </Card>
    </div>
  )
}
