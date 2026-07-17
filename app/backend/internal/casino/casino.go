// Package casino implements the town's single-shot minigames as pure functions
// from a bet and RNG to a payout and result detail. The action layer wraps each
// play in a ledger transaction (bet -> system:casino, payout <- system:casino)
// and records it to game_plays. Games register themselves via init().
package casino

import (
	"encoding/json"

	"github.com/shiroha-a/town/internal/rng"
)

// Result is the outcome of one minigame play.
type Result struct {
	// Payout is the amount returned to the player, INCLUDING the returned stake
	// on a win. Net profit/loss is Payout - bet. A total loss has Payout 0.
	Payout int64
	// Detail is a game-specific struct serialized to JSON for the frontend.
	Detail any
	// Win marks a winning round (for presentation only).
	Win bool
}

// Game is a single-shot minigame. Play must be pure: no ledger or DB access.
type Game interface {
	// Bets returns the allowed bet amounts; empty means any positive amount.
	Bets() []int64
	// Play runs one round from the bet and game-specific JSON params.
	Play(r *rng.Rand, bet int64, params json.RawMessage) (Result, error)
}

// registry holds all single-shot games by key (e.g. "saikoro").
var registry = map[string]Game{}

func register(key string, g Game) { registry[key] = g }

// Lookup returns the game for a key, or nil if unknown.
func Lookup(key string) Game { return registry[key] }

// pick returns a uniformly random element of xs.
func pick[T any](r *rng.Rand, xs []T) T { return xs[r.IntN(len(xs))] }
