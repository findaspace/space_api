package identity

import "github.com/golang-jwt/jwt/v5"

const (
	toeknIssuer   = "space_api"
	tokenAudience = "space_clients"
)

type AccessClaims struct {
	jwt.RegisteredClaims
	Roles []Role `json:"roles"`
}
