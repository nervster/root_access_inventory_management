import { UserButton } from '@clerk/react'
import { Link, NavLink, Outlet, useMatch, useNavigate } from 'react-router'
import { useMe } from '../api/hooks'
import { can } from '../api/roles'
import { Permissions } from '../api/types'
import { ErrorBanner } from './ui'

function Tab({ to, end, children }: { to: string; end?: boolean; children: string }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        `whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium ${
          isActive
            ? 'bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'
            : 'text-stone-600 hover:bg-stone-100 dark:text-stone-400 dark:hover:bg-stone-800'
        }`
      }
    >
      {children}
    </NavLink>
  )
}

export function Layout() {
  const me = useMe()
  const navigate = useNavigate()
  const orgMatch = useMatch('/orgs/:orgId/*')
  const inPlatform = useMatch('/platform/*') !== null
  const orgId = orgMatch ? Number(orgMatch.params.orgId) : undefined
  const memberships = me.data?.memberships ?? []
  const membership = memberships.find((m) => m.organizationId === orgId)

  return (
    <div className="min-h-dvh">
      <header className="border-b border-stone-200 bg-white dark:border-stone-800 dark:bg-stone-900">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 py-3">
          <Link to="/" className="text-lg font-bold tracking-tight text-emerald-800 dark:text-emerald-400">
            NMS
          </Link>
          {memberships.length > 1 && (
            <select
              aria-label="Switch nursery"
              className="min-h-9 max-w-[45vw] truncate rounded-lg border border-stone-300 bg-white px-2 text-sm dark:border-stone-700 dark:bg-stone-950"
              value={orgId ?? ''}
              onChange={(e) => navigate(`/orgs/${e.target.value}`)}
            >
              {orgId === undefined && <option value="">Choose a nursery…</option>}
              {memberships.map((m) => (
                <option key={m.organizationId} value={m.organizationId}>
                  {m.organizationName}
                </option>
              ))}
            </select>
          )}
          {memberships.length === 1 && orgId === undefined && (
            <Link to={`/orgs/${memberships[0].organizationId}`} className="truncate text-sm text-stone-600 dark:text-stone-400">
              {memberships[0].organizationName}
            </Link>
          )}
          <div className="ml-auto flex items-center gap-2">
            {me.data?.isPlatformAdmin && (
              <Link
                to="/platform"
                className={`rounded-lg px-3 py-2 text-sm font-medium ${inPlatform ? 'bg-stone-200 dark:bg-stone-800' : 'hover:bg-stone-100 dark:hover:bg-stone-800'}`}
              >
                Platform
              </Link>
            )}
            <UserButton />
          </div>
        </div>
        {(membership || inPlatform) && (
          <nav className="mx-auto flex max-w-5xl gap-1 overflow-x-auto px-4 pb-2">
            {membership && (
              <>
                <Tab to={`/orgs/${orgId}`} end>
                  Overview
                </Tab>
                <Tab to={`/orgs/${orgId}/team`}>Team</Tab>
                {can(membership, Permissions.OrganizationManage) && <Tab to={`/orgs/${orgId}/settings`}>Settings</Tab>}
              </>
            )}
            {inPlatform && (
              <>
                <Tab to="/platform" end>
                  Nurseries
                </Tab>
                <Tab to="/platform/users">Users</Tab>
              </>
            )}
          </nav>
        )}
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6">
        <ErrorBanner error={me.error} />
        <Outlet />
      </main>
    </div>
  )
}
