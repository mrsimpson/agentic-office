// Package gamma (bb-gamma) uses if-alpha without requiring it, and if-omega with requires.
package gamma

import (
	_ "example.com/violations/alpha/api"
	_ "example.com/violations/omega/one/api"
)
