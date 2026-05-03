// Package rules contains all mcpcheck rule implementations.
//
// Each rule lives in its own .go file and registers itself with the v2
// registry from an init function. Importing this package for side effects
// loads every rule.
package rules
