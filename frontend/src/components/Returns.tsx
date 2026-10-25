import { useState } from 'react'
import { Link } from 'react-router'
import { useApprovedRefunds, useApproveRefund, useMembership, useReturnActions, useReturns, useUnreturnedRefunds } from '../api/hooks'
import { can } from '../api/roles'
import { Permissions, type ReturnDto, type ReturnItemDto, type UnreturnedRefundDto } from '../api/types'
import { formatDate, formatMoney } from '../lib/format'
import { Badge, Button, Card, ErrorBanner, Input, Loading } from './ui'

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

/**
 * Refunds issued in Shopify without a return. Most are "Refund" clicked instead of "Return"; a
 * special case (the customer keeps the plant) is fine once an Owner approves it, saying why.
 */
function UnreturnedRefunds({ orgId }: { orgId: number }) {
  const membership = useMembership(orgId)
  const refunds = useUnreturnedRefunds(orgId)
  const [showApproved, setShowApproved] = useState(false)
  const approved = useApprovedRefunds(orgId, showApproved)
  const canApprove = can(membership, Permissions.RefundsApprove)

  return (
    <div className="space-y-2">
      {refunds.data && refunds.data.length > 0 && (
        <section className="space-y-2 rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-900 dark:bg-red-950">
          <h2 className="font-semibold text-red-900 dark:text-red-200">Refunded without a return</h2>
          <p className="text-sm text-red-900 dark:text-red-200">
            These were refunded in Shopify without a return, so the plant may not have come back. If it was a mistake, use Return on the order next time. If it’s a
            special case, an Owner approves it here and says why.
          </p>
          <ul className="divide-y divide-red-200 dark:divide-red-900">
            {refunds.data.map((refund) => (
              <li key={refund.id} className="py-2">
                <UnreturnedRefund orgId={orgId} refund={refund} canApprove={canApprove} />
              </li>
            ))}
          </ul>
        </section>
      )}
      <button type="button" className="text-sm text-stone-500 underline" onClick={() => setShowApproved(!showApproved)}>
        {showApproved ? 'Hide approved special cases' : 'Show approved special cases'}
      </button>
      {showApproved && (
        <div className="text-sm">
          {approved.isPending ? (
            <Loading />
          ) : approved.error ? (
            <ErrorBanner error={approved.error} />
          ) : approved.data.length === 0 ? (
            <p className="text-stone-500">None yet.</p>
          ) : (
            <ul className="divide-y divide-stone-200 dark:divide-stone-800">
              {approved.data.map((refund) => (
                <li key={refund.id} className="py-2">
                  <p>
                    <span className="font-medium">{refund.orderName}</span> · {formatDate(refund.refundedAt)}
                    {refund.amountCents !== null && <> · {formatMoney(refund.amountCents)}</>} · {refund.items ?? 'money only, no items'}
                  </p>
                  <p className="text-stone-600 dark:text-stone-400">
                    “{refund.note}” · approved {formatDate(refund.approvedAt)}
                    {refund.approvedByName && ` by ${refund.approvedByName}`}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

function UnreturnedRefund({ orgId, refund, canApprove }: { orgId: number; refund: UnreturnedRefundDto; canApprove: boolean }) {
  const approve = useApproveRefund(orgId)
  const [approving, setApproving] = useState(false)
  const [note, setNote] = useState('')

  return (
    <div className="space-y-2 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span>
          <span className="font-medium">{refund.orderName}</span> · {formatDate(refund.refundedAt)}
          {refund.amountCents !== null && <> · {formatMoney(refund.amountCents)}</>} · {refund.items ?? 'money only, no items'}
        </span>
        <span className="flex items-center gap-2">
          <a href={refund.adminUrl} target="_blank" rel="noreferrer" className="text-emerald-700 hover:underline dark:text-emerald-400">
            Open in Shopify ↗
          </a>
          {canApprove && !approving && (
            <Button variant="secondary" onClick={() => setApproving(true)}>
              Approve as special case…
            </Button>
          )}
        </span>
      </div>
      {!canApprove && <p className="text-xs text-red-900 dark:text-red-200">Waiting for an Owner to approve it.</p>}
      {approving && (
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault()
            approve.mutate({ refundId: refund.id, note })
          }}
        >
          <Input
            required
            autoFocus
            maxLength={1000}
            placeholder="Why it’s refunded without a return, e.g. arrived damaged, photo checked, customer kept it"
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={approve.isPending || !note.trim()}>
              {approve.isPending ? 'Approving…' : 'Approve'}
            </Button>
            <Button type="button" variant="secondary" disabled={approve.isPending} onClick={() => setApproving(false)}>
              Cancel
            </Button>
          </div>
          <ErrorBanner error={approve.error} />
        </form>
      )}
    </div>
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
        <WaitingPlant key={barcode} orgId={orgId} barcode={barcode} canHandle={canHandle} />
      ))}
      <ErrorBanner error={actions.receive.error} />
    </div>
  )
}

/** A returned plant waiting for its check, and what to do with it after. */
function WaitingPlant({ orgId, barcode, canHandle }: { orgId: number; barcode: string; canHandle: boolean }) {
  const { check } = useReturnActions(orgId)
  const [discarding, setDiscarding] = useState(false)
  const [reason, setReason] = useState('')

  return (
    <div className="space-y-2 rounded-lg bg-amber-50 p-2 dark:bg-amber-950">
      <div className="flex flex-wrap items-center gap-2">
        <Link to={`/orgs/${orgId}/plants/${barcode}`} className="font-mono text-xs hover:underline">
          {barcode}
        </Link>
        <span className="text-xs text-stone-600 dark:text-stone-400">back: check it, then</span>
        {canHandle && !discarding && (
          <>
            <Button disabled={check.isPending} onClick={() => check.mutate({ barcode, to: 'for_sale' })}>
              Back on sale
            </Button>
            <Button variant="secondary" disabled={check.isPending} onClick={() => check.mutate({ barcode, to: 'growing' })}>
              Back to growing
            </Button>
            <Button variant="danger" disabled={check.isPending} onClick={() => setDiscarding(true)}>
              Discard…
            </Button>
          </>
        )}
      </div>
      {discarding && (
        <div className="space-y-2">
          <p className="text-sm font-medium">Discard {barcode}? It stays in the records, marked discarded.</p>
          <Input placeholder="Reason (optional), e.g. root rot" maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
          <div className="flex flex-wrap gap-2">
            <Button variant="danger" disabled={check.isPending} onClick={() => check.mutate({ barcode, to: 'discarded', reason: reason.trim() || undefined })}>
              {check.isPending ? 'Discarding…' : `Discard ${barcode}`}
            </Button>
            <Button variant="secondary" disabled={check.isPending} onClick={() => setDiscarding(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
      <ErrorBanner error={check.error} />
    </div>
  )
}
