package client_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

func TestCancelDeployment_Success(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1/deployments/deploy-1" {
			t.Errorf("path = %q, want /v1/orgs/org-1/apps/app-1/deployments/deploy-1", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.CancelDeployment(context.Background(), "org-1", "app-1", "deploy-1"); err != nil {
		t.Fatalf("CancelDeployment: %v", err)
	}
}

func TestCancelDeployment_NotFound(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"deployment not found"}`))
	})

	err := c.CancelDeployment(context.Background(), "org-1", "app-1", "missing")
	if err == nil {
		t.Fatal("expected error from 404, got nil")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}
	if apiErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus = %d, want 404", apiErr.HTTPStatus)
	}
	if apiErr.Code != "not_found" {
		t.Errorf("Code = %q, want not_found", apiErr.Code)
	}
}
