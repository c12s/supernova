package auth

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Authorize mirrors the backend permission check used by magnetar, gravity and
// kuiper: the gateway verifies the vault token with Apollo and forwards the
// signed JWT as the "authz-token" gRPC metadata. The service parses the JWT
// with the shared SECRET_KEY and looks for "permission|kind|id" in the
// permissions claim.
//
// Builds are authorized as image.build|image|<image name> (the repo path, e.g.
// "os/edge-base"), so an operator policy grants the build permission on the
// image, not on a node.
func Authorize(ctx context.Context, permName, objKind, objID string) bool {
	tokenString, ok := ctx.Value("authz-token").(string)
	if !ok || tokenString == "" {
		log.Println("auth: no authz-token in context")
		return false
	}
	key := os.Getenv("SECRET_KEY")
	if key == "" {
		log.Println("auth: SECRET_KEY is not set; refusing the request")
		return false
	}
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(key), nil
	})
	if err != nil {
		log.Printf("auth: parse token: %v", err)
		return false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		log.Println("auth: invalid claims type")
		return false
	}
	permissionsClaim, ok := claims["permissions"].(string)
	if !ok {
		log.Println("auth: permissions claim is missing or not a string")
		return false
	}
	required := fmt.Sprintf("%s|%s|%s", permName, objKind, objID)
	for _, perm := range strings.Split(permissionsClaim, ",") {
		if perm == required {
			return true
		}
	}
	log.Printf("auth: required permission %q not found in token", required)
	return false
}
