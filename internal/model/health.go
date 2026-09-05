package model

// SubsystemHealth records assessment and coverage separately: a finding remains
// actionable even when another probe in the same subsystem was unavailable.
type SubsystemHealth struct {
	Name     string `json:"name"`
	Status   string `json:"status" jsonschema:"enum=ok,enum=note,enum=warn,enum=fail,enum=unknown,enum=not_applicable"`
	Complete bool   `json:"complete"`
	Reason   string `json:"reason,omitempty"`
}

// Subsystem returns the recorded assessment, including for offline snapshots.
// Older snapshots did not record coverage, so absence never means healthy.
func (h Health) Subsystem(name string) SubsystemHealth {
	for _, subsystem := range h.Subsystems {
		if subsystem.Name == name {
			return subsystem
		}
	}
	return SubsystemHealth{Name: name, Status: "unknown", Reason: "Assessment not recorded in this snapshot"}
}

func (h Health) State() string {
	if h.Critical > 0 {
		return "CRITICAL"
	}
	if h.Warnings > 0 {
		return "ATTENTION"
	}
	if h.Unknown > 0 || len(h.Subsystems) == 0 {
		return "PARTIAL"
	}
	if h.Notes > 0 {
		return "REVIEW"
	}
	return "HEALTHY"
}

// BlockingChain is a captured row-lock graph rooted at a blocking transaction.
// Edges are observations; absent owners and cycles are explicitly incomplete.
type BlockingChain struct {
	RootTransaction string        `json:"root_transaction"`
	WaiterCount     int           `json:"waiter_count"`
	Transactions    []Transaction `json:"transactions"`
	Edges           []LockWait    `json:"edges"`
	Complete        bool          `json:"complete"`
	Caveats         []string      `json:"caveats"`
}
