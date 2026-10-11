import { Link, useParams } from 'react-router'
import { useBatches, useMembership } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type BatchDto } from '../../api/types'
import { Card, ErrorBanner, LinkButton, Loading, PageHeader } from '../../components/ui'
import { formatDay } from '../../lib/format'

/** Every batch the nursery has received, newest first. */
export function BatchesPage() {
  const orgId = Number(useParams().orgId)
  const membership = useMembership(orgId)
  const batches = useBatches(orgId)

  return (
    <div className="space-y-4">
      <PageHeader
        title="Batches"
        subtitle="Plants that arrived together, e.g. a tissue-culture order."
        actions={
          can(membership, Permissions.InventoryEdit) && (
            <LinkButton to={`/orgs/${orgId}/batches/new`}>+ Receive batch</LinkButton>
          )
        }
      />
      {batches.isPending && <Loading />}
      <ErrorBanner error={batches.error} />
      {batches.data?.length === 0 && (
        <Card>
          <p className="text-sm text-stone-600 dark:text-stone-400">No batches yet. Receive your first batch to give each plant a barcode.</p>
        </Card>
      )}
      <ul className="grid gap-3 sm:grid-cols-2">
        {batches.data?.map((batch) => (
          <li key={batch.id}>
            <BatchCard orgId={orgId} batch={batch} />
          </li>
        ))}
      </ul>
    </div>
  )
}

function BatchCard({ orgId, batch }: { orgId: number; batch: BatchDto }) {
  const { growing, forSale, held, sold, discarded } = batch.plantCounts
  const counts = [
    [growing, 'growing'],
    [forSale, 'for sale'],
    [held, 'held for orders'],
    [sold, 'sold'],
    [discarded, 'discarded'],
  ].filter(([n]) => (n as number) > 0)

  return (
    <Link
      to={`/orgs/${orgId}/batches/${batch.id}`}
      className="block rounded-xl border border-stone-200 bg-white p-4 shadow-sm hover:border-emerald-600 dark:border-stone-800 dark:bg-stone-900"
    >
      <p className="text-sm text-stone-500">
        {batch.code} · {formatDay(batch.receivedOn)}
        {batch.source && <> · {batch.source}</>}
      </p>
      <p className="mt-1 font-medium">
        {batch.plantName}
        {batch.variegation && <span className="text-stone-600 dark:text-stone-400"> {batch.variegation}</span>}
      </p>
      <p className="mt-1 text-sm text-stone-600 dark:text-stone-400">
        {counts.length > 0 ? counts.map(([n, label]) => `${n} ${label}`).join(' · ') : 'No plants'}
      </p>
    </Link>
  )
}
