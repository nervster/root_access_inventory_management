import type { ComponentType, SVGProps } from 'react'
import { NavLink } from 'react-router'
import { can } from '../api/roles'
import { Permissions, type MembershipDto, type Permission } from '../api/types'
import { BatchesIcon, DashboardIcon, MoreIcon, PotIcon, ScanIcon, SettingsIcon, TeamIcon } from './icons'

interface NavItem {
  label: string
  /** Path inside the nursery: "" is the dashboard. */
  path: string
  icon: ComponentType<SVGProps<SVGSVGElement>>
  /** Shown only to members whose role has this permission. */
  permission: Permission
  /** The phone bar's highlighted middle button. */
  primary?: boolean
}

/** The nursery's pages, in order. Phones show the first four in the bottom bar and the rest under More. */
const items: NavItem[] = [
  { label: 'Dashboard', path: '', icon: DashboardIcon, permission: Permissions.InventoryView },
  { label: 'Batches', path: 'batches', icon: BatchesIcon, permission: Permissions.InventoryView },
  { label: 'Scan', path: 'scan', icon: ScanIcon, permission: Permissions.InventoryView, primary: true },
  { label: 'Pot sizes', path: 'pot-sizes', icon: PotIcon, permission: Permissions.InventoryView },
  { label: 'Team', path: 'team', icon: TeamIcon, permission: Permissions.TeamView },
  { label: 'Settings', path: 'settings', icon: SettingsIcon, permission: Permissions.OrganizationManage },
]

const maxBottomBarItems = 5

function href(orgId: number, item: NavItem) {
  return item.path ? `/orgs/${orgId}/${item.path}` : `/orgs/${orgId}`
}

/** Tabs under the header on wider screens. */
export function NurseryTabs({ membership }: { membership: MembershipDto }) {
  const visible = items.filter((item) => can(membership, item.permission))
  return (
    <nav aria-label="Nursery" className="mx-auto hidden max-w-5xl gap-1 overflow-x-auto px-4 pb-2 sm:flex">
      {visible.map((item) => (
        <NavLink
          key={item.label}
          to={href(membership.organizationId, item)}
          end={item.path === ''}
          className={({ isActive }) =>
            `whitespace-nowrap rounded-lg px-3 py-2 text-sm font-medium ${
              isActive
                ? 'bg-emerald-100 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'
                : 'text-stone-600 hover:bg-stone-100 dark:text-stone-400 dark:hover:bg-stone-800'
            }`
          }
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  )
}

/** A bar fixed to the bottom of the screen on phones, within thumb reach. */
export function NurseryBottomBar({ membership }: { membership: MembershipDto }) {
  const visible = items.filter((item) => can(membership, item.permission))
  const overflow = visible.length > maxBottomBarItems
  const inBar = overflow ? visible.slice(0, maxBottomBarItems - 1) : visible
  const inMore = overflow ? visible.slice(maxBottomBarItems - 1) : []

  const itemClass = ({ isActive }: { isActive: boolean }) =>
    `flex min-h-14 flex-1 flex-col items-center justify-center gap-0.5 text-[11px] font-medium ${
      isActive ? 'text-emerald-800 dark:text-emerald-400' : 'text-stone-500 dark:text-stone-400'
    }`

  return (
    <nav
      aria-label="Nursery"
      className="fixed inset-x-0 bottom-0 z-10 flex border-t border-stone-200 bg-white pb-[env(safe-area-inset-bottom)] sm:hidden dark:border-stone-800 dark:bg-stone-900"
    >
      {inBar.map((item) =>
        item.primary ? (
          <NavLink key={item.label} to={href(membership.organizationId, item)} className={itemClass}>
            <span className="-mt-5 flex size-12 items-center justify-center rounded-full bg-emerald-700 text-white shadow-md ring-4 ring-white dark:ring-stone-900">
              <item.icon className="size-6" />
            </span>
            {item.label}
          </NavLink>
        ) : (
          <NavLink key={item.label} to={href(membership.organizationId, item)} end={item.path === ''} className={itemClass}>
            <item.icon className="size-6" />
            {item.label}
          </NavLink>
        ),
      )}
      {inMore.length > 0 && (
        <details className="relative flex flex-1">
          <summary className="flex min-h-14 flex-1 cursor-pointer list-none flex-col items-center justify-center gap-0.5 text-[11px] font-medium text-stone-500 dark:text-stone-400">
            <MoreIcon className="size-6" />
            More
          </summary>
          <div className="absolute right-2 bottom-16 w-44 rounded-xl border border-stone-200 bg-white p-1 shadow-lg dark:border-stone-800 dark:bg-stone-900">
            {inMore.map((item) => (
              <NavLink
                key={item.label}
                to={href(membership.organizationId, item)}
                className="flex items-center gap-2 rounded-lg px-3 py-2.5 text-sm hover:bg-stone-100 dark:hover:bg-stone-800"
              >
                <item.icon className="size-5" />
                {item.label}
              </NavLink>
            ))}
          </div>
        </details>
      )}
    </nav>
  )
}
