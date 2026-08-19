package firewall

// Internal test: Forecaster.Compute takes the unexported sample type, and
// exporting it purely to test the arithmetic would widen the API for no
// benefit. Everything else in this package is tested from outside.

import (
	"testing"
	"time"
)

func at(base time.Time, secs int) time.Time { return base.Add(time.Duration(secs) * time.Second) }

func TestInsufficientHistoryNeverDivides(t *testing.T) {
	f := DefaultForecaster()
	now := time.Now()

	for name, samples := range map[string][]sample{
		"no samples": nil,
		"one sample": {{at: now, cost: 0.5}},
	} {
		s := f.Compute(now, 0.5, 2.0, samples)
		if s.State != StateInsufficient {
			t.Fatalf("%s: expected %s, got %s", name, StateInsufficient, s.State)
		}
		if s.BurnRatePerMin != 0 {
			t.Fatalf("%s: expected no burn rate from insufficient history, got %v", name, s.BurnRatePerMin)
		}
		if s.TimeToBreachSeconds != 0 {
			t.Fatalf("%s: expected no time-to-breach, got %v", name, s.TimeToBreachSeconds)
		}
		if s.ProjectedTotal != 0.5 {
			t.Fatalf("%s: expected the projection to fall back to current cost, got %v", name, s.ProjectedTotal)
		}
	}
}

// TestIdenticalTimestampsDoNotDivideByZero: several samples in the same instant
// give a zero-width window.
func TestIdenticalTimestampsDoNotDivideByZero(t *testing.T) {
	now := time.Now()
	s := DefaultForecaster().Compute(now, 0.5, 2.0, []sample{
		{at: now, cost: 0.25}, {at: now, cost: 0.25},
	})
	if s.State != StateInsufficient {
		t.Fatalf("Expected a zero-width window to report insufficient history, got %s", s.State)
	}
}

func TestZeroBurnRateIsNotABreach(t *testing.T) {
	now := time.Now()
	// Two samples, both zero cost: time passed, nothing was spent.
	s := DefaultForecaster().Compute(now, 0.9, 1.0, []sample{
		{at: at(now, -120), cost: 0}, {at: at(now, -60), cost: 0},
	})
	if s.WillBreach {
		t.Fatal("Expected no breach when nothing is being spent, however close to the budget")
	}
	if s.TimeToBreachSeconds != 0 {
		t.Fatalf("Expected no finite time-to-breach at zero burn, got %v", s.TimeToBreachSeconds)
	}
	if s.State != StateStable {
		t.Fatalf("Expected stable, got %s", s.State)
	}
}

func TestZeroBudgetIsHardLimit(t *testing.T) {
	s := DefaultForecaster().Compute(time.Now(), 0, 0, nil)
	if s.State != StateHardLimit || !s.WillBreach {
		t.Fatalf("Expected a zero budget to authorise no spending, got %+v", s)
	}
}

func TestNegativeRemainingIsHardLimit(t *testing.T) {
	s := DefaultForecaster().Compute(time.Now(), 3.0, 2.0, nil)
	if s.State != StateHardLimit || !s.WillBreach {
		t.Fatalf("Expected an overspent session to be at the hard limit, got %+v", s)
	}
	if s.TimeToBreachSeconds != 0 {
		t.Fatalf("Expected no time-to-breach once already breached, got %v", s.TimeToBreachSeconds)
	}
}

// TestRisingCostProjectsBreach exercises the arithmetic with numbers that can
// be checked by hand: $0.60 over 60s is $0.60/min; $1.40 remaining is 140s.
func TestRisingCostProjectsBreach(t *testing.T) {
	now := time.Now()
	samples := []sample{
		{at: at(now, -60), cost: 0.20},
		{at: at(now, -40), cost: 0.20},
		{at: at(now, -20), cost: 0.20},
	}
	s := DefaultForecaster().Compute(now, 0.60, 2.0, samples)

	if got := s.BurnRatePerMin; got < 0.59 || got > 0.61 {
		t.Fatalf("Expected a burn rate near $0.60/min, got %v", got)
	}
	if got := s.TimeToBreachSeconds; got < 135 || got > 145 {
		t.Fatalf("Expected ~140s to breach, got %v", got)
	}
	if s.ProjectedTotal <= s.CurrentCost {
		t.Fatalf("Expected the projection to exceed current cost, got %v", s.ProjectedTotal)
	}
}

func TestStableCostBelowSoftLimit(t *testing.T) {
	now := time.Now()
	samples := []sample{
		{at: at(now, -600), cost: 0.001},
		{at: at(now, -300), cost: 0.001},
	}
	s := DefaultForecaster().Compute(now, 0.002, 10.0, samples)
	if s.State != StateStable {
		t.Fatalf("Expected a trickle of spend against a large budget to be stable, got %s (%+v)", s.State, s)
	}
	if s.WillBreach {
		t.Fatal("Expected no breach projected")
	}
}

func TestSoftLimitRecommendsReroute(t *testing.T) {
	now := time.Now()
	samples := []sample{
		{at: at(now, -60), cost: 0.5},
		{at: at(now, -30), cost: 0.5},
	}
	s := DefaultForecaster().Compute(now, 1.0, 2.0, samples)
	if s.State != StateSoftLimit {
		t.Fatalf("Expected the soft limit, got %s (%+v)", s.State, s)
	}
	if s.Recommend == "" {
		t.Fatal("Expected a recommendation at the soft limit rather than a bare block")
	}
}

func TestHardLimitAtFullBudget(t *testing.T) {
	now := time.Now()
	samples := []sample{{at: at(now, -60), cost: 1.0}, {at: at(now, -30), cost: 1.0}}
	s := DefaultForecaster().Compute(now, 2.0, 2.0, samples)
	if s.State != StateHardLimit {
		t.Fatalf("Expected the hard limit once spend reaches budget, got %s", s.State)
	}
}

func TestProjectionIsBoundedByTimeToBreach(t *testing.T) {
	now := time.Now()
	// Very high burn against a small remaining budget: projecting a full 5-min
	// horizon would report a nonsense figure far past the limit.
	samples := []sample{{at: at(now, -10), cost: 1.0}, {at: at(now, -5), cost: 1.0}}
	s := DefaultForecaster().Compute(now, 1.9, 2.0, samples)
	if s.ProjectedTotal > 2.5 {
		t.Fatalf("Expected the projection to be bounded near the breach point, got %v", s.ProjectedTotal)
	}
}
