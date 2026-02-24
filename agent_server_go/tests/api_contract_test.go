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
	// 使用最小依赖构造 HTTP 服务：不连 DB/Redis/MQ，只验证 API 契约
	metrics := obs.NewMetrics()
	svc := service.New(nil, (*cache.Client)(nil), nil, nil, 5, 10)
	app := server.NewServer(svc, nil, "test-secret", 5, 10, metrics).App

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "t1",
		"user_id":   "u1",
	})
	// 手工签一个测试 token，验证 JWT 中间件与 /v1/search 配合是否可用
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

	// 直接在内存里发起请求（无需真正监听端口）
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
