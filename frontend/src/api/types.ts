// Mirrors the JSON of the Go API (backend/internal/organization/*_handler.go).

export type OrgRole = 'owner' | 'admin' | 'staff' | 'viewer'
export type OrganizationStatus = 'active' | 'suspended'

export const ORG_ROLES: OrgRole[] = ['owner', 'admin', 'staff', 'viewer']

/** How roles and statuses are shown; the API sends them lower-case. */
export const ROLE_LABELS: Record<OrgRole, string> = { owner: 'Owner', admin: 'Admin', staff: 'Staff', viewer: 'Viewer' }
export const STATUS_LABELS: Record<OrganizationStatus, string> = { active: 'Active', suspended: 'Suspended' }

/** Permission names from backend/internal/organization/permissions.go. */
export const Permissions = {
  OrganizationView: 'organization.view',
  OrganizationManage: 'organization.manage',
  IntegrationsManage: 'integrations.manage',
  TeamView: 'team.view',
  TeamManage: 'team.manage',
  RolesManage: 'roles.manage',
  InventoryView: 'inventory.view',
  InventoryEdit: 'inventory.edit',
  ListingsManage: 'listings.manage',
  FinancialsView: 'financials.view',
} as const
export type Permission = (typeof Permissions)[keyof typeof Permissions]

export interface MembershipDto {
  organizationId: number
  organizationName: string
  slug: string
  role: OrgRole
  status: OrganizationStatus
  permissions: Permission[]
}

export interface PendingInvitationDto {
  id: number
  organizationId: number
  organizationName: string
  role: OrgRole
  expiresAt: string
}

export interface MeResponse {
  id: number
  email: string
  displayName: string | null
  isPlatformAdmin: boolean
  memberships: MembershipDto[]
  pendingInvitations: PendingInvitationDto[]
}

export interface OrganizationDto {
  id: number
  name: string
  slug: string
  timeZone: string
  status: OrganizationStatus
}

export interface MemberDto {
  id: number
  userId: number
  email: string
  displayName: string | null
  role: OrgRole
  joinedAt: string
}

export interface InvitationDto {
  id: number
  email: string
  role: OrgRole
  createdAt: string
  expiresAt: string
  expired: boolean
}

export interface PlatformOrganizationDto {
  id: number
  name: string
  slug: string
  timeZone: string
  status: OrganizationStatus
  memberCount: number
  createdAt: string
}

export interface CreateOrganizationResponse {
  organization: PlatformOrganizationDto
  ownerInvitation: InvitationDto
  deliveryError: string | null
}

export interface PlatformOrganizationDetailDto {
  organization: PlatformOrganizationDto
  members: MemberDto[]
  pendingInvitations: InvitationDto[]
}

export interface PlatformUserDto {
  id: number
  email: string
  displayName: string | null
  isPlatformAdmin: boolean
  createdAt: string
  memberships: { organizationId: number; organizationName: string; role: OrgRole }[]
}
