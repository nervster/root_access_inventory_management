import { useMutation, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useCallback } from 'react'
import { useApi } from './client'
import type {
  CreateOrganizationResponse,
  DashboardDto,
  InvitationDto,
  MeResponse,
  MemberDto,
  MembershipDto,
  OrganizationDto,
  OrganizationStatus,
  OrgRole,
  PlatformOrganizationDetailDto,
  PlatformOrganizationDto,
  PlatformUserDto,
  PotSizeDto,
  BatchDto,
  BatchWithPlantsDto,
  PlantNameDto,
  PlantWithHistoryDto,
  RepotResult,
  SaleResult,
  VariegationDto,
} from './types'

export const queryKeys = {
  me: ['me'] as const,
  organization: (orgId: number) => ['orgs', orgId] as const,
  members: (orgId: number) => ['orgs', orgId, 'members'] as const,
  invitations: (orgId: number) => ['orgs', orgId, 'invitations'] as const,
  platformOrganizations: ['platform', 'orgs'] as const,
  platformOrganization: (id: number) => ['platform', 'orgs', id] as const,
  platformUsers: (email: string) => ['platform', 'users', email] as const,
}

// ── Signed-in user ──────────────────────────────────────────────────────────

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.me, queryFn: () => api<MeResponse>('/me') })
}

/** The signed-in user's membership in an organization, if any. */
export function useMembership(orgId: number): MembershipDto | undefined {
  return useMe().data?.memberships.find((m) => m.organizationId === orgId)
}

export function useRespondToInvitation() {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, accept }: { id: number; accept: boolean }) =>
      api<MembershipDto | undefined>(`/me/invitations/${id}/${accept ? 'accept' : 'decline'}`, { method: 'POST' }),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.me }),
  })
}

// ── Organization (tenant) ───────────────────────────────────────────────────

export function useOrganization(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.organization(orgId), queryFn: () => api<OrganizationDto>(`/orgs/${orgId}`) })
}

export function useUpdateOrganization(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name?: string; timeZone?: string }) =>
      api<OrganizationDto>(`/orgs/${orgId}`, { method: 'PATCH', json: body }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: queryKeys.organization(orgId) })
      client.invalidateQueries({ queryKey: queryKeys.me })
    },
  })
}

export function useMembers(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: queryKeys.members(orgId), queryFn: () => api<MemberDto[]>(`/orgs/${orgId}/members`) })
}

export function useInvitations(orgId: number, enabled: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.invitations(orgId),
    queryFn: () => api<InvitationDto[]>(`/orgs/${orgId}/invitations`),
    enabled,
  })
}

/**
 * Team actions for an organization. `base` is the API path that owns the team:
 * `/orgs/{id}` for members acting in their nursery, `/platform/organizations/{id}` for platform admins.
 */
export function useTeamActions(base: string, invalidate: readonly QueryKey[]) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () => Promise.all(invalidate.map((queryKey) => client.invalidateQueries({ queryKey })))

  return {
    invite: useMutation({
      mutationFn: (body: { email: string; role: OrgRole }) =>
        api<InvitationDto>(`${base}/invitations`, { method: 'POST', json: body }),
      onSettled: onSuccess, // a 502 still saved the invite
    }),
    resend: useMutation({
      mutationFn: (id: number) => api<InvitationDto>(`${base}/invitations/${id}/resend`, { method: 'POST' }),
      onSuccess,
    }),
    revoke: useMutation({
      mutationFn: (id: number) => api<void>(`${base}/invitations/${id}`, { method: 'DELETE' }),
      onSuccess,
    }),
    changeRole: useMutation({
      mutationFn: ({ id, role }: { id: number; role: OrgRole }) =>
        api<MemberDto>(`${base}/members/${id}`, { method: 'PATCH', json: { role } }),
      onSuccess,
    }),
    remove: useMutation({
      mutationFn: (id: number) => api<void>(`${base}/members/${id}`, { method: 'DELETE' }),
      onSuccess,
    }),
  }
}

