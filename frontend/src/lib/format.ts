export function formatDate(value: string) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(value))
}

/** IANA time zones the browser knows, for time zone pickers. */
export const TIME_ZONES = Intl.supportedValuesOf('timeZone')

const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' })

/** 4500 → "$45.00". */
export function formatMoney(cents: number) {
  return money.format(cents / 100)
}

/** 1284 → "1,284"; 12900 → "12.9K". */
export function formatCount(value: number) {
  return new Intl.NumberFormat('en-US', { notation: value >= 10_000 ? 'compact' : 'standard', maximumFractionDigits: 1 }).format(value)
}

/**
 * A calendar date from the API ("2026-10-06") → "Oct 6, 2026". Parsed as a local date: new Date("2026-10-06")
 * would mean midnight UTC, which is still Oct 5 in the Americas.
 */
export function formatDay(day: string) {
  const [year, month, date] = day.split('-').map(Number)
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(year, month - 1, date))
}
