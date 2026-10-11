import { useAuth } from '@clerk/react'
import { useCallback } from 'react'

/** An API error, carrying the message the backend returns as {"error": "..."}. */
export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

/** Returns a fetch for /api/* that sends the Clerk session token and throws ApiError on failure. */
export function useApi() {
  const { getToken } = useAuth()

  return useCallback(
    async <T>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> => {
      const { json, headers, ...rest } = init
      const token = await getToken()
      const response = await fetch(`/api${path}`, {
        ...rest,
        headers: {
          ...(json !== undefined ? { 'Content-Type': 'application/json' } : {}),
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
          ...headers,
        },
        body: json !== undefined ? JSON.stringify(json) : rest.body,
      })

      if (!response.ok) {
        const body: { error?: string } = await response.json().catch(() => ({}))
        throw new ApiError(response.status, body.error ?? `Request failed (${response.status})`)
      }
      // No body (204 No Content, 202 Accepted, …): nothing to parse.
      const text = await response.text()
      return (text ? JSON.parse(text) : undefined) as T
    },
    [getToken],
  )
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return 'Something went wrong.'
}
