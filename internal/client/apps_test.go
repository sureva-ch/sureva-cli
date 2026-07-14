package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

// ---- T3: CreateApp and DeleteApp RED tests ----

func TestCreateApp_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/apps" {
			t.Errorf("path = %q, want /v1/orgs/org-1/apps", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		// Required fields must be present.
		for _, field := range []string{"name", "team_id", "type", "region"} {
			if _, ok := body[field]; !ok {
				t.Errorf("request body missing required field %q", field)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(client.App{
			ID:           "app-new",
			OrgID:        "org-1",
			Name:         "my-api",
			Type:         "web",
			Subdomain:    "my-api",
			DomainStatus: "pending",
			IsActive:     true,
			CreatedAt:    time.Now(),
		})
	})

	req := client.CreateAppRequest{
		Name:   "my-api",
		TeamID: "team-1",
		Type:   "web",
		Region: "us-east-1",
	}
	app, err := c.CreateApp(context.Background(), "org-1", req)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if app.ID != "app-new" {
		t.Errorf("ID = %q, want 'app-new'", app.ID)
	}
	if app.DomainStatus != "pending" {
		t.Errorf("DomainStatus = %q, want 'pending'", app.DomainStatus)
	}
}

func TestCreateApp_OmitemptyFields(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// omitempty fields must be absent when zero.
		for _, field := range []string{"label", "runtime", "build_command", "build_output_dir", "github_branch"} {
			if _, ok := body[field]; ok {
				t.Errorf("omitempty field %q present in request but should be absent", field)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(client.App{ID: "app-2"})
	})

	req := client.CreateAppRequest{
		Name:   "my-api",
		TeamID: "team-1",
		Type:   "web",
		Region: "us-east-1",
	}
	_, err := c.CreateApp(context.Background(), "org-1", req)
	if err != nil {
		t.Fatalf("CreateApp omitempty: %v", err)
	}
}

func TestCreateApp_ErrorPassthrough(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "name already taken"})
	})

	req := client.CreateAppRequest{
		Name:   "taken",
		TeamID: "team-1",
		Type:   "web",
		Region: "us-east-1",
	}
	_, err := c.CreateApp(context.Background(), "org-1", req)
	if err == nil {
		t.Fatal("expected error from 400, got nil")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.HTTPStatus != 400 {
		t.Errorf("HTTPStatus = %d, want 400", apiErr.HTTPStatus)
	}
}

func TestDeleteApp_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1" {
			t.Errorf("path = %q, want /v1/orgs/org-1/apps/app-1", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(client.DeleteAppResponse{Status: "deleting"})
	})

	resp, err := c.DeleteApp(context.Background(), "org-1", "app-1")
	if err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if resp.Status != "deleting" {
		t.Errorf("Status = %q, want 'deleting'", resp.Status)
	}
}

func TestDeleteApp_NotFound(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "app not found"})
	})

	_, err := c.DeleteApp(context.Background(), "org-1", "nonexistent")
	if err == nil {
		t.Fatal("expected error from 404, got nil")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.HTTPStatus != 404 {
		t.Errorf("HTTPStatus = %d, want 404", apiErr.HTTPStatus)
	}
	if apiErr.Code != "not_found" {
		t.Errorf("Code = %q, want 'not_found'", apiErr.Code)
	}
}
