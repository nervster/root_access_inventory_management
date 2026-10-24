import { useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router'
import { useCheckOrders, useMembership, useOrderActions, useOrders, useOrdersStatus, usePickupCalendar } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type OrderDto, type OrderItemDto, type OrderStatus } from '../../api/types'
import { ReturnsView } from '../../components/Returns'
import { Badge, Button, Card, ErrorBanner, Field, Loading, Notice, PageHeader, Select } from '../../components/ui'
import { formatDate, formatMoney } from '../../lib/format'

type Tab = OrderStatus | 'returns'

const TABS: { status: Tab; label: string }[] = [
  { status: 'open', label: 'To prepare' },
  { status: 'ready_for_pickup', label: 'Ready for pickup' },
  { status: 'picked_up', label: 'Picked up' },
  { status: 'cancelled', label: 'Cancelled' },
  { status: 'returns', label: 'Returns' },
]

/**
 * Online orders: for each one waiting for pickup, the exact plants (by barcode) held for it, so
 * staff can pull them. Orders arrive from Shopify within a minute.
 */
export function OrdersPage() {
  const orgId = Number(useParams().orgId)
  const [tab, setTab] = useState<Tab>('open')

  return (
    <div className="space-y-4">
      <PageHeader
        title="Orders"
        subtitle="Bought online. Pull the plants listed for each order, put it in the locker, and mark it ready; once the customer has it, mark it picked up."
      />
      <ImportStatus orgId={orgId} />

      <div role="tablist" className="flex gap-1 overflow-x-auto">
        {TABS.map((t) => (
          <button
            key={t.status}
            type="button"
            role="tab"
            aria-selected={tab === t.status}
            onClick={() => setTab(t.status)}
            className={`rounded-lg px-3 py-2 text-sm font-medium ${
              tab === t.status
                ? 'bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'
                : 'text-stone-600 hover:bg-stone-100 dark:text-stone-400 dark:hover:bg-stone-800'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'returns' ? <ReturnsView orgId={orgId} /> : <OrderList orgId={orgId} status={tab} />}
    </div>
  )
}

function OrderList({ orgId, status }: { orgId: number; status: OrderStatus }) {
  const orders = useOrders(orgId, status)
  return (
    <>
      {orders.isPending ? (
        <Loading />
      ) : orders.error ? (
        <ErrorBanner error={orders.error} />
      ) : orders.data.length === 0 ? (
        <p className="py-6 text-center text-sm text-stone-500">{EMPTY[status]}</p>
      ) : (
        <ul className="space-y-3">
          {orders.data.map((order) => (
            <li key={order.id}>
              <OrderCard orgId={orgId} order={order} />
            </li>
          ))}
        </ul>
      )}
    </>
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

const EMPTY: Record<OrderStatus, string> = {
  open: 'No orders to prepare.',
  ready_for_pickup: 'No orders waiting in the locker.',
  picked_up: 'None yet.',
  cancelled: 'None yet.',
}

/** When the customer collects it, as booked from the cart; and anything about the booking staff must sort out. */
function OrderPickup({ order }: { order: OrderDto }) {
  if (!order.pickupDate && !order.pickupWindow && !order.pickupProblem) return null
  // Changed after staff put it in the locker: the plants may need moving until the new time.
  const changedWhileReady = order.pickupChangedAt && order.readyAt && order.pickupChangedAt > order.readyAt && order.status === 'ready_for_pickup'
  const date = order.pickupDate
    ? new Date(`${order.pickupDate}T00:00:00Z`).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })
    : null
  return (
    <div className="mb-3 space-y-1 text-sm">
      {(date || order.pickupWindow) && (
        <p>
          <span className="font-medium">Pickup:</span> {[date, order.pickupWindow].filter(Boolean).join(', ')}
        </p>
      )}
      {order.pickupChangedAt && (
        <p className={changedWhileReady ? 'rounded-lg bg-amber-50 px-3 py-2 text-amber-900 dark:bg-amber-950 dark:text-amber-200' : 'text-xs text-stone-500'}>
          The customer changed the pickup time {formatDate(order.pickupChangedAt)}
          {changedWhileReady && ', after it was ready: check the locker'}.
        </p>
      )}
      {order.pickupNote && <p className="text-stone-600 dark:text-stone-400">“{order.pickupNote}”</p>}
      {order.pickupProblem && (
        <p role="alert" className="rounded-lg bg-red-50 px-3 py-2 text-red-800 dark:bg-red-950 dark:text-red-300">
          {order.pickupProblem} Contact the customer to agree a pickup time.
        </p>
      )}
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
        {order.readyAt && <> · ready {formatDate(order.readyAt)}{order.readyByName && ` by ${order.readyByName}`}</>}
        {order.pickedUpAt && <> · picked up {formatDate(order.pickedUpAt)}{order.pickedUpByName && ` (marked by ${order.pickedUpByName})`}</>}
      </p>
      <OrderPickup order={order} />
      <MovePickup orgId={orgId} order={order} />
      <ul className="divide-y divide-stone-200 dark:divide-stone-800">
        {order.items.map((item) => (
          <li key={item.id} className="py-2">
            <OrderItem orgId={orgId} item={item} open={order.status === 'open'} />
          </li>
        ))}
      </ul>
      <OrderButtons orgId={orgId} order={order} />
    </Card>
  )
}

/** The next step for an order, for those who prepare orders. */
/**
 * Staff move an order still to collect to another pickup slot, as agreed with the customer (a clash, or a
 * call): windows starting within the lead time or past the booking range are fine; closed, taken and
 * finished ones aren't offered.
 */
function MovePickup({ orgId, order }: { orgId: number; order: OrderDto }) {
  const membership = useMembership(orgId)
  const [open, setOpen] = useState(false)
  if (!can(membership, Permissions.OrdersPrepare) || (order.status !== 'open' && order.status !== 'ready_for_pickup')) return null
  if (!open)
    return (
      <Button variant="secondary" className="mb-3" onClick={() => setOpen(true)}>
        Change pickup time
      </Button>
    )
  return <MovePickupForm orgId={orgId} order={order} onDone={() => setOpen(false)} />
}

function MovePickupForm({ orgId, order, onDone }: { orgId: number; order: OrderDto; onDone: () => void }) {
  const calendar = usePickupCalendar(orgId, '', '')
  const { movePickup } = useOrderActions(orgId)
  const [now] = useState(() => Date.now())
  const [date, setDate] = useState(order.pickupDate ?? '')
  const [windowId, setWindowId] = useState('')
  const bookable = (calendar.data?.days ?? []).map((d) => ({
    ...d,
    slots: d.slots.filter((s) => s.status !== 'closed' && s.status !== 'taken' && new Date(s.end).getTime() > now),
  }))
  const day = bookable.find((d) => d.date === date)
  const timeZone = calendar.data?.timeZone
  const time = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', timeZone })

  const submit = (event: FormEvent) => {
    event.preventDefault()
    movePickup.mutate({ orderId: order.id, date, windowId: Number(windowId) }, { onSuccess: onDone })
  }

  return (
    <form onSubmit={submit} className="mb-3 space-y-2 rounded-lg border border-stone-200 p-3 dark:border-stone-800">
      <p className="text-sm font-medium">Change pickup time</p>
      {calendar.isPending ? (
        <Loading />
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <div className="w-48">
            <Field label="Date">
              <Select
                value={date}
                onChange={(e) => {
                  setDate(e.target.value)
                  setWindowId('')
                }}
              >
                <option value="">Choose a date</option>
                {bookable.map((d) => (
                  <option key={d.date} value={d.date} disabled={d.slots.length === 0}>
                    {new Date(`${d.date}T00:00:00Z`).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })}
                    {d.slots.length === 0 ? ' (full or closed)' : ''}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <div className="w-64">
            <Field label="Window">
              <Select value={windowId} onChange={(e) => setWindowId(e.target.value)} disabled={!day?.slots.length}>
                <option value="">Choose a window</option>
                {day?.slots.map((s) => (
                  <option key={s.windowId} value={s.windowId}>
                    {s.name} ({time(s.start)} – {time(s.end)}){s.status === 'too_soon' ? ', starts within 8 hours' : ''}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Button type="submit" disabled={!date || !windowId || movePickup.isPending}>
            Move
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        </div>
      )}
      <ErrorBanner error={calendar.error ?? movePickup.error} />
      <p className="text-xs text-stone-500">Shopify’s copy of the order is updated too. Let the customer know the new time.</p>
    </form>
  )
}

function OrderButtons({ orgId, order }: { orgId: number; order: OrderDto }) {
  const membership = useMembership(orgId)
  const actions = useOrderActions(orgId)
  if (!can(membership, Permissions.OrdersPrepare) || (order.status !== 'open' && order.status !== 'ready_for_pickup')) return null

  const busy = actions.ready.isPending || actions.notReady.isPending || actions.pickedUp.isPending
  const plants = order.items.flatMap((item) => item.heldBarcodes)
  const pickedUp = () =>
    window.confirm(
      `Mark ${order.name} picked up?\n\n${plants.length ? `${plants.join(', ')} will be sold. ` : ''}` +
        'This can’t be undone: if the customer never got it, it can only be reversed by a return.',
    ) && actions.pickedUp.mutate(order.id)

  return (
    <div className="mt-3 space-y-2 border-t border-stone-200 pt-3 dark:border-stone-800">
      <ErrorBanner error={actions.ready.error ?? actions.notReady.error ?? actions.pickedUp.error} />
      <div className="flex flex-wrap gap-2">
        {order.status === 'open' ? (
          <>
            <Button disabled={busy} onClick={() => actions.ready.mutate(order.id)}>
              Ready for pickup
            </Button>
            <Button variant="secondary" disabled={busy} onClick={pickedUp}>
              Picked up in person
            </Button>
          </>
        ) : (
          <>
            <Button disabled={busy} onClick={pickedUp}>
              Picked up
            </Button>
            <Button variant="ghost" disabled={busy} onClick={() => actions.notReady.mutate(order.id)}>
              Back to “To prepare”
            </Button>
          </>
        )}
      </div>
    </div>
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
