package firewall

import (
	"time"
)

// Forecast states, in increasing severity.
const (
	StateInsufficient = "insufficient_history"
	StateStable       = "stable"
	StateSoftLimit    = "soft_limit"
	StateHardLimit    = "hard_limit"
)

// Snapshot is a cost forecast at a point in time.
type Snapshot struct {
	CurrentCost    float64 `json:"current_cost"`
	Budget         float64 `json:"budget"`
	BurnRatePerMin float64 `json:"burn_rate_per_min"`
	ProjectedTotal float64 `json:"projected_total"`
	// TimeToBreachSeconds is 0 when a breach is not projected — either because
	// there is no burn, or because the budget is already gone.
	TimeToBreachSeconds float64 `json:"time_to_breach_seconds"`
	WillBreach          bool    `json:"will_breach"`
	State               string  `json:"state"`
	Recommend           string  `json:"recommend,omitempty"`
	Samples             int     `json:"samples"`
}

// Forecaster projects spend forward from observed burn.
//
// The method is deliberately a straight-line extrapolation over a rolling
// window, and is described as such in the README. It is not a model and makes
// no claim to be one: for a hackathon-scale governance decision, an operator
// being able to reproduce the number by hand is worth more than accuracy that
// cannot be explained when it blocks something.
type Forecaster struct {
	SoftLimitPct float64       // fraction of budget at which to recommend rerouting
	HardLimitPct float64       // fraction at which to block
	Horizon      time.Duration // how far ahead to project
}

// DefaultForecaster returns the shipped thresholds.
func DefaultForecaster() Forecaster {
	return Forecaster{SoftLimitPct: 0.80, HardLimitPct: 1.0, Horizon: 5 * time.Minute}
}

// Compute projects a session's spend.
//
// Every branch below is an edge case that would otherwise divide by zero or
// project a nonsense number, ordered so the cheapest disqualifier runs first.
func (f Forecaster) Compute(now time.Time, cost, budget float64, samples []sample) Snapshot {
	s := Snapshot{
		CurrentCost:    cost,
		Budget:         budget,
		ProjectedTotal: cost,
		Samples:        len(samples),
		State:          StateStable,
	}

	// A zero or negative budget authorises no spending at all. This is a policy
	// answer, not arithmetic — there is no rate at which it becomes acceptable.
	if budget <= 0 {
		s.State = StateHardLimit
		s.WillBreach = true
		return s
	}

	// Already over: no projection needed.
	remaining := budget - cost
	if remaining <= 0 {
		s.State = StateHardLimit
		s.WillBreach = true
		return s
	}

	// Fewer than two samples gives no interval to measure a rate over.
	// Reporting "insufficient history" is the honest answer; inventing a rate
	// from one point would put a fabricated projection on the dashboard.
	if len(samples) < 2 {
		s.State = StateInsufficient
		return s
	}

	window := now.Sub(samples[0].at)
	if window <= 0 {
		s.State = StateInsufficient
		return s
	}

	var windowCost float64
	for _, sm := range samples {
		windowCost += sm.cost
	}
	s.BurnRatePerMin = windowCost / window.Minutes()

	// Zero burn is not a breach, however close to the budget the session is.
	if s.BurnRatePerMin <= 0 {
		s.State = StateStable
		return s
	}

	minutesToBreach := remaining / s.BurnRatePerMin
	s.TimeToBreachSeconds = minutesToBreach * 60

	// Project over the shorter of the horizon and twice the time to breach, so
	// a session about to breach is not reported as spending far past its limit.
	projectMinutes := f.Horizon.Minutes()
	if m := minutesToBreach * 2; m < projectMinutes {
		projectMinutes = m
	}
	s.ProjectedTotal = cost + s.BurnRatePerMin*projectMinutes

	switch {
	case cost >= budget*f.HardLimitPct:
		s.State = StateHardLimit
		s.WillBreach = true
	case s.ProjectedTotal >= budget*f.SoftLimitPct:
		s.State = StateSoftLimit
		s.WillBreach = s.ProjectedTotal >= budget
		s.Recommend = "switch to a lower-cost model route"
	default:
		s.State = StateStable
	}

	return s
}
