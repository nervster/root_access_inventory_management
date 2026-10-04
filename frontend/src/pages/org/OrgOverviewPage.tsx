import { Link, useParams } from 'react-router'
import { useMembership, useOrganization } from '../../api/hooks'
import { ROLE_LABELS } from '../../api/types'
import { Badge, Card, ErrorBanner, Loading, PageHeader } from '../../components/ui'

export function OrgOverviewPage() {
  const orgId = Number(useParams().orgId)
  const org = useOrganization(orgId)
  const membership = useMembership(orgId)

  if (org.isPending) return <Loading />
  if (org.error) return <ErrorBanner error={org.error} />

  return (
    <div className="space-y-4">
      <PageHeader title={org.data.name} subtitle={<>You're {membership && <Badge tone="green">{ROLE_LABELS[membership.role]}</Badge>} here</>} />
      <Card title="Inventory">
        <p className="text-sm text-stone-600 dark:text-stone-400">
          Plants, photos, and sales arrive in the next phase. For now you can manage your{' '}
          <Link to={`/orgs/${orgId}/team`} className="font-medium text-emerald-800 underline dark:text-emerald-400">
            team
          </Link>
          .
        </p>
      </Card>
    </div>
  )
}
