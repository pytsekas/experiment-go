package httpapi

import (
	"context"
	"fmt"

	"google.golang.org/api/idtoken"
)

// oidcVerifier validates the token Pub/Sub attaches to a push request.
//
// Cloud Run already rejects callers without an invoker binding; this is the
// second layer, so an accidental allUsers binding cannot turn the endpoint
// into an open sink. It checks the signature, the audience and the identity.
type oidcVerifier struct {
	audience       string
	serviceAccount string
}

var _ TokenVerifier = oidcVerifier{}

// NewOIDCVerifier returns a verifier accepting tokens issued for audience and
// signed for serviceAccount.
func NewOIDCVerifier(audience, serviceAccount string) TokenVerifier {
	return oidcVerifier{audience: audience, serviceAccount: serviceAccount}
}

// Verify implements TokenVerifier.
func (v oidcVerifier) Verify(ctx context.Context, bearerToken string) error {
	if bearerToken == "" {
		return fmt.Errorf("oidc: no bearer token")
	}

	payload, err := idtoken.Validate(ctx, bearerToken, v.audience)
	if err != nil {
		return fmt.Errorf("oidc: validating token: %w", err)
	}

	email, _ := payload.Claims["email"].(string)
	if email != v.serviceAccount {
		return fmt.Errorf("oidc: token belongs to %q, want %q", email, v.serviceAccount)
	}

	if verified, ok := payload.Claims["email_verified"].(bool); ok && !verified {
		return fmt.Errorf("oidc: token email is not verified")
	}

	return nil
}
