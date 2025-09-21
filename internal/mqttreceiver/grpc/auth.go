package grpcserver

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"brutus/internal/mqttreceiver/logger"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// -------------------------
// Конфигурация авторизации
// -------------------------

// getJWTSecret возвращает секрет для подписи JWT из окружения.
// Если секрет не задан — логируем предупреждение и используем пустой секрет (не безопасно).
func getJWTSecret() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		logger.Log.Warn().Msg("JWT_SECRET is not set — using empty secret (INSECURE). Set JWT_SECRET in env for production.")
	}
	return []byte(secret)
}

// getJWTTTL возвращает TTL для токена в time.Duration.
// Читает переменную JWT_TTL_MINUTES, по умолчанию 60 минут.
func getJWTTTL() time.Duration {
	minStr := os.Getenv("JWT_TTL_MINUTES")
	if minStr == "" {
		return time.Hour
	}
	mins, err := strconv.Atoi(minStr)
	if err != nil || mins <= 0 {
		return time.Hour
	}
	return time.Duration(mins) * time.Minute
}

// -------------------------
// Проверка логина/пароля
// -------------------------

// CheckCredentials проверяет username/password против ENV AUTH_USERNAME/AUTH_PASSWORD.
// По умолчанию — "admin"/"s3cr3t".
func CheckCredentials(username, password string) bool {
	envUser := os.Getenv("AUTH_USERNAME")
	envPass := os.Getenv("AUTH_PASSWORD")
	if envUser == "" {
		envUser = "admin"
	}
	if envPass == "" {
		envPass = "s3cr3t"
	}
	return username == envUser && password == envPass
}

// -------------------------
// JWT генерация и проверка
// -------------------------

// GenerateJWT создаёт JWT (HS256) с полем "username" и временем истечения.
func GenerateJWT(username string) (string, error) {
	secret := getJWTSecret()
	ttl := getJWTTTL()

	claims := jwt.MapClaims{
		"username": username,
		"exp":      time.Now().Add(ttl).Unix(),
		"iat":      time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		logger.Log.Error().Err(err).Msg("GenerateJWT: signing failed")
		return "", err
	}
	return signed, nil
}

// ValidateJWT парсит и валидирует токен, возвращая claims (jwt.MapClaims).
func ValidateJWT(tokenString string) (jwt.MapClaims, error) {
	secret := getJWTSecret()
	if len(secret) == 0 {
		// ещё раз логируем, потому что пустой секрет — потенциальная проблема
		logger.Log.Warn().Msg("ValidateJWT: JWT_SECRET is empty — token validation may fail")
	}

	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		// защита от изменения алгоритма
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithLeeway(5*time.Second))
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("token invalid")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}
	// Дополнительно можно проверить exp вручную, но jwt lib уже делает это при Parse, если claims используются корректно.
	return claims, nil
}

// -------------------------
// gRPC интерцепторы
// -------------------------

// UnaryAuthInterceptor проверяет JWT для unary RPC.
// Login (/brutus.MQTTReceiver/Login) пропускается без проверки.
func UnaryAuthInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	// allow login without token
	if info.FullMethod == "/brutus.MQTTReceiver/Login" {
		return handler(ctx, req)
	}
	if err := authorize(ctx); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

// StreamAuthInterceptor проверяет JWT для stream RPCs.
func StreamAuthInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	// allow login stream (if any) — currently Login is unary, but keep check
	if info.FullMethod == "/brutus.MQTTReceiver/Login" {
		return handler(srv, ss)
	}
	if err := authorize(ss.Context()); err != nil {
		return err
	}
	return handler(srv, ss)
}

// authorize извлекает Authorization metadata и валидирует токен.
func authorize(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "metadata not provided")
	}

	values := md.Get("authorization")
	if len(values) == 0 {
		return status.Error(codes.Unauthenticated, "authorization token not provided")
	}

	// support: "Bearer <token>" or just "<token>"
	authHeader := values[0]
	parts := strings.SplitN(authHeader, " ", 2)
	var tokenStr string
	if len(parts) == 1 {
		tokenStr = parts[0]
	} else if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		tokenStr = parts[1]
	} else {
		return status.Error(codes.Unauthenticated, "invalid authorization header")
	}

	_, err := ValidateJWT(tokenStr)
	if err != nil {
		logger.Log.Debug().Err(err).Str("auth_header", authHeader).Msg("authorize: token validation failed")
		return status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}
	return nil
}
