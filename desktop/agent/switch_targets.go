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
	FamilyPostgres   SwitchFamily = "postgres"
	FamilySQLite     SwitchFamily = "sqlite"
	FamilyConvex     SwitchFamily = "convex"
	FamilyPocketBase SwitchFamily = "pocketbase"
	FamilyAppwrite   SwitchFamily = "appwrite"
	FamilyApp        SwitchFamily = "app"
)

type TargetHost string

const (
	HostLocalDocker   TargetHost = "local-docker"
	HostLocalProcess  TargetHost = "local-process"
	HostConvexCloud   TargetHost = "convex-cloud"
	HostSupabaseCloud TargetHost = "supabase-cloud"
	HostNeon          TargetHost = "neon"
	HostRDS           TargetHost = "aws-rds"
	HostCloudSQL      TargetHost = "gcp-cloudsql"
	HostTurso         TargetHost = "turso"
	HostCloudflareD1  TargetHost = "cloudflare-d1"
	HostHetzner       TargetHost = "hetzner"
	HostYaverCloud    TargetHost = "yaver-cloud"
	HostDOManaged     TargetHost = "do-managed-pg"
	HostVercel        TargetHost = "vercel"
	HostFly           TargetHost = "fly"
	HostCFWorkers     TargetHost = "cloudflare-workers"
	HostRailway       TargetHost = "railway"
	HostRender        TargetHost = "render"
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
	return []SwitchTarget{
		{ID: "convex-local", Label: "Convex (local Docker)", Family: FamilyConvex, Host: HostLocalDocker, Backend: BackendConvex, Description: "Self-hosted Convex on your machine", Cost: "$0"},
		{ID: "convex-cloud", Label: "Convex Cloud", Family: FamilyConvex, Host: HostConvexCloud, Backend: BackendConvex, Description: "Managed Convex at convex.dev", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "convex"},
		{ID: "postgres-local", Label: "Postgres (local Docker)", Family: FamilyPostgres, Host: HostLocalDocker, Backend: BackendPostgres, Description: "Plain Postgres 16", Cost: "$0"},
		{ID: "supabase-local", Label: "Supabase (local Docker)", Family: FamilyPostgres, Host: HostLocalDocker, Backend: BackendSupabase, Description: "Supabase stack via `supabase start`", Cost: "$0"},
		{ID: "supabase-cloud", Label: "Supabase Cloud", Family: FamilyPostgres, Host: HostSupabaseCloud, Backend: BackendSupabase, Description: "Managed Supabase at supabase.com", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "supabase"},
		{ID: "postgres-neon", Label: "Neon", Family: FamilyPostgres, Host: HostNeon, Backend: BackendPostgres, Description: "Serverless Postgres", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "neon"},
		{ID: "postgres-rds", Label: "AWS RDS Postgres", Family: FamilyPostgres, Host: HostRDS, Backend: BackendPostgres, Description: "Managed Postgres on AWS", Cost: "~$15/mo", RequiresAcct: true, AccountKey: "aws"},
		{ID: "postgres-cloudsql", Label: "GCP Cloud SQL Postgres", Family: FamilyPostgres, Host: HostCloudSQL, Backend: BackendPostgres, Description: "Managed Postgres on GCP", Cost: "~$10/mo", RequiresAcct: true, AccountKey: "gcp"},
		{ID: "postgres-do", Label: "DigitalOcean Managed Postgres", Family: FamilyPostgres, Host: HostDOManaged, Backend: BackendPostgres, Description: "Managed Postgres on DO", Cost: "~$15/mo", RequiresAcct: true, AccountKey: "digitalocean"},
		{ID: "sqlite-local", Label: "SQLite (file)", Family: FamilySQLite, Host: HostLocalProcess, Backend: BackendSQLite, Description: "Single file, zero services", Cost: "$0"},
		{ID: "sqlite-turso", Label: "Turso", Family: FamilySQLite, Host: HostTurso, Backend: BackendSQLite, Description: "Managed LibSQL on the edge", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "turso"},
		{ID: "sqlite-d1", Label: "Cloudflare D1", Family: FamilySQLite, Host: HostCloudflareD1, Backend: BackendSQLite, Description: "SQLite on Cloudflare edge", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "cloudflare"},
		{ID: "hetzner", Label: "Hetzner VPS (self-host)", Family: FamilyApp, Host: HostHetzner, Description: "Your Hetzner VPS, Yaver installs Docker", Cost: "€4–€20/mo", RequiresAcct: true, AccountKey: "hetzner"},
		{ID: "yaver-cloud", Label: "Yaver Cloud", Family: FamilyApp, Host: HostYaverCloud, Description: "Managed infrastructure", Cost: "$9/mo", RequiresAcct: true, AccountKey: "yaver"},
		{ID: "vercel", Label: "Vercel", Family: FamilyApp, Host: HostVercel, Description: "Deploy app to Vercel", Cost: "$0 hobby", RequiresAcct: true, AccountKey: "vercel"},
		{ID: "fly", Label: "Fly.io", Family: FamilyApp, Host: HostFly, Description: "Deploy Docker container to Fly", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "fly"},
		{ID: "cloudflare-workers", Label: "Cloudflare Workers", Family: FamilyApp, Host: HostCFWorkers, Description: "Deploy to CF Workers", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "cloudflare"},
		{ID: "railway", Label: "Railway", Family: FamilyApp, Host: HostRailway, Description: "All-in-one deploy", Cost: "$5/mo", RequiresAcct: true, AccountKey: "railway"},
		{ID: "render", Label: "Render", Family: FamilyApp, Host: HostRender, Description: "Managed web services", Cost: "$0 free tier", RequiresAcct: true, AccountKey: "render"},
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
		if fromFamily == FamilyConvex || to.Family == FamilyConvex ||
			fromFamily == FamilyPocketBase || to.Family == FamilyPocketBase ||
			fromFamily == FamilyAppwrite || to.Family == FamilyAppwrite {
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
	case BackendPocketBase:
		return FamilyPocketBase
	case BackendAppwrite:
		return FamilyAppwrite
	}
	return ""
}
