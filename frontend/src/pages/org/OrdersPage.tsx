import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { useCheckOrders, useMembership, useOrders, useOrdersStatus } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type OrderDto, type OrderItemDto, type OrderStatus } from '../../api/types'
import { Badge, Button, Card, ErrorBanner, Loading, Notice, PageHeader } from '../../components/ui'
import { formatDate, formatMoney } from '../../lib/format'

const TABS: { status: OrderStatus; label: string }[] = [
  { status: 'open', label: 'To pick up' },
  { status: 'picked_up', label: 'Picked up' },
  { status: 'cancelled', label: 'Cancelled' },
]

/**
 * Online orders: for each one waiting for pickup, the exact plants (by barcode) held for it, so
 * staff can pull them. Orders arrive from Shopify within a minute.
 */
export function OrdersPage() {
  const orgId = Number(useParams().orgId)
  const [status, setStatus] = useState<OrderStatus>('open')
  const orders = useOrders(orgId, status)

  return (
    <div className="space-y-4">
      <PageHeader title="Orders" subtitle="Bought online, waiting for pickup. The plants listed are held for each order." />
      <ImportStatus orgId={orgId} />

      <div role="tablist" className="flex gap-1">
        {TABS.map((tab) => (
          <button
            key={tab.status}
            type="button"
            role="tab"
            aria-selected={status === tab.status}
            onClick={() => setStatus(tab.status)}
            className={`rounded-lg px-3 py-2 text-sm font-medium ${
              status === tab.status
                ? 'bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'
                : 'text-stone-600 hover:bg-stone-100 dark:text-stone-400 dark:hover:bg-stone-800'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {orders.isPending ? (
        <Loading />
      ) : orders.error ? (
        <ErrorBanner error={orders.error} />
      ) : orders.data.length === 0 ? (
        <p className="py-6 text-center text-sm text-stone-500">{status === 'open' ? 'No orders waiting for pickup.' : 'None yet.'}</p>
      ) : (
        <ul className="space-y-3">
          {orders.data.map((order) => (
            <li key={order.id}>
              <OrderCard orgId={orgId} order={order} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** When orders were last read from Shopify, why not, and a button to read them now. */
function ImportStatus({ orgId }: { orgId: number }) {
  const membership = useMembership(orgId)
  const status = useOrdersStatus(orgId)
  const check = useCheckOrders(orgId)

  if (!status.data) return null
  if (!status.data.connected) {
    return <Notice>No Shopify store is connected, so no online orders arrive. An Owner can connect one in Settings.</Notice>
  }
  return (
    <div className="space-y-2">
      {status.data.error && (
        <ErrorBanner
          error={
            new Error(
              status.data.error.includes('not approved')
                ? 'Shopify isn’t letting Plant Stock read orders yet. In the Shopify Dev Dashboard, open Plant Stock and turn on access to protected customer data (orders), then check again.'
                : `Couldn’t read orders from Shopify: ${status.data.error}`,
            )
          }
        />
      )}
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-stone-500">
        <span>{status.data.checkedAt ? `Orders checked ${timeAgo(status.data.checkedAt)}.` : 'Orders haven’t been checked yet.'} New ones arrive within a minute.</span>
        {can(membership, Permissions.InventoryEdit) && (
          <Button variant="secondary" disabled={check.isPending} onClick={() => check.mutate()}>
            {check.isPending ? 'Checking…' : 'Check now'}
          </Button>
        )}
      </div>
      <ErrorBanner error={check.error} />
    </div>
  )
}

function OrderCard({ orgId, order }: { orgId: number; order: OrderDto }) {
  return (
    <Card
      title={order.name}
      actions={
        <a href={order.adminUrl} target="_blank" rel="noreferrer" className="text-sm text-emerald-700 hover:underline dark:text-emerald-400">
          Open in Shopify ↗
        </a>
      }
    >
      <p className="-mt-2 mb-3 text-xs text-stone-500">
        Placed {formatDate(order.placedAt)}
        {order.cancelledAt && <> · cancelled {formatDate(order.cancelledAt)}</>}
      </p>
      <ul className="divide-y divide-stone-200 dark:divide-stone-800">
        {order.items.map((item) => (
          <li key={item.id} className="py-2">
            <OrderItem orgId={orgId} item={item} open={order.status === 'open'} />
          </li>
        ))}
      </ul>
    </Card>
  )
}

function OrderItem({ orgId, item, open }: { orgId: number; item: OrderItemDto; open: boolean }) {
  const short = item.quantity - item.heldBarcodes.length
  return (
    <div className="space-y-1 text-sm">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span>
          <span className="font-medium">{item.quantity} ×</span> {item.title}
          {item.potSize && <span className="text-stone-500"> · {item.potSize}</span>}
        </span>
        {item.priceCents !== null && <span className="text-stone-500">{formatMoney(item.priceCents)} each</span>}
      </div>
      {!item.inPlantStock ? (
        <Badge>Not a Plant Stock product: nothing held</Badge>
      ) : (
        <>
          {item.heldBarcodes.length > 0 && (
            <p className="flex flex-wrap gap-1.5">
              {item.heldBarcodes.map((barcode) => (
                <Link
                  key={barcode}
                  to={`/orgs/${orgId}/plants/${barcode}`}
                  className="rounded-md bg-stone-100 px-2 py-0.5 font-mono text-xs hover:bg-emerald-100 dark:bg-stone-800 dark:hover:bg-emerald-950"
                >
                  {barcode}
                </Link>
              ))}
            </p>
          )}
          {open && short > 0 && (
            <p className="text-xs text-red-700 dark:text-red-400">
              {short} more than were for sale: {item.heldBarcodes.length} of {item.quantity} held. Contact the customer or refund the rest in Shopify.
            </p>
          )}
        </>
      )}
    </div>
  )
}

function timeAgo(iso: string) {
  const minutes = Math.round((Date.now() - new Date(iso).getTime()) / 60_000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes} min ago`
  return formatDate(iso)
}
