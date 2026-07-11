// Package effects implements the data-driven effect/condition schema. Items,
// jobs and events store their behavior as JSON that is parsed, validated and
// evaluated here — entirely server-side, with no arbitrary code execution.
// Admin-authored content is untrusted input: unknown ops/predicates/params are
// rejected at parse time.
package effects

import (
	"encoding/json"
	"fmt"
)

// SchemaVersion is the current effect/condition schema version.
const SchemaVersion = 1

// AllParams lists every parameter an effect/condition may reference. These map
// 1:1 to player_status columns. Note: satiety(空腹値) is a status value, not a
// displayed "parameter", but effects (食事) target it, so it is listed here.
var AllParams = []string{
	"energy", "nou_energy", "satiety",
	"kokugo", "suugaku", "rika", "syakai", "eigo", "ongaku", "bijutsu",
	"looks", "tairyoku", "kenkou", "speed", "power", "wanryoku", "kyakuryoku",
	"love", "omoshirosa",
}

// knownParams are the parameters an effect may touch (mapped to status columns).
var knownParams = func() map[string]bool {
	m := make(map[string]bool, len(AllParams))
	for _, p := range AllParams {
		m[p] = true
	}
	return m
}()

// Op is a single effect operation.
type Op struct {
	Kind   string
	Param  string
	Amount int64
}

// Effect is an ordered list of operations applied atomically.
type Effect struct {
	Ops []Op
}

type opJSON struct {
	Op     string `json:"op"`
	Param  string `json:"param,omitempty"`
	Amount int64  `json:"amount"`
}

// ParseEffect parses and validates effect JSON.
func ParseEffect(data []byte) (Effect, error) {
	var raw []opJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return Effect{}, fmt.Errorf("effect json: %w", err)
	}
	eff := Effect{Ops: make([]Op, 0, len(raw))}
	for i, r := range raw {
		switch r.Op {
		case "add_money":
			eff.Ops = append(eff.Ops, Op{Kind: r.Op, Amount: r.Amount})
		case "add_param":
			if !knownParams[r.Param] {
				return Effect{}, fmt.Errorf("effect[%d]: unknown param %q", i, r.Param)
			}
			eff.Ops = append(eff.Ops, Op{Kind: r.Op, Param: r.Param, Amount: r.Amount})
		default:
			return Effect{}, fmt.Errorf("effect[%d]: unknown op %q", i, r.Op)
		}
	}
	return eff, nil
}

// MoneySum returns the net money delta of the effect (for display).
func (e Effect) MoneySum() int64 {
	var sum int64
	for _, op := range e.Ops {
		if op.Kind == "add_money" {
			sum += op.Amount
		}
	}
	return sum
}

// ParamSum returns the net delta per parameter (for display of rising params).
func (e Effect) ParamSum() map[string]int {
	m := map[string]int{}
	for _, op := range e.Ops {
		if op.Kind == "add_param" {
			m[op.Param] += int(op.Amount)
		}
	}
	return m
}

// Pred is a single condition predicate.
type Pred struct {
	Kind  string
	Param string
	Value int64
}

// Conditions is a list of predicates combined with logical AND.
type Conditions struct {
	Preds []Pred
}

type predJSON struct {
	Pred  string `json:"pred"`
	Param string `json:"param,omitempty"`
	Value int64  `json:"value"`
}

// ParseConditions parses and validates condition JSON.
func ParseConditions(data []byte) (Conditions, error) {
	var raw []predJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return Conditions{}, fmt.Errorf("conditions json: %w", err)
	}
	c := Conditions{Preds: make([]Pred, 0, len(raw))}
	for i, r := range raw {
		switch r.Pred {
		case "money_gte":
			c.Preds = append(c.Preds, Pred{Kind: r.Pred, Value: r.Value})
		case "param_gte":
			if !knownParams[r.Param] {
				return Conditions{}, fmt.Errorf("conditions[%d]: unknown param %q", i, r.Param)
			}
			c.Preds = append(c.Preds, Pred{Kind: r.Pred, Param: r.Param, Value: r.Value})
		default:
			return Conditions{}, fmt.Errorf("conditions[%d]: unknown pred %q", i, r.Pred)
		}
	}
	return c, nil
}

// InsufficientParam reports the first parameter that a negative effect would
// drive below zero given the current state (e.g. not enough energy to work).
// Returns ("", false) if the effect can be afforded.
func (e Effect) InsufficientParam(s State) (string, bool) {
	for _, op := range e.Ops {
		if op.Kind == "add_param" && op.Amount < 0 {
			if s.Params[op.Param].Value+int(op.Amount) < 0 {
				return op.Param, true
			}
		}
	}
	return "", false
}

// ParamMins returns the minimum required value per parameter (for display of
// job requirements).
func (c Conditions) ParamMins() map[string]int {
	m := map[string]int{}
	for _, p := range c.Preds {
		if p.Kind == "param_gte" {
			if int(p.Value) > m[p.Param] {
				m[p.Param] = int(p.Value)
			}
		}
	}
	return m
}

// MoneyMin returns the highest money_gte requirement (0 if none).
func (c Conditions) MoneyMin() int64 {
	var min int64
	for _, p := range c.Preds {
		if p.Kind == "money_gte" && p.Value > min {
			min = p.Value
		}
	}
	return min
}

// ParamState is the current and maximum value of a parameter.
type ParamState struct {
	Value int
	Max   int
}

// State is a snapshot of a player used to evaluate conditions and effects.
type State struct {
	Money  int64
	Params map[string]ParamState
}

// Check reports whether all conditions hold. If not, it returns the first
// failing predicate so the caller can produce a suitable message.
func (c Conditions) Check(s State) (bool, *Pred) {
	for i := range c.Preds {
		p := &c.Preds[i]
		switch p.Kind {
		case "money_gte":
			if s.Money < p.Value {
				return false, p
			}
		case "param_gte":
			if int64(s.Params[p.Param].Value) < p.Value {
				return false, p
			}
		}
	}
	return true, nil
}

// ParamChange is the applied change to one parameter after clamping.
type ParamChange struct {
	Name     string `json:"name"`
	OldValue int    `json:"old_value"`
	NewValue int    `json:"new_value"`
}

// Plan is the concrete set of changes an effect produces for a given state.
type Plan struct {
	MoneyDelta int64         `json:"money_delta"`
	Params     []ParamChange `json:"params"`
}

// Plan computes the result of applying the effect to the state. Parameters are
// clamped to [0, max]. Money is returned as a raw delta; the ledger remains the
// source of truth for balances.
func (e Effect) Plan(s State) Plan {
	var plan Plan
	cur := make(map[string]int, len(s.Params))
	orig := make(map[string]int, len(s.Params))
	var order []string

	for _, op := range e.Ops {
		switch op.Kind {
		case "add_money":
			plan.MoneyDelta += op.Amount
		case "add_param":
			ps := s.Params[op.Param]
			if _, seen := orig[op.Param]; !seen {
				orig[op.Param] = ps.Value
				cur[op.Param] = ps.Value
				order = append(order, op.Param)
			}
			// [0, max] にクランプ
			cur[op.Param] = max(0, min(cur[op.Param]+int(op.Amount), ps.Max))
		}
	}

	for _, name := range order {
		plan.Params = append(plan.Params, ParamChange{
			Name:     name,
			OldValue: orig[name],
			NewValue: cur[name],
		})
	}
	return plan
}
