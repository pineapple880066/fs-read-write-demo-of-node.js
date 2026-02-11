package tests

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	cache "agent_server_go/internal/cache/redis"
	server "agent_server_go/internal/http"
	"agent_server_go/internal/obs"
	"agent_server_go/internal/service"
)

func TestSearchContract(t *testing.T) {
	metrics := obs.NewMetrics()
	svc := service.New(nil, (*cache.Client)(nil), nil, nil, 5, 10)
	app := server.NewServer(svc, nil, "test-secret", 5, 10, metrics).App

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "t1",
		"user_id":   "u1",
	})
	token, err := tok.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatalf("sign token failed: %v", err)
	}

	payload := map[string]any{
		"tenant_id": "t1",
		"query":     "what is this project",
		"top_k":     3,
	}
	b, _ := json.Marshal(payload)

	req := httptest.NewRequest("POST", "/v1/search", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
}
