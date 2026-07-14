package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sureva-ch/sureva-cli/internal/client"
)

func TestCreateKVSTable_SendsBody(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/orgs/org-1/apps/app-1/services/kvs/tables" {
			t.Errorf("path = %q, want /v1/orgs/org-1/apps/app-1/services/kvs/tables", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["name"] != "sessions" {
			t.Errorf("name = %v, want sessions", body["name"])
		}
		if body["minute_limit"] != float64(300) {
			t.Errorf("minute_limit = %v, want 300", body["minute_limit"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(client.KVSTableResponse{
			Table: &client.KVSTable{ID: "table-1", Name: "sessions", Status: "active", KVSURL: "https://kvs.example.com", MinuteLimit: 300},
			Token: "skvs_table_secret",
		})
	})

	resp, err := c.CreateKVSTable(context.Background(), "org-1", "app-1", client.CreateKVSTableRequest{
		Name:        "sessions",
		MinuteLimit: 300,
	})
	if err != nil {
		t.Fatalf("CreateKVSTable: %v", err)
	}
	if resp.Table.Name != "sessions" {
		t.Errorf("Table.Name = %q, want sessions", resp.Table.Name)
	}
}
