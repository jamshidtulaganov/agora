package zohoprojects

import (
	"context"
	"net/http"
	"testing"
)

func TestClientParsesProjectOwnerAndStatus(t *testing.T) {
	c := newFieldsServer(t, http.StatusOK)
	projects, err := c.ListProjects(context.Background(), "1")
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = %v, %v", projects, err)
	}
	p := projects[0]
	if p.Key != "OCT-33" || p.CustomStatus != "In Progress" || p.Name != "Collections & Recovery" ||
		p.Owner.Email != "zeyba.i@octanefuel.com" || p.Owner.Name != "Zeyba Ildarova" {
		t.Errorf("project = %+v", p)
	}
}

func TestClientListProjectUsers(t *testing.T) {
	c := newFieldsServer(t, http.StatusOK)
	users, err := c.ListProjectUsers(context.Background(), "1", "2494")
	if err != nil {
		t.Fatalf("ListProjectUsers: %v", err)
	}
	if len(users) != 2 || users[0].Role != "manager" || !users[0].Active || users[1].Active {
		t.Errorf("users = %+v", users)
	}
}

func TestClientListProjectUsersScopeError(t *testing.T) {
	c := newFieldsServer(t, http.StatusUnauthorized)
	if _, err := c.ListProjectUsers(context.Background(), "1", "2494"); err == nil {
		t.Fatal("expected scope error")
	}
}
