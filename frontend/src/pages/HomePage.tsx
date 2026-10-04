import { Link, Navigate, useNavigate } from 'react-router'
import { useMe, useRespondToInvitation } from '../api/hooks'
import { ROLE_LABELS } from '../api/types'
import { Badge, Button, Card, ErrorBanner, Loading, PageHeader } from '../components/ui'
import { formatDate } from '../lib/format'

/** Landing page after sign-in: pending invitations, then the user's nurseries. */
export function HomePage() {
  const me = useMe()
  const respond = useRespondToInvitation()
  const navigate = useNavigate()

  if (me.isPending) return <Loading />
  if (!me.data) return null
  const { memberships, pendingInvitations, isPlatformAdmin, email } = me.data
  const active = memberships.filter((m) => m.status === 'active')

  // One nursery and nothing to decide: go straight there.
  if (pendingInvitations.length === 0 && active.length === 1 && memberships.length === 1)
    return <Navigate to={`/orgs/${active[0].organizationId}`} replace />

  return (
    <div className="space-y-4">
      <PageHeader title="Welcome" subtitle={`Signed in as ${email}`} />

      {pendingInvitations.length > 0 && (
        <Card title="Invitations">
          <ErrorBanner error={respond.error} />
          <ul className="divide-y divide-stone-200 dark:divide-stone-800">
            {pendingInvitations.map((invitation) => (
              <li key={invitation.id} className="flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div>
                  <p className="font-medium">{invitation.organizationName}</p>
                  <p className="text-sm text-stone-500">
                    Join as <Badge>{ROLE_LABELS[invitation.role]}</Badge> · expires {formatDate(invitation.expiresAt)}
                  </p>
                </div>
                <div className="flex gap-2">
                  <Button
                    disabled={respond.isPending}
                    onClick={() =>
                      respond.mutate(
                        { id: invitation.id, accept: true },
                        { onSuccess: () => navigate(`/orgs/${invitation.organizationId}`) },
                      )
                    }
                  >
                    Accept
                  </Button>
                  <Button variant="secondary" disabled={respond.isPending} onClick={() => respond.mutate({ id: invitation.id, accept: false })}>
                    Decline
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {memberships.length > 0 && (
        <Card title="Your nurseries">
          <ul className="divide-y divide-stone-200 dark:divide-stone-800">
            {memberships.map((m) => (
              <li key={m.organizationId} className="flex items-center justify-between gap-3 py-3">
                {m.status === 'active' ? (
                  <Link to={`/orgs/${m.organizationId}`} className="font-medium text-emerald-800 hover:underline dark:text-emerald-400">
                    {m.organizationName}
                  </Link>
                ) : (
                  <span className="font-medium text-stone-500">{m.organizationName}</span>
                )}
                <span className="flex gap-2">
                  {m.status === 'suspended' && <Badge tone="red">Suspended</Badge>}
                  <Badge tone={m.role === 'owner' ? 'green' : 'neutral'}>{ROLE_LABELS[m.role]}</Badge>
                </span>
              </li>
            ))}
          </ul>
        </Card>
      )}

      {memberships.length === 0 && pendingInvitations.length === 0 && (
        <Card>
          {isPlatformAdmin ? (
            <p className="text-sm">
              You're not a member of any nursery. As a platform admin you can{' '}
              <Link to="/platform" className="font-medium text-emerald-800 underline dark:text-emerald-400">
                create one
              </Link>
              .
            </p>
          ) : (
            <p className="text-sm">
              You're not part of a nursery yet. Ask your nursery's owner to invite <strong>{email}</strong>.
            </p>
          )}
        </Card>
      )}
    </div>
  )
}
