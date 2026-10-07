package auth

import "testing"

func TestAdvertisedPermissionRegistryRoundTripsThroughRoleValidation(t *testing.T) {
	grants := map[string]AccessAuth{}
	for _, mark := range accessMarks {
		grants[mark] = AccessAuth{Readable: true, Writeable: true}
	}
	nodes := permissionNodes(grants, false)
	input := make([]AccessGrant, 0, len(nodes))
	for _, node := range nodes {
		input = append(input, AccessGrant{Mark: node.Mark, Auth: node.Auth})
	}
	validated, err := validateGrants(input)
	if err != nil {
		t.Fatalf("server-advertised permission cannot be saved: %v", err)
	}
	if len(validated) != len(nodes) {
		t.Fatal("permission registry lost a grant")
	}
	for _, node := range nodes {
		if validated[node.Mark] != node.Auth {
			t.Fatalf("grant changed for %s", node.Mark)
		}
	}
	for _, unknown := range []string{"", "TASKS", "tasks.evil", "unregistered"} {
		if _, err = validateGrants([]AccessGrant{{Mark: unknown, Auth: AccessAuth{Readable: true}}}); err == nil {
			t.Fatalf("unknown grant %q accepted", unknown)
		}
	}
	if _, err = validateGrants([]AccessGrant{{Mark: "tasks", Auth: AccessAuth{Readable: true}}, {Mark: "tasks", Auth: AccessAuth{Readable: true}}}); err == nil {
		t.Fatal("duplicate grant accepted")
	}
	if _, err = validateGrants([]AccessGrant{{Mark: "tasks", Auth: AccessAuth{Writeable: true}}}); err == nil {
		t.Fatal("write without read accepted")
	}
}
