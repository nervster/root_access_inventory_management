package organization

import (
	"slices"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Permission is something a member may do inside an organization. Routes check permissions, never
// role names, so roles can later be customized per organization without touching routes.
type Permission string

const (
	OrganizationView   Permission = "organization.view"
	OrganizationManage Permission = "organization.manage" // name, time zone, and other settings
	IntegrationsManage Permission = "integrations.manage" // connect sales channels such as Shopify

	TeamView    Permission = "team.view"
	TeamManage  Permission = "team.manage"  // invite and remove members, limited by CanManageRole
	RolesManage Permission = "roles.manage" // change members' roles, including granting Owner

	InventoryView  Permission = "inventory.view"
	InventoryEdit  Permission = "inventory.edit"  // add and edit plants and photos
	ListingsManage Permission = "listings.manage" // publish listings, set prices, sync channels
	FinancialsView Permission = "financials.view" // purchase cost, sale proceeds, fees, profit
)

// rolePermissions is each role's default permission set, listed in a fixed order for API responses.
var rolePermissions = map[dbgen.OrgRole][]Permission{
	dbgen.OrgRoleOwner: {
		OrganizationView, OrganizationManage, IntegrationsManage,
		TeamView, TeamManage, RolesManage,
		InventoryView, InventoryEdit, ListingsManage, FinancialsView,
	},
	dbgen.OrgRoleAdmin: {
		OrganizationView, TeamView, TeamManage,
		InventoryView, InventoryEdit, ListingsManage,
	},
	dbgen.OrgRoleStaff: {
		OrganizationView, TeamView, InventoryView, InventoryEdit,
	},
	dbgen.OrgRoleViewer: {
		OrganizationView, TeamView, InventoryView,
	},
}

// Has reports whether role grants permission.
func Has(role dbgen.OrgRole, permission Permission) bool {
	return slices.Contains(rolePermissions[role], permission)
}

// PermissionsFor returns everything role may do.
func PermissionsFor(role dbgen.OrgRole) []Permission {
	return slices.Clone(rolePermissions[role]) // a copy, so callers can't change the defaults
}

// CanManageRole reports whether a member with role actor may invite or remove members with role
// target: Owners manage every role, Admins manage Staff and Viewers only.
func CanManageRole(actor, target dbgen.OrgRole) bool {
	switch actor {
	case dbgen.OrgRoleOwner:
		return true
	case dbgen.OrgRoleAdmin:
		return target == dbgen.OrgRoleStaff || target == dbgen.OrgRoleViewer
	default:
		return false
	}
}

// validRole reports whether role is one of the four roles (for checking request input).
func validRole(role dbgen.OrgRole) bool {
	_, ok := rolePermissions[role]
	return ok
}
