import { useState } from 'react'
import { Link } from 'react-router'
import { useMembership, useReturnActions, useReturns, useReviewRefund, useUnreturnedRefunds } from '../api/hooks'
import { can } from '../api/roles'
import { Permissions, type ReturnDto, type ReturnItemDto } from '../api/types'
import { formatDate, formatMoney } from '../lib/format'
import { Badge, Button, Card, ErrorBanner, Loading } from './ui'

const SHOPIFY_STATUS: Record<ReturnDto['shopifyStatus'], string> = {
  REQUESTED: 'Requested',
  OPEN: 'Approved',
  CLOSED: 'Closed',
  DECLINED: 'Declined',
  CANCELED: 'Canceled',
}

/**
 * Returns: customers ask in Shopify to send plants back. A refund needs the plant physically back:
 * staff mark which plant came back, check it, and issue the refund in Shopify within a day.
 */
export function ReturnsView({ orgId }: { orgId: number }) {
  const [show, setShow] = useState<'active' | 'done'>('active')
  const returns = useReturns(orgId, show)

  return (
    <div className="space-y-3">
      <UnreturnedRefunds orgId={orgId} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-stone-500">Refunds are only for plants physically returned, and are issued in Shopify within a day of receiving them.</p>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={show === 'done'} onChange={(e) => setShow(e.target.checked ? 'done' : 'active')} />
          Show finished returns
        </label>
      </div>
      {returns.isPending ? (
        <Loading />
      ) : returns.error ? (
        <ErrorBanner error={returns.error} />
      ) : returns.data.length === 0 ? (
        <p className="py-6 text-center text-sm text-stone-500">{show === 'active' ? 'No returns to handle.' : 'None yet.'}</p>
      ) : (
        <ul className="space-y-3">
          {returns.data.map((r) => (
            <li key={r.id}>
              <ReturnCard orgId={orgId} ret={r} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Refunds issued in Shopify without a return: "Refund" clicked instead of "Return", most likely. */
function UnreturnedRefunds({ orgId }: { orgId: number }) {
  const membership = useMembership(orgId)
  const refunds = useUnreturnedRefunds(orgId)
  const review = useReviewRefund(orgId)
  if (!refunds.data?.length) return null

  return (
    <section className="space-y-2 rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-900 dark:bg-red-950">
      <h2 className="font-semibold text-red-900 dark:text-red-200">Refunded without a return</h2>
      <p className="text-sm text-red-900 dark:text-red-200">
        These were refunded in Shopify without a return, so the plant may not have come back. Next time, use Return on the order, then refund once the plant is back.
        Look into each one, then mark it reviewed.
      </p>
      <ul className="divide-y divide-red-200 dark:divide-red-900">
        {refunds.data.map((refund) => (
          <li key={refund.id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
            <span>
              <span className="font-medium">{refund.orderName}</span> · {formatDate(refund.refundedAt)}
              {refund.amountCents !== null && <> · {formatMoney(refund.amountCents)}</>} · {refund.items ?? 'money only, no items'}
            </span>
            <span className="flex items-center gap-2">
              <a href={refund.adminUrl} target="_blank" rel="noreferrer" className="text-emerald-700 hover:underline dark:text-emerald-400">
                Open in Shopify ↗
              </a>
              {can(membership, Permissions.OrdersPrepare) && (
                <Button variant="secondary" disabled={review.isPending} onClick={() => review.mutate(refund.id)}>
                  Mark reviewed
                </Button>
              )}
            </span>
          </li>
        ))}
      </ul>
      <ErrorBanner error={review.error} />
    </section>
  )
}

function ReturnCard({ orgId, ret }: { orgId: number; ret: ReturnDto }) {
  return (
    <Card
      title={`${ret.name} · order ${ret.orderName}`}
      actions={
        <a href={ret.adminUrl} target="_blank" rel="noreferrer" className="text-sm text-emerald-700 hover:underline dark:text-emerald-400">
          Open in Shopify ↗
        </a>
      }
    >
      <div className="-mt-2 mb-3 flex flex-wrap items-center gap-2 text-xs text-stone-500">
        <Badge tone={ret.shopifyStatus === 'DECLINED' || ret.shopifyStatus === 'CANCELED' ? 'neutral' : 'amber'}>{SHOPIFY_STATUS[ret.shopifyStatus]}</Badge>
        <span>Requested {formatDate(ret.requestedAt)}</span>
      </div>
      <RefundStatus ret={ret} />
      <ul className="mt-3 divide-y divide-stone-200 dark:divide-stone-800">
        {ret.items.map((item) => (
          <li key={item.id} className="py-3">
            <ReturnItem orgId={orgId} returnId={ret.id} item={item} />
          </li>
        ))}
      </ul>
    </Card>
  )
}

function RefundStatus({ ret }: { ret: ReturnDto }) {
  if (ret.refunded) return <Badge tone="green">Refunded</Badge>
  if (!ret.refundDueAt) return <p className="text-sm text-stone-600 dark:text-stone-400">Waiting for the plant to come back. No refund until it does.</p>
  const overdue = ret.refundOverdue
  return (
    <p className={`text-sm ${overdue ? 'font-medium text-red-700 dark:text-red-400' : 'text-stone-600 dark:text-stone-400'}`}>
      {overdue ? 'Refund overdue: it was due ' : 'Refund due by '}
      {new Date(ret.refundDueAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}. Issue it in Shopify.
    </p>
  )
}

function ReturnItem({ orgId, returnId, item }: { orgId: number; returnId: number; item: ReturnItemDto }) {
  const membership = useMembership(orgId)
  const actions = useReturnActions(orgId)
  const canHandle = can(membership, Permissions.OrdersPrepare)
  const stillComing = item.quantity - item.receivedCount

  return (
    <div className="space-y-2 text-sm">
      <p>
        <span className="font-medium">{item.quantity} ×</span> {item.title}
        {item.potSize && <span className="text-stone-500"> · {item.potSize}</span>}
        {item.reason && <span className="text-stone-500"> · {item.reason}</span>}
      </p>
      {item.customerNote && <p className="border-l-2 border-stone-300 pl-2 text-stone-600 italic dark:border-stone-700 dark:text-stone-400">“{item.customerNote}”</p>}

      {canHandle && stillComing > 0 && item.soldBarcodes.length > 0 && (
        <div className="space-y-1">
          <p className="text-xs text-stone-500">When it’s back, mark which plant ({stillComing} still to come):</p>
          <div className="flex flex-wrap gap-1.5">
            {item.soldBarcodes.map((barcode) => (
              <Button
                key={barcode}
                variant="secondary"
                disabled={actions.receive.isPending}
                onClick={() => window.confirm(`Is ${barcode} back with you?`) && actions.receive.mutate({ returnId, barcode })}
              >
                {barcode} is back
              </Button>
            ))}
          </div>
        </div>
      )}

      {item.waitingBarcodes.map((barcode) => (
        <div key={barcode} className="flex flex-wrap items-center gap-2 rounded-lg bg-amber-50 p-2 dark:bg-amber-950">
          <Link to={`/orgs/${orgId}/plants/${barcode}`} className="font-mono text-xs hover:underline">
            {barcode}
          </Link>
          <span className="text-xs text-stone-600 dark:text-stone-400">back: check it, then</span>
          {canHandle && (
            <>
              <Button disabled={actions.check.isPending} onClick={() => actions.check.mutate({ barcode, to: 'for_sale' })}>
                Back on sale
              </Button>
              <Button variant="secondary" disabled={actions.check.isPending} onClick={() => actions.check.mutate({ barcode, to: 'growing' })}>
                Back to growing
              </Button>
              <Button
                variant="danger"
                disabled={actions.check.isPending}
                onClick={() => {
                  const reason = window.prompt(`Why is ${barcode} being discarded?`)
                  if (reason?.trim()) actions.check.mutate({ barcode, to: 'discarded', reason })
                }}
              >
                Discard
              </Button>
            </>
          )}
        </div>
      ))}
      <ErrorBanner error={actions.receive.error ?? actions.check.error} />
    </div>
  )
}
