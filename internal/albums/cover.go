package albums

import (
	"time"
)

const inviteWindow = 48 * time.Hour

var validCoverStyles = map[string]bool{
	"espresso":         true,
	"gold":             true,
	"cream":            true,
	"dot_grid":         true,
	"wave":             true,
	"gradient_sunset":  true,
}

func validCoverStyle(s string) bool {
	return validCoverStyles[s]
}
