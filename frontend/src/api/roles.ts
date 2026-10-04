import type { MembershipDto, OrgRole, Permission } from './types'
import { ORG_ROLES } from './types'

/** Mirrors CanManageRole in permissions.go: Owners manage every role; Admins manage Staff and Viewers. */
export function canManageRole(actor: OrgRole, target: OrgRole): boolean {
  if (actor === 'owner') return true
  if (actor === 'admin') return target === 'staff' || target === 'viewer'
  return false
}

export function manageableRoles(actor: OrgRole): OrgRole[] {
  return ORG_ROLES.filter((role) => canManageRole(actor, role))
}

export function can(membership: MembershipDto | undefined, permission: Permission): boolean {
  return membership?.permissions.includes(permission) ?? false
}
