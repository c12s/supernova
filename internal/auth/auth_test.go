package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testKey = "test-secret-key"

func token(t *testing.T, permissions string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"permissions": permissions,
		"exp":         time.Now().Add(time.Hour).Unix(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testKey))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

func ctxWith(tok string) context.Context {
	return context.WithValue(context.Background(), "authz-token", tok)
}

func TestAuthorizeAllowed(t *testing.T) {
	t.Setenv("SECRET_KEY", testKey)
	if !Authorize(ctxWith(token(t, "image.build|image|os/edge-base,node.get|node|x")), "image.build", "image", "os/edge-base") {
		t.Fatal("expected the permission to be granted")
	}
}

func TestAuthorizeWrongObject(t *testing.T) {
	t.Setenv("SECRET_KEY", testKey)
	if Authorize(ctxWith(token(t, "image.build|image|other/image")), "image.build", "image", "os/edge-base") {
		t.Fatal("expected the permission to be denied for another image")
	}
}

func TestAuthorizeNoToken(t *testing.T) {
	t.Setenv("SECRET_KEY", testKey)
	if Authorize(context.Background(), "image.build", "image", "os/edge-base") {
		t.Fatal("expected no token to be denied")
	}
}

func TestAuthorizeMissingKey(t *testing.T) {
	t.Setenv("SECRET_KEY", "")
	if Authorize(ctxWith(token(t, "image.build|image|os/edge-base")), "image.build", "image", "os/edge-base") {
		t.Fatal("expected a missing SECRET_KEY to be denied")
	}
}
