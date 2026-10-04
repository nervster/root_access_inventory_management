export function formatDate(value: string) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(value))
}

/** IANA time zones the browser knows, for time zone pickers. */
export const TIME_ZONES = Intl.supportedValuesOf('timeZone')
