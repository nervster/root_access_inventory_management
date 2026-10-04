import { useParams } from 'react-router'
import { queryKeys, useInvitations, useMe, useMembers, useMembership, useTeamActions } from '../../api/hooks'
import { can, canManageRole, manageableRoles } from '../../api/roles'
import { ORG_ROLES, Permissions } from '../../api/types'
import { InvitationList, InviteForm, MemberList, type TeamRules } from '../../components/Team'
import { Card, ErrorBanner, Loading, PageHeader } from '../../components/ui'

export function TeamPage() {
  const orgId = Number(useParams().orgId)
  const me = useMe()
  const membership = useMembership(orgId)
  const canInvite = can(membership, Permissions.TeamManage)
  const members = useMembers(orgId)
  const invitations = useInvitations(orgId, canInvite)
  const actions = useTeamActions(`/orgs/${orgId}`, [queryKeys.members(orgId), queryKeys.invitations(orgId), queryKeys.me])

  if (!membership || members.isPending) return <Loading />
  if (members.error) return <ErrorBanner error={members.error} />

  const myRole = membership.role
  const rules: TeamRules = {
    canChangeRole: () => can(membership, Permissions.RolesManage),
    canRemove: (m) => canInvite && canManageRole(myRole, m.role),
    canManageInvitation: (i) => canManageRole(myRole, i.role),
    roles: can(membership, Permissions.RolesManage) ? ORG_ROLES : manageableRoles(myRole),
    selfUserId: me.data?.id,
  }

  return (
    <div className="space-y-4">
      <PageHeader title="Team" subtitle={`${members.data.length} member${members.data.length === 1 ? '' : 's'}`} />
      <Card title="Members">
        <MemberList members={members.data} rules={rules} changeRole={actions.changeRole} remove={actions.remove} />
      </Card>
      {canInvite && (
        <>
          <Card title="Invite someone">
            <InviteForm roles={manageableRoles(myRole)} invite={actions.invite} />
          </Card>
          <Card title="Pending invitations">
            {invitations.isPending ? (
              <Loading />
            ) : (
              <InvitationList invitations={invitations.data ?? []} rules={rules} resend={actions.resend} revoke={actions.revoke} />
            )}
          </Card>
        </>
      )}
    </div>
  )
}
