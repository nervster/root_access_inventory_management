import { ClerkLoading, RedirectToSignIn, Show } from '@clerk/react'
import { Link, Outlet, useParams } from 'react-router'
import { useMe } from '../api/hooks'
import { Card, Loading } from './ui'

export function RequireSignIn() {
  return (
    <>
      <ClerkLoading>
        <Loading />
      </ClerkLoading>
      <Show when="signed-in">
        <Outlet />
      </Show>
      <Show when="signed-out">
        <RedirectToSignIn />
      </Show>
    </>
  )
}

/** Nursery routes: only for members of an active nursery. The API enforces this too; this just explains it. */
export function RequireMembership() {
  const orgId = Number(useParams().orgId)
  const me = useMe()
  if (me.isPending) return <Loading />

  const membership = me.data?.memberships.find((m) => m.organizationId === orgId)
  if (!membership) return <NotFound message="You're not a member of this nursery." />
  if (membership.status === 'suspended')
    return <NotFound message={`${membership.organizationName} is suspended. Contact NMS support.`} />
  return <Outlet />
}

export function RequirePlatformAdmin() {
  const me = useMe()
  if (me.isPending) return <Loading />
  if (!me.data?.isPlatformAdmin) return <NotFound />
  return <Outlet />
}

export function NotFound({ message = "This page doesn't exist." }: { message?: string }) {
  return (
    <Card>
      <p className="text-sm">{message}</p>
      <Link to="/" className="mt-2 inline-block text-sm font-medium text-emerald-800 underline dark:text-emerald-400">
        Go home
      </Link>
    </Card>
  )
}
