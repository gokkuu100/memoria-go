-- name: DeleteOTPs :exec
DELETE FROM otp_codes WHERE email = $1 AND purpose = $2;

-- name: CreateOTP :one
INSERT INTO otp_codes (email, code_hash, purpose, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetActiveOTP :one
SELECT * FROM otp_codes
WHERE email = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- name: IncrementOTPAttempts :one
UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts;

-- name: ConsumeOTP :exec
UPDATE otp_codes SET consumed_at = now() WHERE id = $1;
