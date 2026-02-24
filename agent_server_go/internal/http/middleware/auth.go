package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"

	"agent_server_go/internal/http/response"
)

func JWTAuth(secret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// 只接受 Bearer Token
		auth := c.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			return response.Error(c, fiber.StatusUnauthorized, "UNAUTHORIZED", "missing bearer token")
		}

		tokenStr := strings.TrimPrefix(auth, "Bearer ")
		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			return response.Error(c, fiber.StatusUnauthorized, "UNAUTHORIZED", "invalid token")
		}

		// 从 claims 中抽出 tenant_id / user_id，供后续鉴权和限流使用
		claims, ok := token.Claims.(jwt.MapClaims)
		if ok {
			if tenantID, ok2 := claims["tenant_id"].(string); ok2 {
				c.Locals("tenant_id", tenantID)
			}
			if userID, ok2 := claims["user_id"].(string); ok2 {
				c.Locals("user_id", userID)
			}
		}

		return c.Next()
	}
}
