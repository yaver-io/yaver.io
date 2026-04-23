package main

import "path/filepath"

func hasCloudflareDeployConfig(dir string) bool {
	return hasFile(dir, "wrangler.toml") || hasFile(dir, "wrangler.jsonc")
}

func hasVercelDeployConfig(dir string) bool {
	return hasFile(dir, "vercel.json") || hasFile(dir, ".vercel")
}

func hasNextConfig(dir string) bool {
	return hasFile(dir, "next.config.js") || hasFile(dir, "next.config.mjs") || hasFile(dir, "next.config.ts")
}

func detectWebDeployPlatform(dir string) (platform string, command string) {
	switch {
	case hasCloudflareDeployConfig(dir):
		return "cloudflare", "npm run deploy"
	case hasVercelDeployConfig(dir):
		return "vercel", "npx vercel --prod --yes"
	default:
		return "", ""
	}
}

func dirHasCloudflareDeployConfig(dir string) bool {
	return fileExists(filepath.Join(dir, "wrangler.toml")) || fileExists(filepath.Join(dir, "wrangler.jsonc"))
}

func dirHasVercelDeployConfig(dir string) bool {
	return fileExists(filepath.Join(dir, "vercel.json")) || fileExists(filepath.Join(dir, ".vercel"))
}
