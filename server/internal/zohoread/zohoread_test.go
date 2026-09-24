package zohoread

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateCOQL(t *testing.T) {
	ok := []string{
		"SELECT Deal_Name FROM Deals WHERE Stage = 'Won' LIMIT 10",
		"  select id from Leads where id is not null",
	}
	for _, q := range ok {
		if err := ValidateCOQL(q); err != nil {
			t.Fatalf("%q rejected: %v", q, err)
		}
	}
	bad := []string{"", "   ", "DELETE FROM Deals", "UPDATE Deals SET Stage='x'",
		"SELECT id FROM Deals; DELETE FROM Deals", "selectid from Deals", strings.Repeat("x", 2001)}
	for _, q := range bad {
		if err := ValidateCOQL(q); err == nil {
			t.Fatalf("%q accepted", q)
		}
	}
}

func TestCallWithoutProductSaysSo(t *testing.T) {
	ctx := context.Background()
	if _, err := Call(ctx, Clients{}, "zoho_crm_modules", nil); err == nil || !strings.Contains(err.Error(), "CRM isn't available") {
		t.Fatalf("crm without client: %v", err)
	}
	if _, err := Call(ctx, Clients{}, "zoho_desk_list_tickets", nil); err == nil || !strings.Contains(err.Error(), "Desk isn't available") {
		t.Fatalf("desk without client: %v", err)
	}
	if _, err := Call(ctx, Clients{}, "zoho_crm_update_record", nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("write tool: %v", err)
	}
	res, err := Call(ctx, Clients{Me: Identity{Email: "a@b.c", CRMRole: "Agent"}}, "zoho_whoami", nil)
	if err != nil || res.Value.(Identity).CRMRole != "Agent" {
		t.Fatalf("whoami: %v %v", res, err)
	}
}

func TestToolsAreReadOnly(t *testing.T) {
	for _, tool := range Tools() {
		for _, verb := range []string{"create", "update", "delete", "send", "assign"} {
			if strings.Contains(tool.Name, verb) {
				t.Fatalf("tool %s looks like a write", tool.Name)
			}
		}
		if tool.InputSchema["type"] != "object" {
			t.Fatalf("tool %s schema: %v", tool.Name, tool.InputSchema)
		}
	}
}
