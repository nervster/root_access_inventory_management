import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router'
import { useMembership, usePickupActions, usePickupCalendar, usePickupSettings } from '../../api/hooks'
import { can } from '../../api/roles'
import { Permissions, type PickupDayDto, type PickupSettingsDto, type PickupSlotDto, type PickupSlotStatus, type PickupWindowDto } from '../../api/types'
import { Badge, Button, Card, ErrorBanner, Field, Input, Loading, Notice, PageHeader, Select } from '../../components/ui'

const statusLabels: Record<PickupSlotStatus, string> = { open: 'Open', closed: 'Closed', too_soon: 'Too soon', too_far: 'Too far ahead' }
const statusDots: Record<PickupSlotStatus, string> = {
  open: 'bg-emerald-500',
  closed: 'bg-red-500',
  too_soon: 'bg-stone-400 dark:bg-stone-600',
  too_far: 'border border-stone-400 dark:border-stone-600',
}
const weekdays = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

/** Dates as "2026-10-25" strings, worked out in UTC so no time zone can shift the day. */
function isoDate(date: Date) {
  return date.toISOString().slice(0, 10)
}
function addDays(date: Date, days: number) {
  return new Date(date.getTime() + days * 86_400_000)
}

/** The month's weeks, Monday first: from the Monday on or before the 1st to the Sunday on or after the last day. */
function monthGrid(year: number, month: number) {
  const first = new Date(Date.UTC(year, month, 1))
  const last = new Date(Date.UTC(year, month + 1, 0))
  const start = addDays(first, -((first.getUTCDay() + 6) % 7))
  const end = addDays(last, (7 - last.getUTCDay()) % 7)
  return { start, end }
}

/**
 * The nursery's pickup calendar: open by default, with whole days or single windows closed. Customers
 * can only book what's open here (once the store is connected to it).
 */
export function PickupPage() {
  const orgId = Number(useParams().orgId)
  const membership = useMembership(orgId)
  const settings = usePickupSettings(orgId)
  const canManage = can(membership, Permissions.PickupManage)

  return (
    <div className="space-y-4">
      <PageHeader title="Pickup" subtitle="When customers can collect orders. Every window is open unless it's closed here." />
      {settings.isPending ? (
        <Loading />
      ) : settings.error ? (
        <ErrorBanner error={settings.error} />
      ) : settings.data.windows.length === 0 ? (
        <Notice>{canManage ? 'No pickup windows yet. Set them up below.' : 'No pickup windows yet. An Owner or Admin can set them up here.'}</Notice>
      ) : (
        <Calendar orgId={orgId} canClose={can(membership, Permissions.OrdersPrepare)} />
      )}
      {canManage && settings.data && <WindowSetup orgId={orgId} settings={settings.data} />}
    </div>
  )
}

function Calendar({ orgId, canClose }: { orgId: number; canClose: boolean }) {
  const [month, setMonth] = useState(() => {
    const now = new Date()
    return { year: now.getFullYear(), month: now.getMonth() }
  })
  const [selected, setSelected] = useState<string | null>(null)
  const { start, end } = monthGrid(month.year, month.month)
  const calendar = usePickupCalendar(orgId, isoDate(start), isoDate(end))
  const title = new Date(Date.UTC(month.year, month.month, 1)).toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' })
  const shift = (by: number) => {
    const d = new Date(Date.UTC(month.year, month.month + by, 1))
    setMonth({ year: d.getUTCFullYear(), month: d.getUTCMonth() })
    setSelected(null)
  }
  const day = calendar.data?.days.find((d) => d.date === selected)

  return (
    <>
      <Card
        title={title}
        actions={
          <div className="flex gap-1">
            <Button variant="ghost" aria-label="Previous month" onClick={() => shift(-1)}>
              ‹
            </Button>
            <Button variant="ghost" aria-label="Next month" onClick={() => shift(1)}>
              ›
            </Button>
          </div>
        }
      >
        {calendar.error && <ErrorBanner error={calendar.error} />}
        <div className="grid grid-cols-7 gap-1 text-center text-xs text-stone-500">
          {weekdays.map((w) => (
            <div key={w}>{w}</div>
          ))}
        </div>
        <div className="mt-1 grid grid-cols-7 gap-1">
          {(calendar.data?.days ?? []).map((d) => {
            const inMonth = Number(d.date.slice(5, 7)) - 1 === month.month
            const isToday = d.date === calendar.data?.today
            return (
              <button
                key={d.date}
                type="button"
                onClick={() => setSelected(d.date)}
                aria-label={`${d.date}${d.closure ? ', closed' : ''}`}
                aria-pressed={selected === d.date}
                className={`flex min-h-14 flex-col items-center gap-1 rounded-lg p-1 text-sm ${inMonth ? '' : 'opacity-40'} ${
                  selected === d.date ? 'ring-2 ring-emerald-600' : isToday ? 'ring-1 ring-stone-400' : ''
                } ${d.closure ? 'bg-red-50 dark:bg-red-950/40' : 'hover:bg-stone-100 dark:hover:bg-stone-800'}`}
              >
                <span className={isToday ? 'font-bold' : ''}>{Number(d.date.slice(8))}</span>
                <span className="flex flex-wrap justify-center gap-0.5">
                  {d.slots.map((s) => (
                    <span key={s.windowId} className={`size-1.5 rounded-full ${statusDots[s.status]}`} />
                  ))}
                </span>
              </button>
            )
          })}
        </div>
        <ul className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-stone-600 dark:text-stone-400">
          {(Object.keys(statusLabels) as PickupSlotStatus[]).map((s) => (
            <li key={s} className="flex items-center gap-1">
              <span className={`size-2 rounded-full ${statusDots[s]}`} /> {statusLabels[s]}
            </li>
          ))}
        </ul>
      </Card>
      {day && calendar.data && <DayPanel orgId={orgId} day={day} today={calendar.data.today} canClose={canClose} />}
    </>
  )
}

