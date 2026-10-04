package client_test

import (
	"context"
	"encoding/json"
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

func TestTriggerDeployment_SendsSourceIDOnlyWhenSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tag, srcID string
		wantTag    bool
		wantSource bool
	}{
		{name: "tag only", tag: "v1.0.0", wantTag: true},
		{name: "source id only", srcID: "src-id-1", wantSource: true},
		{name: "neither"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got map[string]any
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"deploy-1","status":"pending"}`))
			})

			if _, err := c.TriggerDeployment(context.Background(), "org-1", "app-1", tc.tag, tc.srcID, ""); err != nil {
				t.Fatalf("TriggerDeployment: %v", err)
			}
			if _, present := got["release_tag"]; present != tc.wantTag {
				t.Errorf("release_tag present = %v, want %v (body: %v)", present, tc.wantTag, got)
			}
			if v, present := got["source_id"]; present != tc.wantSource {
				t.Errorf("source_id present = %v, want %v (body: %v)", present, tc.wantSource, got)
			} else if present && v != tc.srcID {
				t.Errorf("source_id = %v, want %q", v, tc.srcID)
			}
		})
	}
}
