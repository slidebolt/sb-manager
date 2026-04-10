# Git Workflow for sb-manager

This repository contains the Slidebolt Manager, which coordinates discovery and process management. It produces a standalone binary and depends on the core contracts.

## Dependencies
- **Internal:**
  - `sb-contract`: Core interfaces and shared structures.
- **External:** 
  - Standard Go library.

## Build Process
- **Type:** Go Application (Service).
- **Consumption:** Run as a background service/daemon.
- **Artifacts:** Produces a binary named `sb-manager`.
- **Command:** `go build -o sb-manager ./cmd/sb-manager`
- **Validation:** 
  - Validated through unit tests: `go test -v ./...`
  - Validated by successful compilation of the binary.

## Pre-requisites & Publishing
As a service that relies on core contracts, `sb-manager` should be updated whenever `sb-contract` is changed.

**Before publishing:**
1. Determine current tag: `git tag | sort -V | tail -n 1`
2. Ensure all local tests pass: `go test -v ./...`
3. Ensure the binary builds: `go build -o sb-manager ./cmd/sb-manager`

**Publishing Order:**
1. Ensure `sb-contract` is tagged and pushed (e.g., `v1.0.4`).
2. Update `sb-manager/go.mod` to reference the latest `sb-contract` tag.
3. Determine next semantic version for `sb-manager` (e.g., `v1.0.4`).
4. Commit and push the changes to `main`.
5. Tag the repository: `git tag v1.0.4`.
6. Push the tag: `git push origin main v1.0.4`.

## Update Workflow & Verification
1. **Modify:** Update management logic in `internal/` or `app/`.
2. **Verify Local:**
   - Run `go mod tidy`.
   - Run `go test ./...`.
   - Run `go build -o sb-manager ./cmd/sb-manager`.
3. **Commit:** Ensure the commit message clearly describes the management change.
4. **Tag & Push:** (Follow the Publishing Order above).