function timeRange(slot: PickupSlotDto, timeZone: string | undefined) {
  const format = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', timeZone })
  return `${format(slot.start)} – ${format(slot.end)}`
}

/** One day's windows, to close or reopen the whole day or a single window. */
function DayPanel({ orgId, day, today, canClose }: { orgId: number; day: PickupDayDto; today: string; canClose: boolean }) {
  const { setClosure } = usePickupActions(orgId)
  const [reason, setReason] = useState('')
  const past = day.date < today
  const editable = canClose && !past
  const title = new Date(`${day.date}T00:00:00Z`).toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric', timeZone: 'UTC' })
  const settings = usePickupSettings(orgId)
  const timeZone = settings.data?.timeZone

  const closeDay = (event: FormEvent) => {
    event.preventDefault()
    setClosure.mutate({ date: day.date, closed: true, reason: reason.trim() || undefined }, { onSuccess: () => setReason('') })
  }

  return (
    <Card title={title}>
      <ErrorBanner error={setClosure.error} />
      {day.closure ? (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg bg-red-50 p-3 text-sm dark:bg-red-950/40">
          <span className="flex-1">
            <span className="font-medium">Closed all day</span>
            {day.closure.reason && ` · ${day.closure.reason}`}
            {day.closure.closedBy && <span className="text-stone-600 dark:text-stone-400"> · {day.closure.closedBy}</span>}
          </span>
          {editable && (
            <Button variant="secondary" disabled={setClosure.isPending} onClick={() => setClosure.mutate({ date: day.date, closed: false })}>
              Reopen day
            </Button>
          )}
        </div>
      ) : (
        editable && (
          <form onSubmit={closeDay} className="mb-3 flex flex-wrap items-end gap-2">
            <div className="min-w-40 flex-1">
              <Field label="Reason (optional)">
                <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. Out of town" maxLength={200} />
              </Field>
            </div>
            <Button type="submit" variant="danger" disabled={setClosure.isPending}>
              Close whole day
            </Button>
          </form>
        )
      )}
      <ul className="divide-y divide-stone-200 dark:divide-stone-800">
        {day.slots.map((slot) => (
          <li key={slot.windowId} className="flex flex-wrap items-center gap-2 py-2">
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium">{slot.name}</p>
              <p className="text-xs text-stone-600 dark:text-stone-400">
                {timeRange(slot, timeZone)}
                {slot.closure?.reason && ` · ${slot.closure.reason}`}
                {slot.closure?.closedBy && ` · closed by ${slot.closure.closedBy}`}
              </p>
            </div>
            <Badge tone={slot.status === 'open' ? 'green' : slot.status === 'closed' ? 'red' : 'neutral'}>{statusLabels[slot.status]}</Badge>
            {editable && !day.closure && (
              <Button
                variant={slot.closure ? 'secondary' : 'ghost'}
                disabled={setClosure.isPending}
                onClick={() => setClosure.mutate({ date: day.date, windowId: slot.windowId, closed: !slot.closure })}
              >
                {slot.closure ? 'Reopen' : 'Close'}
              </Button>
            )}
          </li>
        ))}
      </ul>
      {past && <p className="mt-2 text-xs text-stone-500">This day has passed.</p>}
    </Card>
  )
}

