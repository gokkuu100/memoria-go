package httpx

// Input length caps enforced across handlers (B11 validation pass).
const (
	MaxNameLen        = 100
	MaxCaptionLen     = 500
	MaxCommentLen     = 1000
	MaxDisplayNameLen = 100
)
