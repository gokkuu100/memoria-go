package auth

import "time"

// Auth endpoint rate limits (per single API instance; shared Limiter with global
// middleware in server). Global buckets: 600 req/min per IP, 900 req/min per
// authenticated user — see server.RateLimitIP / RateLimitUser.
const (
	OTPEmailLimit   = 3
	OTPIPWindow     = 10 * time.Minute
	OTPEmailWindow  = 10 * time.Minute
	OTPIPRequest    = 10
	OTPVerifyIP     = 20
	LoginIPLimit    = 10
	LoginIPWindow   = time.Minute
	UsernameIPLimit = 30
	UsernameIPWindow = time.Minute
)
