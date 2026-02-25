package middleware

// JWT鉴权

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"

	"agent_server_go/internal/http/response"
)

func JWTAuth(secret string) fiber.Handler {
	// JWTAuth 返回一个鉴权中间件：校验 Bearer Token，并把身份信息写入 Locals。
	return func(c *fiber.Ctx) error {
		// 只接受 Bearer Token
		auth := c.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") { // 为空或者没有Bearer 前缀
			return response.Error(c, fiber.StatusUnauthorized, "UNAUTHORIZED", "missing bearer token")
		}

		tokenStr := strings.TrimPrefix(auth, "Bearer ")                         // 去掉前缀 "Bearer "，得到纯 token 字符串
		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) { // 解析并验签 token
			// 返回服务端密钥给 JWT 库，用来校验 token 签名是否正确
			return []byte(secret), nil
		})
		if err != nil || !token.Valid { // token	是否有效
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

		return c.Next() // 鉴权通过，继续后续流程
	}
}
