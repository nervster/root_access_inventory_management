import type { PlantEventDto } from '../api/types'
import { formatDate } from '../lib/format'

/** What an event says, in plain words. */
function describe(event: PlantEventDto): { text: string; note?: string } {
  const d = event.details
  switch (event.type) {
    case 'received':
      return { text: `Received into the ${d.toPotSize ?? 'first pot'}`, note: d.batch && `Batch ${d.batch}` }
    case 'repotted':
      return { text: `Moved ${d.fromPotSize} → ${d.toPotSize}` }
    case 'marked_for_sale':
      return { text: 'Marked for sale' }
    case 'unmarked_for_sale':
      return { text: 'Taken off sale' }
    case 'sold':
      return { text: 'Sold' }
    case 'discarded':
      return { text: 'Discarded', note: d.reason }
    case 'name_corrected':
      return { text: 'Name corrected', note: `${d.from} → ${d.to}` }
    case 'photo_added':
      return { text: 'Photo added', note: d.caption }
    case 'photo_deleted':
      return { text: 'Photo deleted', note: d.caption }
  }
}

// The dot's color says what kind of event it was; the words always say it too.
const dot: Record<PlantEventDto['type'], string> = {
  received: 'bg-emerald-700',
  repotted: 'bg-stone-500',
  marked_for_sale: 'bg-emerald-500',
  unmarked_for_sale: 'bg-stone-400',
  sold: 'bg-amber-500',
  discarded: 'bg-red-600',
  name_corrected: 'bg-stone-500',
  photo_added: 'bg-sky-600',
  photo_deleted: 'bg-stone-400',
}

/** A plant's life, newest first: every move, status change, and photo, with who and when. */
export function PlantHistory({ history }: { history: PlantEventDto[] }) {
  if (history.length === 0) return <p className="text-sm text-stone-600 dark:text-stone-400">No history yet.</p>
  return (
    <ol className="space-y-3 border-l-2 border-stone-200 pl-4 dark:border-stone-800">
      {[...history].reverse().map((event, i) => {
        const { text, note } = describe(event)
        return (
          <li key={i} className="relative text-sm">
            <span className={`absolute top-1.5 -left-[21px] size-2 rounded-full ${dot[event.type]}`} aria-hidden="true" />
            <p className="font-medium">{text}</p>
            {note && <p>{note}</p>}
            <p className="text-stone-500">
              {formatDate(event.occurredAt)}
              {event.changedBy && ` · ${event.changedBy}`}
            </p>
          </li>
        )
      })}
    </ol>
  )
}
