// Package two (bb-two) uses if-omega of its parent without requiring it: a violation.
package two

import _ "example.com/violations/omega/one/api"
