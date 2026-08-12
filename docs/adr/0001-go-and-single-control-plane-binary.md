# ADR-0001: Go, and one control-plane binary with role flags

**Status:** Accepted · **Date:** 2026-08-12

## Context

The project needs a language and a process topology for a small VM control plane (API + reconciler + scheduler + per-host agent). Production platforms in this lineage (Nova, KubeVirt) separate an API tier from controllers; at portfolio scale a separate API process would be a thin proxy adding a hop and a deployment.

## Decision

- **Go** for all binaries: the lingua franca of infrastructure control planes; static agent binaries; goroutines map naturally onto reconcile loops and per-VM executors.
- **One `control-plane` binary** hosting API, reconciler, and scheduler behind `--role` flags (default: all). Package boundaries stay strict — `internal/api` never imports `internal/reconciler`; they share only `internal/store`.

## Consequences

- The split-out path is a deployment change, not a refactor: the seam is the store and the Operation table. `--role api` / `--role controller` demonstrates it live.
- One process to supervise in local dev (Tier 0), which the project optimizes for.

## Alternatives considered

- Separate API + controller processes: closer to large-scale topology, but doubles local orchestration for no learning gain at this scope; the seam is demonstrated with flags instead.
- Rust/Java: viable, but Go maximizes transferability to the systems this project emulates.
