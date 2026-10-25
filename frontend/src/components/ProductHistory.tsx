import { useState } from 'react'
import { useProductHistory } from '../api/hooks'
import type { ProductEventDto } from '../api/types'
import { formatDate, formatMoney } from '../lib/format'
import { Button, ErrorBanner } from './ui'

const FIELD_LABELS = { title: 'Title', description: 'Description', productType: 'Product type', tags: 'Tags' }

/** A price, or "No price" when it isn't set. */
const price = (cents: unknown) => (typeof cents === 'number' ? formatMoney(cents) : 'No price')

const fieldValue = (value: unknown) => {
  if (Array.isArray(value)) return value.length === 0 ? 'none' : value.join(', ')
  return typeof value === 'string' && value !== '' ? value : 'none'
}

/** What an event says, in plain words. */
function describe(event: ProductEventDto): string {
  const d = event.details
  switch (event.type) {
    case 'price_changed':
      return `Price of ${d.sku}: ${price(d.from)} → ${price(d.to)}`
    case 'sku_added':
      return `SKU ${d.sku} added (${d.potSize})`
    case 'sku_deleted':
      return `SKU ${d.sku} deleted (${d.potSize})`
    case 'sku_changed':
      return `SKU code (${d.potSize}): ${d.from} → ${d.to}`
    case 'details_changed':
      return `${FIELD_LABELS[d.field ?? 'title']}: ${fieldValue(d.from)} → ${fieldValue(d.to)}`
  }
}

/** A product's prices and details over time, newest first, with who changed them and when. Loaded when opened. */
export function ProductHistory({ orgId, productId }: { orgId: number; productId: number }) {
  const [open, setOpen] = useState(false)
  const history = useProductHistory(orgId, productId, open)
  return (
    <div>
      <Button variant="ghost" onClick={() => setOpen(!open)} aria-expanded={open}>
        {open ? 'Hide history' : 'History'}
      </Button>
      {open && (
        <div className="mt-2">
          <ErrorBanner error={history.error} />
          {history.data?.length === 0 && <p className="text-sm text-stone-600 dark:text-stone-400">No history yet.</p>}
          <ol className="space-y-3 border-l-2 border-stone-200 pl-4 dark:border-stone-800">
            {[...(history.data ?? [])].reverse().map((event, i) => (
              <li key={i} className="relative text-sm">
                <span className="absolute top-1.5 -left-[21px] size-2 rounded-full bg-stone-500" aria-hidden="true" />
                <p className="font-medium">{describe(event)}</p>
                <p className="text-stone-500">
                  {formatDate(event.occurredAt)}
                  {event.changedBy && ` · ${event.changedBy}`}
                </p>
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  )
}
