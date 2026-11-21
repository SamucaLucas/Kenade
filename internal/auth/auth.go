package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const CookieName = "kenade_session"

type SessionClaims struct {
	UserID   uint   `json:"uid"`
	Username string `json:"usr"`
	Role     string `json:"rol"`
	Exp      int64  `json:"exp"`
}

func getSecretKey() []byte {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "kenade-enade-super-secret-key-2026"
	}
	return []byte(secret)
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateToken(userID uint, username, role string) (string, error) {
	claims := SessionClaims{
		UserID:   userID,
		Username: username,
		Role:     role,
		Exp:      time.Now().Add(30 * 24 * time.Hour).Unix(),
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sig := signPayload(payloadB64)
	return payloadB64 + "." + sig, nil
}

func VerifyToken(token string) (*SessionClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("formato de token inválido")
	}
	expectedSig := signPayload(parts[0])
	if !hmac.Equal([]byte(parts[1]), []byte(expectedSig)) {
		return nil, errors.New("assinatura de token inválida")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var claims SessionClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, err
	}
	if time.Now().Unix() > claims.Exp {
		return nil, errors.New("sessão expirada")
	}
	return &claims, nil
}

func signPayload(payload string) string {
	mac := hmac.New(sha256.New, getSecretKey())
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   30 * 24 * 3600,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func ExtractClaimsFromRequest(r *http.Request) (*SessionClaims, error) {
	// 1. Verifica Header Authorization Bearer
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if claims, err := VerifyToken(token); err == nil {
			return claims, nil
		}
	}
	// 2. Verifica Cookie de Sessão
	cookie, err := r.Cookie(CookieName)
	if err == nil && cookie.Value != "" {
		if claims, err := VerifyToken(cookie.Value); err == nil {
			return claims, nil
		}
	}
	// 3. Verifica Query param token (para WebSocket se necessário)
	if qToken := r.URL.Query().Get("token"); qToken != "" {
		if claims, err := VerifyToken(qToken); err == nil {
			return claims, nil
		}
	}
	return nil, fmt.Errorf("não autenticado")
}
