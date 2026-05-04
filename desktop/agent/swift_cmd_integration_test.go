package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRunSwiftSubcommand_BuildAndTestTinyPackage exercises the
// runSwiftSubcommand happy path against a real swift toolchain.
// Skipped when swift is not on PATH (CI sandbox, fresh dev box
// without the toolchain installed). The fixture is intentionally
// dependency-free so `swift build` doesn't need to fetch anything
// and the test stays under the 60-second deadline on first run.
func TestRunSwiftSubcommand_BuildAndTestTinyPackage(t *testing.T) {
	if _, err := exec.LookPath("swift"); err != nil {
		t.Skip("swift not on PATH — install swift toolchain to run this test")
	}

	dir := t.TempDir()
	writeSwiftFixture(t, filepath.Join(dir, "Package.swift"), `// swift-tools-version:5.5
import PackageDescription
let package = Package(
    name: "Logic",
    targets: [
        .target(name: "Logic"),
        .testTarget(name: "LogicTests", dependencies: ["Logic"]),
    ]
)
`)
	mkdirSwiftFixture(t, filepath.Join(dir, "Sources", "Logic"))
	writeSwiftFixture(t, filepath.Join(dir, "Sources", "Logic", "Logic.swift"),
		"public func add(_ a: Int, _ b: Int) -> Int { a + b }\n")
	mkdirSwiftFixture(t, filepath.Join(dir, "Tests", "LogicTests"))
	// Qualify `Logic.add` to dodge Swift 6's stricter
	// XCTestCase-method-vs-free-function disambiguation rules. The
	// fixture is meant to exercise the agent plumbing, not Swift's
	// shadowing semantics.
	writeSwiftFixture(t, filepath.Join(dir, "Tests", "LogicTests", "LogicTests.swift"), `import XCTest
@testable import Logic
final class LogicTests: XCTestCase {
    func testAdd() { XCTAssertEqual(Logic.add(2, 3), 5) }
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tc, err := DetectSwiftToolchain(ctx)
	if err != nil || !tc.Available {
		t.Skipf("swift detect failed: %v (notes=%q)", err, tc.Notes)
	}

	if err := runSwiftSubcommand(ctx, tc, dir, "build", false); err != nil {
		t.Fatalf("swift build failed: %v", err)
	}
	if err := runSwiftSubcommand(ctx, tc, dir, "test", false); err != nil {
		t.Fatalf("swift test failed: %v", err)
	}
}

// TestRunSwiftSubcommand_TestFailureReturnsExitError verifies that a
// genuinely-failing test surfaces an *exec.ExitError so the CLI can
// mirror swift's exit code rather than collapsing every failure to 1.
// Skipped when swift is missing.
func TestRunSwiftSubcommand_TestFailureReturnsExitError(t *testing.T) {
	if _, err := exec.LookPath("swift"); err != nil {
		t.Skip("swift not on PATH")
	}
	dir := t.TempDir()
	writeSwiftFixture(t, filepath.Join(dir, "Package.swift"), `// swift-tools-version:5.5
import PackageDescription
let package = Package(
    name: "Logic",
    targets: [
        .target(name: "Logic"),
        .testTarget(name: "LogicTests", dependencies: ["Logic"]),
    ]
)
`)
	mkdirSwiftFixture(t, filepath.Join(dir, "Sources", "Logic"))
	writeSwiftFixture(t, filepath.Join(dir, "Sources", "Logic", "Logic.swift"),
		"public func add(_ a: Int, _ b: Int) -> Int { a + b }\n")
	mkdirSwiftFixture(t, filepath.Join(dir, "Tests", "LogicTests"))
	// Failing assertion — 2+3 != 999. Qualify Logic.add for the
	// same reason as the happy-path test.
	writeSwiftFixture(t, filepath.Join(dir, "Tests", "LogicTests", "LogicTests.swift"), `import XCTest
@testable import Logic
final class LogicTests: XCTestCase {
    func testAddFails() { XCTAssertEqual(Logic.add(2, 3), 999) }
}
`)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	tc, err := DetectSwiftToolchain(ctx)
	if err != nil || !tc.Available {
		t.Skipf("swift detect failed: %v", err)
	}

	err = runSwiftSubcommand(ctx, tc, dir, "test", false)
	if err == nil {
		t.Fatal("failing test should return non-nil error")
	}
	code, ok := exitCodeFromError(err)
	if !ok {
		t.Fatalf("expected ExitError chain, got %T (%v)", err, err)
	}
	if code == 0 {
		t.Errorf("expected non-zero exit code from failing test, got %d", code)
	}
}

func writeSwiftFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdirSwiftFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
