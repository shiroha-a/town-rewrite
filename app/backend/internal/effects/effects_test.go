package effects

import "testing"

func TestParseEffect(t *testing.T) {
	eff, err := ParseEffect([]byte(`[{"op":"add_money","amount":1000},{"op":"add_param","param":"energy","amount":-1}]`))
	if err != nil {
		t.Fatalf("ParseEffect: %v", err)
	}
	if len(eff.Ops) != 2 {
		t.Fatalf("ops = %d, want 2", len(eff.Ops))
	}
}

func TestParseEffectRejectsUnknown(t *testing.T) {
	if _, err := ParseEffect([]byte(`[{"op":"delete_universe"}]`)); err == nil {
		t.Error("expected error for unknown op")
	}
	if _, err := ParseEffect([]byte(`[{"op":"add_param","param":"mana","amount":1}]`)); err == nil {
		t.Error("expected error for unknown param")
	}
}

func TestParseConditionsRejectsUnknown(t *testing.T) {
	if _, err := ParseConditions([]byte(`[{"pred":"always_true"}]`)); err == nil {
		t.Error("expected error for unknown pred")
	}
}

func TestConditionsCheck(t *testing.T) {
	conds, err := ParseConditions([]byte(`[{"pred":"param_gte","param":"energy","value":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	pass := State{Params: map[string]ParamState{"energy": {Value: 3, Max: 10}}}
	if ok, _ := conds.Check(pass); !ok {
		t.Error("expected conditions to pass with energy=3")
	}
	fail := State{Params: map[string]ParamState{"energy": {Value: 0, Max: 10}}}
	ok, failed := conds.Check(fail)
	if ok {
		t.Error("expected conditions to fail with energy=0")
	}
	if failed == nil || failed.Param != "energy" {
		t.Errorf("failed pred = %+v, want energy", failed)
	}
}

func TestEffectPlanClamps(t *testing.T) {
	eff, err := ParseEffect([]byte(`[{"op":"add_money","amount":1000},{"op":"add_param","param":"energy","amount":-5}]`))
	if err != nil {
		t.Fatal(err)
	}
	// energy 2 - 5 は 0 で下限クランプ。
	plan := eff.Plan(State{Money: 500, Params: map[string]ParamState{"energy": {Value: 2, Max: 10}}})
	if plan.MoneyDelta != 1000 {
		t.Errorf("money delta = %d, want 1000", plan.MoneyDelta)
	}
	if len(plan.Params) != 1 || plan.Params[0].NewValue != 0 {
		t.Errorf("param plan = %+v, want energy -> 0", plan.Params)
	}
	if plan.Params[0].OldValue != 2 {
		t.Errorf("param old value = %d, want 2", plan.Params[0].OldValue)
	}
}

func TestEffectPlanClampsToMax(t *testing.T) {
	eff, _ := ParseEffect([]byte(`[{"op":"add_param","param":"energy","amount":100}]`))
	plan := eff.Plan(State{Params: map[string]ParamState{"energy": {Value: 8, Max: 10}}})
	if plan.Params[0].NewValue != 10 {
		t.Errorf("new value = %d, want 10 (max clamp)", plan.Params[0].NewValue)
	}
}
