import { Link, useParams } from 'react-router'
import { useDashboard, useMembership, useOrganization } from '../../api/hooks'
import type { DashboardDto } from '../../api/types'
import { Card, ErrorBanner, Loading, PageHeader } from '../../components/ui'
import { formatCount, formatMoney } from '../../lib/format'

/** The nursery's home: inventory and sales at a glance. */
export function DashboardPage() {
  const orgId = Number(useParams().orgId)
  const org = useOrganization(orgId)
  const membership = useMembership(orgId)
  const dashboard = useDashboard(orgId)

  if (dashboard.isPending) return <Loading />
  if (dashboard.error) return <ErrorBanner error={dashboard.error} />
  const d = dashboard.data

  return (
    <div className="space-y-4">
      <PageHeader title={org.data?.name ?? membership?.organizationName ?? 'Dashboard'} />

      <Card>
        <p className="text-sm text-stone-600 dark:text-stone-400">For sale now</p>
        <p className="mt-1 text-5xl font-semibold tracking-tight">{formatMoney(d.forSaleValueCents)}</p>
        <p className="mt-1 text-sm text-stone-600 dark:text-stone-400">
          {formatCount(d.plants.forSale)} plant{d.plants.forSale === 1 ? '' : 's'} at their listed prices
        </p>
      </Card>

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <StatTile label="Growing" value={d.plants.growing} />
        <StatTile
          label="Ready to mark for sale"
          value={d.readyToMark}
          hint="Growing in a pot that can be sold"
          attention={d.readyToMark > 0}
          to={`/orgs/${orgId}/batches`}
        />
        <StatTile
          label="Needs a price"
          value={d.needsPrice}
          hint="For sale, but their SKU has no price"
          attention={d.needsPrice > 0}
          to={`/orgs/${orgId}/products`}
        />
        <StatTile label="Received" value={d.last30Days.received} hint="Last 30 days" />
        <StatTile label="Discarded" value={d.last30Days.discarded} hint="Last 30 days" />
      </div>

      <Card title="Growth pipeline">
        <Pipeline orgId={orgId} potSizes={d.potSizes} />
      </Card>

      <Card title="Sales">
        <p className="text-sm text-stone-600 dark:text-stone-400">
          {formatCount(d.plants.sold)} plant{d.plants.sold === 1 ? '' : 's'} sold so far. Orders, revenue, and profit appear here once
          sales come in from Shopify.
        </p>
      </Card>
    </div>
  )
}

function StatTile({ label, value, hint, attention, to }: { label: string; value: number; hint?: string; attention?: boolean; to?: string }) {
  const className = `block rounded-xl border bg-white p-3 shadow-sm dark:bg-stone-900 ${
    attention ? 'border-amber-300 dark:border-amber-800' : 'border-stone-200 dark:border-stone-800'
  } ${to ? 'hover:border-emerald-600' : ''}`
  const content = (
    <>
      <p className="text-sm text-stone-600 dark:text-stone-400">{label}</p>
      <p className="mt-1 text-2xl font-semibold">{formatCount(value)}</p>
      {hint && <p className="mt-0.5 text-xs text-stone-500">{hint}</p>}
    </>
  )
  return to ? (
    <Link to={to} className={className}>
      {content}
    </Link>
  ) : (
    <div className={className}>{content}</div>
  )
}

/**
 * One horizontal stacked bar per pot size, smallest first: growing and for-sale plants. A legend
 * names the two series, and each row states its numbers in text, so color is never the only cue.
 */
function Pipeline({ orgId, potSizes }: { orgId: number; potSizes: DashboardDto['potSizes'] }) {
  if (potSizes.length === 0)
    return (
      <p className="text-sm text-stone-600 dark:text-stone-400">
        No pot sizes yet.{' '}
        <Link to={`/orgs/${orgId}/pot-sizes`} className="font-medium text-emerald-800 underline dark:text-emerald-400">
          Add your pot sizes
        </Link>{' '}
        to track plants as they grow.
      </p>
    )

  const largest = Math.max(1, ...potSizes.map((p) => p.growing + p.forSale))
  return (
    <div className="space-y-3">
      <div className="flex gap-4 text-xs text-stone-600 dark:text-stone-400" aria-hidden="true">
        <LegendKey color="var(--viz-growing)" label="Growing" />
        <LegendKey color="var(--viz-for-sale)" label="For sale" />
      </div>
      <ul className="space-y-3">
        {potSizes.map((p) => (
          <li key={p.id}>
            <div className="mb-1 flex items-baseline justify-between gap-2 text-sm">
              <span className="font-medium">
                {p.name}
                {!p.sellable && <span className="ml-2 text-xs font-normal text-stone-500">grow-on only</span>}
              </span>
              <span className="text-stone-600 tabular-nums dark:text-stone-400">
                {formatCount(p.growing)} growing · {formatCount(p.forSale)} for sale
              </span>
            </div>
            <div className="flex h-3 gap-[2px]" role="img" aria-label={`${p.name}: ${p.growing} growing, ${p.forSale} for sale`}>
              <Segment value={p.growing} largest={largest} color="var(--viz-growing)" label={`${p.growing} growing`} last={p.forSale === 0} />
              <Segment value={p.forSale} largest={largest} color="var(--viz-for-sale)" label={`${p.forSale} for sale`} last />
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** A stacked-bar segment; only the bar's far end is rounded. Hovering shows its value. */
function Segment({ value, largest, color, label, last }: { value: number; largest: number; color: string; label: string; last: boolean }) {
  if (value === 0) return null
  return (
    <div className="group relative h-full" style={{ width: `${(value / largest) * 100}%` }}>
      <div className={`h-full ${last ? 'rounded-r' : ''}`} style={{ background: color }} />
      <span className="pointer-events-none absolute bottom-full left-1/2 z-10 mb-1 hidden -translate-x-1/2 whitespace-nowrap rounded-md bg-stone-900 px-2 py-1 text-xs text-white shadow group-hover:block dark:bg-stone-100 dark:text-stone-900">
        {label}
      </span>
    </div>
  )
}

function LegendKey({ color, label }: { color: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <span className="inline-block size-2.5 rounded-sm" style={{ background: color }} />
      {label}
    </span>
  )
}