// ── Platform admin ──────────────────────────────────────────────────────────

export function usePlatformOrganizations() {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.platformOrganizations,
    queryFn: () => api<PlatformOrganizationDto[]>('/platform/organizations'),
  })
}

export function useCreateOrganization() {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string; slug?: string; timeZone?: string; ownerEmail: string }) =>
      api<CreateOrganizationResponse>('/platform/organizations', { method: 'POST', json: body }),
    onSuccess: () => client.invalidateQueries({ queryKey: queryKeys.platformOrganizations }),
  })
}

export function usePlatformOrganization(id: number) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.platformOrganization(id),
    queryFn: () => api<PlatformOrganizationDetailDto>(`/platform/organizations/${id}`),
  })
}

export function useUpdatePlatformOrganization(id: number) {
  const api = useApi()
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name?: string; status?: OrganizationStatus }) =>
      api<PlatformOrganizationDto>(`/platform/organizations/${id}`, { method: 'PATCH', json: body }),
    onSuccess: () => client.invalidateQueries({ queryKey: ['platform', 'orgs'] }),
  })
}

export function usePlatformUsers(email: string) {
  const api = useApi()
  return useQuery({
    queryKey: queryKeys.platformUsers(email),
    queryFn: () => api<PlatformUserDto[]>(`/platform/users?email=${encodeURIComponent(email)}`),
  })
}

// ── Inventory ───────────────────────────────────────────────────────────────

export const inventoryKeys = {
  /** Everything inventory-related in a nursery: invalidate this after any plant changes. */
  all: (orgId: number) => ['orgs', orgId, 'inventory'] as const,
  dashboard: (orgId: number) => ['orgs', orgId, 'inventory', 'dashboard'] as const,
  potSizes: (orgId: number) => ['orgs', orgId, 'inventory', 'pot-sizes'] as const,
  batches: (orgId: number) => ['orgs', orgId, 'inventory', 'batches'] as const,
  batch: (orgId: number, batchId: number) => ['orgs', orgId, 'inventory', 'batches', batchId] as const,
  plantNames: (orgId: number, search: string) => ['orgs', orgId, 'plant-names', search] as const,
  variegations: (orgId: number) => ['orgs', orgId, 'variegations'] as const,
  plant: (orgId: number, barcode: string) => ['orgs', orgId, 'inventory', 'plants', barcode] as const,
}

export function useDashboard(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.dashboard(orgId), queryFn: () => api<DashboardDto>(`/orgs/${orgId}/dashboard`) })
}

export function usePotSizes(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.potSizes(orgId), queryFn: () => api<PotSizeDto[]>(`/orgs/${orgId}/pot-sizes`) })
}

export interface PotSizeInput {
  name?: string
  diameterInches?: number
  heightInches?: number
  sellable?: boolean
  description?: string
}

export function usePotSizeActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () =>
    Promise.all([
      client.invalidateQueries({ queryKey: inventoryKeys.potSizes(orgId) }),
      client.invalidateQueries({ queryKey: inventoryKeys.dashboard(orgId) }),
    ])
  return {
    create: useMutation({
      mutationFn: (body: PotSizeInput) => api<PotSizeDto>(`/orgs/${orgId}/pot-sizes`, { method: 'POST', json: body }),
      onSuccess,
    }),
    update: useMutation({
      mutationFn: ({ id, ...body }: PotSizeInput & { id: number }) =>
        api<PotSizeDto>(`/orgs/${orgId}/pot-sizes/${id}`, { method: 'PATCH', json: body }),
      onSuccess,
    }),
    reorder: useMutation({
      mutationFn: (ids: number[]) => api<PotSizeDto[]>(`/orgs/${orgId}/pot-sizes/order`, { method: 'PUT', json: { ids } }),
      onSuccess,
    }),
    remove: useMutation({
      mutationFn: (id: number) => api<void>(`/orgs/${orgId}/pot-sizes/${id}`, { method: 'DELETE' }),
      onSuccess,
    }),
  }
}