const hourLabel = (h: number) => `${h % 12 === 0 ? 12 : h % 12} ${h < 12 ? 'AM' : 'PM'}`

/** Owners and Admins: the windows (made from a length and a first start, then renamed), lead time, and booking range. */
function WindowSetup({ orgId, settings }: { orgId: number; settings: PickupSettingsDto }) {
  const { setUpWindows, setLimits } = usePickupActions(orgId)
  const [startHour, setStartHour] = useState(8)
  const [hours, setHours] = useState(1)
  const count = Math.ceil(24 / hours)
  const [lead, setLead] = useState(String(settings.leadHours))
  const [days, setDays] = useState(String(settings.bookingDays))

  const createWindows = (event: FormEvent) => {
    event.preventDefault()
    if (
      settings.windows.length > 0 &&
      !window.confirm('Replace all pickup windows? Their names are reset, and windows closed on single days reopen. Whole days stay closed.')
    )
      return
    setUpWindows.mutate({ startHour, hours })
  }

  const saveLimits = (event: FormEvent) => {
    event.preventDefault()
    setLimits.mutate({ leadHours: Number(lead), bookingDays: Number(days) })
  }

  return (
    <Card title="Pickup windows">
      <p className="mb-3 text-sm text-stone-600 dark:text-stone-400">The same windows every day, in {settings.timeZone} time. Close single days or windows on the calendar above.</p>
      {settings.windows.length > 0 && (
        <ul className="mb-4 space-y-2">
          {settings.windows.map((w) => (
            <WindowName key={`${w.id}-${w.name}`} orgId={orgId} window={w} />
          ))}
        </ul>
      )}

      <form onSubmit={createWindows} className="flex flex-wrap items-end gap-2 border-t border-stone-200 pt-4 dark:border-stone-800">
        <div className="w-36">
          <Field label="Each window is">
            <Select value={hours} onChange={(e) => setHours(Number(e.target.value))}>
              {Array.from({ length: 24 }, (_, i) => i + 1).map((h) => (
                <option key={h} value={h}>
                  {h} hour{h === 1 ? '' : 's'}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <div className="w-36">
          <Field label="First one starts">
            <Select value={startHour} onChange={(e) => setStartHour(Number(e.target.value))}>
              {Array.from({ length: 24 }, (_, h) => (
                <option key={h} value={h}>
                  {hourLabel(h)}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <Button type="submit" variant={settings.windows.length ? 'secondary' : 'primary'} disabled={setUpWindows.isPending}>
          {settings.windows.length ? 'Replace' : 'Create'} {count} window{count === 1 ? '' : 's'}
        </Button>
      </form>
      <p className="mt-1 text-xs text-stone-500">
        Windows run round the clock from the first one{24 % hours ? '; the last one is shorter' : ''}. Rename them after.
      </p>
      <ErrorBanner error={setUpWindows.error} />

      <form onSubmit={saveLimits} className="mt-4 flex flex-wrap items-end gap-2 border-t border-stone-200 pt-4 dark:border-stone-800">
        <div className="w-44">
          <Field label="Book at least (hours ahead)">
            <Input type="number" min={0} max={168} value={lead} onChange={(e) => setLead(e.target.value)} />
          </Field>
        </div>
        <div className="w-44">
          <Field label="Book up to (days ahead)">
            <Input type="number" min={1} max={90} value={days} onChange={(e) => setDays(e.target.value)} />
          </Field>
        </div>
        <Button type="submit" variant="secondary" disabled={setLimits.isPending}>
          Save
        </Button>
      </form>
      <ErrorBanner error={setLimits.error} />
    </Card>
  )
}

function WindowName({ orgId, window: w }: { orgId: number; window: PickupWindowDto }) {
  const { renameWindow } = usePickupActions(orgId)
  const [name, setName] = useState(w.name)
  const save = () => {
    if (name.trim() && name.trim() !== w.name) renameWindow.mutate({ windowId: w.id, name: name.trim() })
  }
  const hours = w.lengthMinutes / 60
  return (
    <li>
      <div className="flex items-center gap-2">
        <span className="w-28 shrink-0 text-xs text-stone-600 dark:text-stone-400">
          {hourLabel(Math.floor(w.startMinute / 60))} · {hours} h
        </span>
        <Input aria-label="Window name" value={name} maxLength={60} onChange={(e) => setName(e.target.value)} onBlur={save} onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()} />
      </div>
      <ErrorBanner error={renameWindow.error} />
    </li>
  )
}
