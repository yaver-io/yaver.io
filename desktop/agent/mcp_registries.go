package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	osexec "os/exec"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Docker Hub — search images, get tags, image info
// ---------------------------------------------------------------------------

func mcpDockerHubSearch(query string, limit int) interface{} {
	if limit <= 0 {
		limit = 10
	}
	u := fmt.Sprintf("https://hub.docker.com/v2/search/repositories/?query=%s&page_size=%d",
		url.QueryEscape(query), limit)
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if results, ok := m["results"].([]interface{}); ok {
			var simplified []map[string]interface{}
			for _, r := range results {
				if rm, ok := r.(map[string]interface{}); ok {
					simplified = append(simplified, map[string]interface{}{
						"name":        rm["repo_name"],
						"description": rm["short_description"],
						"stars":       rm["star_count"],
						"pulls":       rm["pull_count"],
						"official":    rm["is_official"],
					})
				}
			}
			return map[string]interface{}{"images": simplified, "count": len(simplified)}
		}
	}
	return data
}

func mcpDockerHubTags(image string, limit int) interface{} {
	if limit <= 0 {
		limit = 10
	}
	parts := strings.SplitN(image, "/", 2)
	var u string
	if len(parts) == 1 {
		u = fmt.Sprintf("https://hub.docker.com/v2/repositories/library/%s/tags/?page_size=%d&ordering=last_updated", image, limit)
	} else {
		u = fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags/?page_size=%d&ordering=last_updated", image, limit)
	}
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if results, ok := m["results"].([]interface{}); ok {
			var tags []map[string]interface{}
			for _, r := range results {
				if rm, ok := r.(map[string]interface{}); ok {
					tags = append(tags, map[string]interface{}{
						"name":        rm["name"],
						"size":        rm["full_size"],
						"last_pushed": rm["last_updated"],
						"digest":      rm["digest"],
					})
				}
			}
			return map[string]interface{}{"image": image, "tags": tags, "count": len(tags)}
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// PyPI — search, package info, versions
// ---------------------------------------------------------------------------

func mcpPyPIInfo(pkg string) interface{} {
	u := fmt.Sprintf("https://pypi.org/pypi/%s/json", url.PathEscape(pkg))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		info, _ := m["info"].(map[string]interface{})
		return map[string]interface{}{
			"name":        info["name"],
			"version":     info["version"],
			"summary":     info["summary"],
			"author":      info["author"],
			"license":     info["license"],
			"home_page":   info["home_page"],
			"project_url": info["project_url"],
			"requires_python": info["requires_python"],
		}
	}
	return data
}

func mcpPyPIVersions(pkg string) interface{} {
	u := fmt.Sprintf("https://pypi.org/pypi/%s/json", url.PathEscape(pkg))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if releases, ok := m["releases"].(map[string]interface{}); ok {
			var versions []string
			for v := range releases {
				versions = append(versions, v)
			}
			// Return last 20
			if len(versions) > 20 {
				versions = versions[len(versions)-20:]
			}
			return map[string]interface{}{"package": pkg, "versions": versions, "count": len(versions)}
		}
	}
	return data
}

func mcpPyPISearch(query string) interface{} {
	// PyPI doesn't have a search API anymore, use pip search alternative
	out, err := runCmd("pip", "index", "versions", query)
	if err != nil {
		// Fallback to simple package lookup
		return mcpPyPIInfo(query)
	}
	return map[string]interface{}{"output": out}
}

// ---------------------------------------------------------------------------
// npm — search, package info (already have mcpNPMInfo, add search)
// ---------------------------------------------------------------------------

func mcpNPMSearch(query string, limit int) interface{} {
	if limit <= 0 {
		limit = 10
	}
	u := fmt.Sprintf("https://registry.npmjs.org/-/v1/search?text=%s&size=%d",
		url.QueryEscape(query), limit)
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if objects, ok := m["objects"].([]interface{}); ok {
			var packages []map[string]interface{}
			for _, o := range objects {
				if om, ok := o.(map[string]interface{}); ok {
					if pkg, ok := om["package"].(map[string]interface{}); ok {
						packages = append(packages, map[string]interface{}{
							"name":        pkg["name"],
							"version":     pkg["version"],
							"description": pkg["description"],
						})
					}
				}
			}
			return map[string]interface{}{"packages": packages, "count": len(packages)}
		}
	}
	return data
}