export function useBatches(orgId: number) {
  const api = useApi()
  return useQuery({ queryKey: inventoryKeys.batches(orgId), queryFn: () => api<BatchDto[]>(`/orgs/${orgId}/batches`) })
}

export function useBatch(orgId: number, batchId: number) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.batch(orgId, batchId),
    queryFn: () => api<BatchWithPlantsDto>(`/orgs/${orgId}/batches/${batchId}`),
  })
}

/** Plant names containing search (2+ characters), from the shared list and the nursery's own. */
export function usePlantNames(orgId: number, search: string) {
  const api = useApi()
  const term = search.trim()
  return useQuery({
    queryKey: inventoryKeys.plantNames(orgId, term),
    queryFn: () => api<PlantNameDto[]>(`/orgs/${orgId}/plant-names?search=${encodeURIComponent(term)}`),
    enabled: term.length >= 2,
    staleTime: 5 * 60_000,
  })
}

export function useVariegations(orgId: number) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.variegations(orgId),
    queryFn: () => api<VariegationDto[]>(`/orgs/${orgId}/variegations`),
    staleTime: 5 * 60_000,
  })
}

export interface ReceiveBatchInput {
  plantNameId: number
  variegationId?: number
  potSizeId: number
  quantity: number
  receivedOn: string
  source?: string
  notes?: string
  unitCostCents?: number
}

/** Receiving batches, repotting, and marking for sale: each refreshes everything inventory shows. */
export function usePlantActions(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  const onSuccess = () => client.invalidateQueries({ queryKey: inventoryKeys.all(orgId) })
  return {
    receiveBatch: useMutation({
      mutationFn: (body: ReceiveBatchInput) => api<BatchWithPlantsDto>(`/orgs/${orgId}/batches`, { method: 'POST', json: body }),
      onSuccess,
    }),
    repot: useMutation({
      mutationFn: (body: { potSizeId: number; batchId?: number; barcodes?: string[] }) =>
        api<RepotResult>(`/orgs/${orgId}/repot`, { method: 'POST', json: body }),
      onSuccess,
    }),
    markForSale: useMutation({
      mutationFn: (body: { batchId?: number; barcodes?: string[] }) =>
        api<SaleResult>(`/orgs/${orgId}/plants/mark-for-sale`, { method: 'POST', json: body }),
      onSuccess,
    }),
    unmarkForSale: useMutation({
      mutationFn: (barcodes: string[]) =>
        api<{ unmarked: number }>(`/orgs/${orgId}/plants/unmark-for-sale`, { method: 'POST', json: { barcodes } }),
      onSuccess,
    }),
    discard: useMutation({
      mutationFn: (body: { barcodes: string[]; reason?: string }) =>
        api<{ discarded: number }>(`/orgs/${orgId}/plants/discard`, { method: 'POST', json: body }),
      onSuccess,
    }),
  }
}

/** Looks up a plant by its barcode (for the plant page). */
export function usePlant(orgId: number, barcode: string) {
  const api = useApi()
  return useQuery({
    queryKey: inventoryKeys.plant(orgId, barcode),
    queryFn: () => api<PlantWithHistoryDto>(`/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}`),
  })
}

/** Returns a function that looks up a plant by barcode, using the cache when possible (for scanning). */
export function useLookupPlant(orgId: number) {
  const api = useApi()
  const client = useQueryClient()
  return useCallback(
    (barcode: string) =>
      client.fetchQuery({
        queryKey: inventoryKeys.plant(orgId, barcode),
        queryFn: () => api<PlantWithHistoryDto>(`/orgs/${orgId}/plants/by-barcode/${encodeURIComponent(barcode)}`),
      }),
    [api, client, orgId],
  )
}
