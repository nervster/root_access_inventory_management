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

// ── Inventory (backend/internal/inventory) ──────────────────────────────────

export interface PotSizeDto {
  id: number
  name: string
  diameterInches: number | null
  heightInches: number | null
  sortOrder: number
  sellable: boolean
  description: string | null
}

export interface DashboardDto {
  plants: { growing: number; forSale: number; sold: number; discarded: number }
  last30Days: { received: number; discarded: number }
  /** The prices of everything for sale right now. */
  forSaleValueCents: number
  /** Growing, in a sellable pot, priced: could be marked for sale now. */
  readyToMark: number
  /** Growing, in a sellable pot, with no price for its plant and pot size yet. */
  needsPrice: number
  /** Smallest first: the growth pipeline. */
  potSizes: { id: number; name: string; sellable: boolean; growing: number; forSale: number }[]
}

export type PlantStatus = 'growing' | 'for_sale' | 'sold' | 'discarded'
export const PLANT_STATUS_LABELS: Record<PlantStatus, string> = { growing: 'Growing', for_sale: 'For sale', sold: 'Sold', discarded: 'Discarded' }

export interface PlantNameDto {
  id: number
  rank: 'family' | 'genus' | 'species' | 'cultivar'
  /** Alocasia baginda 'Dragon Scale' */
  fullName: string
  /** E.g. the trade name it's sold under. */
  notes: string | null
  shared: boolean
}

export interface VariegationDto {
  id: number
  name: string
  notes: string | null
  shared: boolean
}

export interface BatchDto {
  id: number
  /** B-0007 */
  code: string
  plantNameId: number
  plantName: string
  variegationId: number | null
  variegation: string | null
  source: string | null
  /** 2026-10-06 */
  receivedOn: string
  /** Only present for roles that see financials. */
  unitCostCents?: number | null
  notes: string | null
  createdAt: string
  plantCounts: { growing: number; forSale: number; sold: number; discarded: number }
}

export interface PlantDto {
  id: number
  /** RAH-000123 */
  barcode: string
  batchId: number
  batchCode: string
  plantNameId: number
  plantName: string
  variegationId: number | null
  variegation: string | null
  potSizeId: number
  potSize: string
  status: PlantStatus
  notes: string | null
  createdAt: string
  discardedAt: string | null
  discardReason: string | null
}

export interface BatchWithPlantsDto {
  batch: BatchDto
  plants: PlantDto[]
}

/** A SKU created along the way: plants of this kind went on sale in this pot for the first time. */
export interface NewVariantDto {
  product: string
  potSize: string
  sku: string
}

export interface RepotResult {
  moved: number
  /** Already in that pot. */
  unchanged: number
  /** For-sale plants moved into a pot they weren't sold in before get a SKU there. */
  newVariants: NewVariantDto[]
}

export interface SaleResult {
  marked: number
  alreadyForSale: number
  skipped: { unsellablePot: number }
  /** SKUs created for these plants; they have no price yet. */
  newVariants: NewVariantDto[]
}

export interface PotChangeDto {
  changedAt: string
  /** null for the plant's first pot. */
  fromPotSize: string | null
  toPotSize: string
  /** Email of who moved it. */
  changedBy: string | null
}

export interface PlantWithHistoryDto extends PlantDto {
  /** Oldest first. */
  potHistory: PotChangeDto[]
}

export interface VariantDto {
  id: number
  potSizeId: number
  potSize: string
  sku: string
  /** null until someone sets it; unpriced SKUs stay off sales channels. */
  priceCents: number | null
  /** Plants for sale right now. */
  stock: number
}

export interface ProductDto {
  id: number
  plantNameId: number
  plantName: string
  variegationId: number | null
  variegation: string | null
  title: string
  description: string | null
  /** E.g. Houseplant. */
  productType: string | null
  /** E.g. Aroid, Rare: for Shopify collections and search. */
  tags: string[]
  createdAt: string
  /** Smallest pot first. */
  variants: VariantDto[]
}

export interface PhotoDto {
  id: number
  /** A signed link, valid for an hour. */
  viewUrl: string
  takenAt: string
  caption: string | null
  /** Email of who added it. */
  uploadedBy: string | null
}