func mcpNPMVersions(pkg string) interface{} {
	u := fmt.Sprintf("https://registry.npmjs.org/%s", url.PathEscape(pkg))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if versions, ok := m["versions"].(map[string]interface{}); ok {
			var versionList []string
			for v := range versions {
				versionList = append(versionList, v)
			}
			if len(versionList) > 20 {
				versionList = versionList[len(versionList)-20:]
			}
			distTags, _ := m["dist-tags"].(map[string]interface{})
			return map[string]interface{}{"package": pkg, "versions": versionList, "dist_tags": distTags, "count": len(versionList)}
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// crates.io — Rust package registry
// ---------------------------------------------------------------------------

func mcpCratesInfo(crate string) interface{} {
	u := fmt.Sprintf("https://crates.io/api/v1/crates/%s", url.PathEscape(crate))
	headers := map[string]string{"User-Agent": "Yaver-MCP/1.0"}
	data := registryGET(u, headers)
	if m, ok := data.(map[string]interface{}); ok {
		if c, ok := m["crate"].(map[string]interface{}); ok {
			return map[string]interface{}{
				"name":        c["name"],
				"description": c["description"],
				"downloads":   c["downloads"],
				"max_version": c["max_version"],
				"repository":  c["repository"],
				"homepage":    c["homepage"],
				"categories":  c["categories"],
			}
		}
	}
	return data
}

func mcpCratesSearch(query string, limit int) interface{} {
	if limit <= 0 {
		limit = 10
	}
	u := fmt.Sprintf("https://crates.io/api/v1/crates?q=%s&per_page=%d",
		url.QueryEscape(query), limit)
	headers := map[string]string{"User-Agent": "Yaver-MCP/1.0"}
	data := registryGET(u, headers)
	if m, ok := data.(map[string]interface{}); ok {
		if crates, ok := m["crates"].([]interface{}); ok {
			var results []map[string]interface{}
			for _, c := range crates {
				if cm, ok := c.(map[string]interface{}); ok {
					results = append(results, map[string]interface{}{
						"name":        cm["name"],
						"description": cm["description"],
						"downloads":   cm["downloads"],
						"version":     cm["max_version"],
					})
				}
			}
			return map[string]interface{}{"crates": results, "count": len(results)}
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// Go modules — pkg.go.dev
// ---------------------------------------------------------------------------

func mcpGoModuleInfo(module string) interface{} {
	u := fmt.Sprintf("https://proxy.golang.org/%s/@latest", url.PathEscape(module))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		return map[string]interface{}{
			"module":  module,
			"version": m["Version"],
			"time":    m["Time"],
			"doc_url": fmt.Sprintf("https://pkg.go.dev/%s", module),
		}
	}
	return data
}

func mcpGoModuleVersions(module string) interface{} {
	u := fmt.Sprintf("https://proxy.golang.org/%s/@v/list", url.PathEscape(module))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	versions := strings.Split(strings.TrimSpace(string(body)), "\n")
	return map[string]interface{}{"module": module, "versions": versions, "count": len(versions)}
}

// ---------------------------------------------------------------------------
// pub.dev — Dart/Flutter packages
// ---------------------------------------------------------------------------

func mcpPubDevInfo(pkg string) interface{} {
	u := fmt.Sprintf("https://pub.dev/api/packages/%s", url.PathEscape(pkg))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if latest, ok := m["latest"].(map[string]interface{}); ok {
			if pubspec, ok := latest["pubspec"].(map[string]interface{}); ok {
				return map[string]interface{}{
					"name":        pubspec["name"],
					"version":     pubspec["version"],
					"description": pubspec["description"],
					"homepage":    pubspec["homepage"],
					"repository":  pubspec["repository"],
				}
			}
		}
	}
	return data
}

func mcpPubDevSearch(query string) interface{} {
	u := fmt.Sprintf("https://pub.dev/api/search?q=%s", url.QueryEscape(query))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if packages, ok := m["packages"].([]interface{}); ok {
			var results []string
			for _, p := range packages {
				if pm, ok := p.(map[string]interface{}); ok {
					if name, ok := pm["package"].(string); ok {
						results = append(results, name)
					}
				}
			}
			return map[string]interface{}{"packages": results, "count": len(results)}
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// Homebrew — formulae.brew.sh API
// ---------------------------------------------------------------------------

func mcpBrewInfo(formula string) interface{} {
	u := fmt.Sprintf("https://formulae.brew.sh/api/formula/%s.json", url.PathEscape(formula))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		versions, _ := m["versions"].(map[string]interface{})
		return map[string]interface{}{
			"name":         m["name"],
			"description":  m["desc"],
			"homepage":     m["homepage"],
			"version":      versions["stable"],
			"license":      m["license"],
			"dependencies": m["dependencies"],
			"installs_30d": m["analytics"],
		}
	}
	return data
}

func mcpBrewSearch(query string) interface{} {
	out, err := runCmd("brew", "search", query)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	results := strings.Split(strings.TrimSpace(out), "\n")
	return map[string]interface{}{"results": results, "count": len(results)}
}

// ---------------------------------------------------------------------------
// apt — search, show, list installed
// ---------------------------------------------------------------------------

func mcpAptSearch(query string) interface{} {
	out, err := runCmd("apt", "search", query)
	if err != nil {
		out, err = runCmd("apt-cache", "search", query)
		if err != nil {
			return map[string]interface{}{"error": "apt not available (Linux only)"}
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 20 {
		lines = lines[:20]
	}
	return map[string]interface{}{"results": lines, "count": len(lines)}
}

func mcpAptShow(pkg string) interface{} {
	out, err := runCmd("apt", "show", pkg)
	if err != nil {
		out, err = runCmd("apt-cache", "show", pkg)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	return map[string]interface{}{"info": out}
}

// ---------------------------------------------------------------------------
// pip — search, show, list
// ---------------------------------------------------------------------------

func mcpPipShow(pkg string) interface{} {
	out, err := runCmd("pip", "show", pkg)
	if err != nil {
		out, err = runCmd("pip3", "show", pkg)
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	return map[string]interface{}{"info": out}
}

func mcpPipList() interface{} {
	out, err := runCmd("pip", "list", "--format", "json")
	if err != nil {
		out, err = runCmd("pip3", "list", "--format", "json")
		if err != nil {
			return map[string]interface{}{"error": err.Error()}
		}
	}
	var result interface{}
	json.Unmarshal([]byte(out), &result)
	return map[string]interface{}{"packages": result}
}

// ---------------------------------------------------------------------------
// cargo — search, info
// ---------------------------------------------------------------------------

func mcpCargoSearch(query string) interface{} {
	cmd := osexec.Command("cargo", "search", query, "--limit", "10")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"results": string(out)}
}

// ---------------------------------------------------------------------------
// gem — Ruby gems
// ---------------------------------------------------------------------------

func mcpGemInfo(gem string) interface{} {
	u := fmt.Sprintf("https://rubygems.org/api/v1/gems/%s.json", url.PathEscape(gem))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		return map[string]interface{}{
			"name":        m["name"],
			"version":     m["version"],
			"info":        m["info"],
			"downloads":   m["downloads"],
			"homepage_uri": m["homepage_uri"],
			"source_code_uri": m["source_code_uri"],
		}
	}
	return data
}

func mcpGemSearch(query string) interface{} {
	u := fmt.Sprintf("https://rubygems.org/api/v1/search.json?query=%s", url.QueryEscape(query))
	data := registryGET(u, nil)
	if arr, ok := data.([]interface{}); ok {
		var results []map[string]interface{}
		for _, g := range arr {
			if gm, ok := g.(map[string]interface{}); ok {
				results = append(results, map[string]interface{}{
					"name":      gm["name"],
					"version":   gm["version"],
					"info":      gm["info"],
					"downloads": gm["downloads"],
				})
			}
		}
		if len(results) > 10 {
			results = results[:10]
		}
		return map[string]interface{}{"gems": results, "count": len(results)}
	}
	return data
}

// ---------------------------------------------------------------------------
// Maven — Java/Kotlin packages (search.maven.org)
// ---------------------------------------------------------------------------

func mcpMavenSearch(query string) interface{} {
	u := fmt.Sprintf("https://search.maven.org/solrsearch/select?q=%s&rows=10&wt=json",
		url.QueryEscape(query))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if resp, ok := m["response"].(map[string]interface{}); ok {
			if docs, ok := resp["docs"].([]interface{}); ok {
				var results []map[string]interface{}
				for _, d := range docs {
					if dm, ok := d.(map[string]interface{}); ok {
						results = append(results, map[string]interface{}{
							"group":    dm["g"],
							"artifact": dm["a"],
							"version":  dm["latestVersion"],
						})
					}
				}
				return map[string]interface{}{"artifacts": results, "count": len(results)}
			}
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// NuGet — .NET packages
// ---------------------------------------------------------------------------

func mcpNuGetSearch(query string) interface{} {
	u := fmt.Sprintf("https://azuresearch-usnc.nuget.org/query?q=%s&take=10",
		url.QueryEscape(query))
	data := registryGET(u, nil)
	if m, ok := data.(map[string]interface{}); ok {
		if results, ok := m["data"].([]interface{}); ok {
			var packages []map[string]interface{}
			for _, r := range results {
				if rm, ok := r.(map[string]interface{}); ok {
					packages = append(packages, map[string]interface{}{
						"id":          rm["id"],
						"version":     rm["version"],
						"description": rm["description"],
						"downloads":   rm["totalDownloads"],
					})
				}
			}
			return map[string]interface{}{"packages": packages, "count": len(packages)}
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// Package install wrappers
// ---------------------------------------------------------------------------

func mcpPkgInstall(manager, pkg string, global bool) interface{} {
	var args []string
	switch manager {
	case "npm":
		args = []string{"install", pkg}
		if global {
			args = []string{"install", "-g", pkg}
		}
		return pkgRun("npm", args)
	case "pip", "pip3":
		args = []string{"install", pkg}
		return pkgRun("pip3", args)
	case "cargo":
		args = []string{"install", pkg}
		return pkgRun("cargo", args)
	case "go":
		args = []string{"install", pkg + "@latest"}
		return pkgRun("go", args)
	case "brew":
		args = []string{"install", pkg}
		return pkgRun("brew", args)
	case "gem":
		args = []string{"install", pkg}
		return pkgRun("gem", args)
	case "apt":
		// Helper-first (root helper validates the package name + execs as root
		// on confined operator nodes), scoped-sudo fallback elsewhere.
		out, err := privilegedPackageInstall("apt", []string{pkg})
		if err != nil {
			return map[string]interface{}{"error": err.Error(), "output": out}
		}
		return map[string]interface{}{"ok": true, "output": out}
	case "flutter", "dart":
		args = []string{"pub", "add", pkg}
		return pkgRun("dart", args)
	default:
		return map[string]interface{}{"error": "manager must be: npm, pip, cargo, go, brew, gem, apt, dart"}
	}
}

func pkgRun(name string, args []string) interface{} {
	cmd := osexec.Command(name, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": string(out)}
	}
	return map[string]interface{}{"ok": true, "output": string(out)}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func registryGET(urlStr string, headers map[string]string) interface{} {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", urlStr, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Yaver-MCP/1.0")
	}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result interface{}
	json.Unmarshal(body, &result)
	if result == nil {
		return map[string]interface{}{"raw": string(body)}
	}
	return result
}
