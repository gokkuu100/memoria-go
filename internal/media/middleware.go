package media

import (
	"net/http"

	"memoria-backend/internal/config"
)

// PublicEndpointMiddleware reads X-Memoria-Media-Endpoint (dev) so signed GET/PUT
// URLs target the host the mobile client can reach.
func PublicEndpointMiddleware(env config.Env) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if env == config.EnvDevelopment {
				if ep := r.Header.Get("X-Memoria-Media-Endpoint"); ep != "" && AllowedDevMediaEndpoint(ep) {
					ctx = WithPublicEndpoint(ctx, ep)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
