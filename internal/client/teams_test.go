package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// ---- T1: ListTeams RED tests ----

func TestListTeams_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/teams" {
			t.Errorf("path = %q, want /v1/orgs/org-1/teams", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Team{
			{ID: "team-1", OrgID: "org-1", Slug: "engineers", Name: "Engineers", IsSystem: false, IsActive: true},
			{ID: "team-2", OrgID: "org-1", Slug: "admins", Name: "Admins", IsSystem: true, IsActive: true},
		})
	})

	teams, err := c.ListTeams(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("ListTeams: %v", err)
	}
	if len(teams) != 2 {
		t.Errorf("len(teams) = %d, want 2", len(teams))
	}
	if teams[0].Slug != "engineers" {
		t.Errorf("teams[0].Slug = %q, want 'engineers'", teams[0].Slug)
	}
	if teams[1].Name != "Admins" {
		t.Errorf("teams[1].Name = %q, want 'Admins'", teams[1].Name)
	}
}

func TestListTeams_Empty(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.Team{})
	})

	teams, err := c.ListTeams(context.Background(), "org-1")
	if err != nil {
		t.Fatalf("ListTeams empty: %v", err)
	}
	if len(teams) != 0 {
		t.Errorf("len(teams) = %d, want 0", len(teams))
	}
}

func TestListTeams_ErrorPassthrough(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := c.ListTeams(context.Background(), "org-1")
	if err == nil {
		t.Fatal("expected error from 500, got nil")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.Code != "server_error" {
		t.Errorf("Code = %q, want 'server_error'", apiErr.Code)
	}
}
