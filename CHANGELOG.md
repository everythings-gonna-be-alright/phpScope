# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.4] - 2026-11-08

### Added
- Unit tests for all packages (>90% coverage), including parser fuzzing
- End-to-end tests on kind: WordPress + phpScope + Pyroscope, checking per-request coverage, timestamps and per-endpoint stacks
- CI test workflow on every PR (lint, race tests, govulncheck, Docker build, e2e) with test reports as artifacts; releases now require it
- `--sampleLabels` flag to choose which trace fields become Pyroscope labels (default: `path`)
- Config validation, Dependabot, `docker-compose.build.yaml`

### Changed
- **Breaking:** only `path` is sent as a label by default (`--phpspyRequestInfo` now defaults to `p`). Before, `uri`, `qstring`, `cookie` and `ts` created a series per request and leaked cookies. Opt back in with `--phpspyRequestInfo` and `--sampleLabels`.
- Go 1.27.3, dependencies updated
- Hardened Docker Compose and Helm examples; WordPress 7.1.4 / PHP 8.5

### Fixed
- Invalid UTF-8 in a trace no longer drops the whole batch (#5)
- First sample after an idle gap was timestamped too early
- Hang on shutdown; phpspy no longer started through a shell

## [v0.1.3] - 2026-04-20

### Added
- Helm chart example for deploying WordPress + MySQL + Nginx with PHPScope as a sidecar container and Pyroscope as a subchart dependency (`examples/helm-chart/wordpress-example`)

### Changed
- Go version bumped from `1.23.3` to `1.26.2`
- Builder Docker image updated: `golang:1.23.3-alpine` → `golang:1.26.2-alpine3.23`
- phpspy build stage base image updated: `alpine:3.21` → `alpine:3.23`
- Pyroscope image in Docker Compose updated: `1.12.0` → `1.21.0`
- `github.com/google/pprof` dependency updated to `v0.0.0-20260402051712-545e8a4df936`

## [v0.1.2] - 2025-04-11

### Added
- `--pgrep` flag to configure the pgrep pattern used by phpspy for PHP process discovery (default: `-x "(php-fpm.*|^php$)"`)
- `pgrepPattern` displayed in the startup banner

### Changed
- phpspy process filter is no longer hardcoded — it is now driven by the `--pgrep` argument

## [v0.1.1] - 2025-03-03

### Added
- `--phpspyRequestInfo` flag to control which request info fields phpspy includes in traces (default: `qcup`)

### Fixed
- `PhpspyRequestInfo` argument was not being passed correctly to phpspy

## [v0.1.0] - 2025-02-20

### Added
- Initial pre-release of PHPScope
- Continuous PHP profiling via [phpspy](https://github.com/adsr/phpspy) with Pyroscope integration
- Support for PHP-FPM and PHP CLI process profiling
- Configurable sampling rate (`--rateHz`), batch size (`--batch`), and send interval (`--interval`)
- Function exclusion via regex pattern (`--exclude`)
- Custom tag support (`--tags key=value`)
- Docker image with phpspy and binutils bundled
- Docker Compose setup with Pyroscope for local usage
