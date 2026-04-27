package main

import "fmt"

type SwitchComplexity string

const (
	ComplexityTrivial SwitchComplexity = "trivial"
	ComplexityEasy    SwitchComplexity = "easy"
	ComplexityMedium  SwitchComplexity = "medium"
	ComplexityHard    SwitchComplexity = "hard"
)

type SwitchFamily string

const (
	FamilyPostgres SwitchFamily = "postgres"
	FamilySQLite   SwitchFamily = "sqlite"
	FamilyConvex   SwitchFamily = "convex"
	FamilyApp      SwitchFamily = "app"
)

type TargetHost string

const (
	HostLocalDocker   TargetHost = "local-docker"
	HostLocalProcess  TargetHost = "local-process"
	HostConvexCloud   TargetHost = "convex-cloud"
	HostSupabaseCloud TargetHost = "supabase-cloud"
	HostYaverCloud    TargetHost = "yaver-cloud"
	HostVercel        TargetHost = "vercel"
	HostCFWorkers     TargetHost = "cloudflare-workers"
)

type SwitchTarget struct {
	ID           string       `json:"id"`
	Label        string       `json:"label"`
	Family       SwitchFamily `json:"family"`
	Host         TargetHost   `json:"host"`
	Backend      BackendKind  `json:"backend,omitempty"`
	Description  string       `json:"description"`
	Cost         string       `json:"cost"`
	RequiresAcct bool         `json:"requiresAccount"`
	AccountKey   string       `json:"accountKey,omitempty"`
}

func SwitchTargets() []SwitchTarget {
	// Lean target set (2026-04-28). First-class deploy: Convex Cloud,
	// Cloudflare Workers, Yaver Cloud. Export-only escape routes:
	// Supabase Cloud, Vercel. Mobile builds use the dedicated TestFlight
	// / Play-internal flow in deploy_script_gen.go, not the switch
	// engine, so they don't appear here.
	return []SwitchTarget{
		{ID: "convex-cloud", Label: "Convex Cloud", Family: FamilyConvex, Host: HostConvexCloud, Backend: BackendConvex, Description: "Managed Convex at convex.dev", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "convex"},
		{ID: "supabase-cloud", Label: "Supabase Cloud", Family: FamilyPostgres, Host: HostSupabaseCloud, Backend: BackendSupabase, Description: "Managed Supabase at supabase.com (export-only — escape route)", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "supabase"},
		{ID: "yaver-cloud", Label: "Yaver Cloud", Family: FamilyApp, Host: HostYaverCloud, Description: "Managed infrastructure", Cost: "$9/mo", RequiresAcct: true, AccountKey: "yaver"},
		{ID: "vercel", Label: "Vercel", Family: FamilyApp, Host: HostVercel, Description: "Deploy app to Vercel (export-only — escape route)", Cost: "$0 hobby", RequiresAcct: true, AccountKey: "vercel"},
		{ID: "cloudflare-workers", Label: "Cloudflare Workers", Family: FamilyApp, Host: HostCFWorkers, Description: "Deploy to CF Workers", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "cloudflare"},
	}
}

func SwitchTargetByID(id string) (*SwitchTarget, error) {
	for _, t := range SwitchTargets() {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("unknown switch target %q", id)
}

func AssessComplexity(from BackendKind, to SwitchTarget) SwitchComplexity {
	fromFamily := backendFamily(from)
	if to.Family == FamilyApp && to.Backend == "" {
		return ComplexityTrivial
	}
	if from == to.Backend && to.Host == HostLocalDocker {
		return ComplexityTrivial
	}
	if fromFamily != to.Family {
		if (fromFamily == FamilySQLite && to.Family == FamilyPostgres) ||
			(fromFamily == FamilyPostgres && to.Family == FamilySQLite) {
			return ComplexityMedium
		}
		if fromFamily == FamilyConvex || to.Family == FamilyConvex {
			return ComplexityHard
		}
		return ComplexityMedium
	}
	if fromFamily == FamilyPostgres && (from == BackendSupabase) != (to.Backend == BackendSupabase) {
		return ComplexityEasy
	}
	return ComplexityTrivial
}

func backendFamily(b BackendKind) SwitchFamily {
	switch b {
	case BackendPostgres, BackendSupabase:
		return FamilyPostgres
	case BackendSQLite:
		return FamilySQLite
	case BackendConvex:
		return FamilyConvex
	}
	return ""
}
