package main

import (
	"context"
	"net/http"
	"time"
)

// OverviewSummary is the compact payload feeding the AWS-style home card grid.
type OverviewSummary struct {
	Machines struct {
		Total   int `json:"total"`
		Online  int `json:"online"`
		Offline int `json:"offline"`
	} `json:"machines"`
	Projects struct {
		Total    int `json:"total"`
		Deployed int `json:"deployed"`
		Local    int `json:"local"`
	} `json:"projects"`
	Services struct {
		Running int `json:"running"`
		Stopped int `json:"stopped"`
	} `json:"services"`
	Alerts struct {
		Active   int    `json:"active"`
		Summary  string `json:"summary"`
	} `json:"alerts"`
	Cost struct {
		MonthlyUSD float64 `json:"monthlyUsd"`
		Breakdown  []CostLine `json:"breakdown"`
	} `json:"cost"`
	Uptime struct {
		Up     int     `json:"up"`
		Down   int     `json:"down"`
		Pct    float64 `json:"pct"`
	} `json:"uptime"`
	RecentActivity []ActivityEntry `json:"recentActivity"`
}

type CostLine struct {
	Provider string  `json:"provider"`
	Monthly  float64 `json:"monthly"`
}

type ActivityEntry struct {
	Timestamp string `json:"timestamp"`
	Icon      string `json:"icon"`
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
}

// BuildOverviewSummary gathers numbers from across the agent for the home page.
// Best-effort: missing data becomes zero/empty, never fails the whole response.
func BuildOverviewSummary(ctx context.Context) OverviewSummary {
	var s OverviewSummary

	// Machines.
	machines := listAllMachines(ctx)
	s.Machines.Total = len(machines)
	for _, m := range machines {
		if m.IsOnline {
			s.Machines.Online++
		} else {
			s.Machines.Offline++
		}
	}

	// Services — count Docker containers.
	if list, err := ListContainers(ctx, true); err == nil {
		for _, c := range list {
			if c.State == "running" {
				s.Services.Running++
			} else {
				s.Services.Stopped++
			}
		}
	}

	// Alerts — uptime monitors currently down + threshold alerts in recent window.
	if u, err := ensureUptime(); err == nil {
		if mons, err := u.list(); err == nil {
			for _, m := range mons {
				if m.Status == "down" {
					s.Alerts.Active++
				}
			}
		}
	}
	if s.Alerts.Active == 0 {
		s.Alerts.Summary = "All healthy"
	} else {
		s.Alerts.Summary = "Needs attention"
	}

	// Cost — sum existing known hosts. For the solo vibe coder this is
	// rough: $0 for own hardware, Hetzner line items, $9 Yaver Cloud, etc.
	// We piggyback on the per-target cost catalog.
	for _, m := range machines {
		switch m.Provider {
		case "hetzner":
			s.Cost.Breakdown = append(s.Cost.Breakdown, CostLine{Provider: "Hetzner (" + m.Name + ")", Monthly: 4.50})
			s.Cost.MonthlyUSD += 4.50
		case "aws":
			s.Cost.Breakdown = append(s.Cost.Breakdown, CostLine{Provider: "AWS (" + m.Name + ")", Monthly: 15.00})
			s.Cost.MonthlyUSD += 15.00
		}
	}

	// Uptime.
	if u, err := ensureUptime(); err == nil {
		if mons, err := u.list(); err == nil {
			for _, m := range mons {
				if m.Status == "up" {
					s.Uptime.Up++
				} else if m.Status == "down" {
					s.Uptime.Down++
				}
			}
			total := s.Uptime.Up + s.Uptime.Down
			if total > 0 {
				s.Uptime.Pct = float64(s.Uptime.Up) / float64(total) * 100
			} else {
				s.Uptime.Pct = 100
			}
		}
	}

	// Recent activity — last 6 audit entries.
	if a, err := ensureAudit(); err == nil {
		if list, err := a.List(6); err == nil {
			for _, e := range list {
				s.RecentActivity = append(s.RecentActivity, ActivityEntry{
					Timestamp: e.Timestamp.Format(time.RFC3339),
					Icon:      iconFor(e.Action, e.Outcome),
					Title:     humanizeAction(e.Action, e.Target),
					Detail:    e.Outcome,
				})
			}
		}
	}
	return s
}

func iconFor(action, outcome string) string {
	switch action {
	case "deploy":
		if outcome == "success" {
			return "✅"
		}
		return "❌"
	case "domain_add":
		return "🌐"
	case "secret_rotate":
		return "🔑"
	case "ci_run":
		if outcome == "passed" {
			return "✅"
		}
		return "❌"
	case "threshold_alert":
		return "⚠️"
	case "env_switch":
		return "🔄"
	case "multiregion_deploy":
		return "🌍"
	}
	return "•"
}

func humanizeAction(action, target string) string {
	switch action {
	case "deploy":
		return "Deploy · " + target
	case "domain_add":
		return "Added domain · " + target
	case "secret_rotate":
		return "Rotated secret · " + target
	case "ci_run":
		return "CI ran · " + target
	case "threshold_alert":
		return "Threshold alert · " + target
	case "env_switch":
		return "Environment switched · " + target
	}
	return action + " · " + target
}

func (s *HTTPServer) handleOverviewSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, BuildOverviewSummary(r.Context()))
}
