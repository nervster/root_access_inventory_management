import { ClerkProvider, useAuth } from '@clerk/react'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { ApiError } from '../api/client'

const publishableKey = import.meta.env.VITE_CLERK_PUBLISHABLE_KEY
if (!publishableKey) throw new Error('Set VITE_CLERK_PUBLISHABLE_KEY in frontend/.env.development.local (Clerk dashboard → API Keys)')

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      // Don't retry 4xx responses (not found, forbidden, …); they won't change on retry.
      retry: (failureCount, error) => !(error instanceof ApiError && error.status < 500) && failureCount < 2,
    },
  },
})

/** Clerk (using React Router for navigation) and TanStack Query. Must render inside the router. */
export function Providers({ children }: { children: ReactNode }) {
  const navigate = useNavigate()
  return (
    <ClerkProvider
      publishableKey={publishableKey}
      routerPush={(to) => navigate(to)}
      routerReplace={(to) => navigate(to, { replace: true })}
      signInUrl="/sign-in"
      signUpUrl="/sign-up"
      signInFallbackRedirectUrl="/"
      signUpFallbackRedirectUrl="/"
      afterSignOutUrl="/sign-in"
    >
      <QueryClientProvider client={queryClient}>
        <ClearCacheOnUserChange />
        {children}
      </QueryClientProvider>
    </ClerkProvider>
  )
}

/** Drops cached API data when a different user signs in (or signs out), so nobody sees the previous user's data. */
function ClearCacheOnUserChange() {
  const { isLoaded, userId } = useAuth()
  const client = useQueryClient()
  const previous = useRef<string | null | undefined>(undefined)

  useEffect(() => {
    if (!isLoaded) return
    if (previous.current !== undefined && previous.current !== userId) client.clear()
    previous.current = userId ?? null
  }, [isLoaded, userId, client])

  return null
}
