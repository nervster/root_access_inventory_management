import { useMutation, useQuery, useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useApi } from './client'
import type {
  CreateOrganizationResponse,
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
