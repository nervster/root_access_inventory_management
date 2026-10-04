package organization

import (
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role       dbgen.OrgRole
		permission Permission
		want       bool
	}{
		{dbgen.OrgRoleOwner, FinancialsView, true},
		{dbgen.OrgRoleOwner, RolesManage, true},
		{dbgen.OrgRoleAdmin, FinancialsView, false}, // only Owners see financials
		{dbgen.OrgRoleAdmin, ListingsManage, true},
		{dbgen.OrgRoleAdmin, RolesManage, false},
		{dbgen.OrgRoleStaff, InventoryEdit, true},
		{dbgen.OrgRoleStaff, TeamManage, false},
		{dbgen.OrgRoleViewer, InventoryView, true},
		{dbgen.OrgRoleViewer, InventoryEdit, false},
		{"superadmin", OrganizationView, false}, // unknown roles get nothing
	}
	for _, tt := range tests {
		if got := Has(tt.role, tt.permission); got != tt.want {
			t.Errorf("Has(%s, %s) = %v, want %v", tt.role, tt.permission, got, tt.want)
		}
	}
}

func TestCanManageRole(t *testing.T) {
	tests := []struct {
		actor, target dbgen.OrgRole
		want          bool
	}{
		{dbgen.OrgRoleOwner, dbgen.OrgRoleOwner, true},
		{dbgen.OrgRoleAdmin, dbgen.OrgRoleStaff, true},
		{dbgen.OrgRoleAdmin, dbgen.OrgRoleViewer, true},
		{dbgen.OrgRoleAdmin, dbgen.OrgRoleAdmin, false},
		{dbgen.OrgRoleAdmin, dbgen.OrgRoleOwner, false},
		{dbgen.OrgRoleStaff, dbgen.OrgRoleViewer, false},
	}
	for _, tt := range tests {
		if got := CanManageRole(tt.actor, tt.target); got != tt.want {
			t.Errorf("CanManageRole(%s, %s) = %v, want %v", tt.actor, tt.target, got, tt.want)
		}
	}
}
