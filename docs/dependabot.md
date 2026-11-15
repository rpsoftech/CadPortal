# Dependabot

Dependabot opens pull requests to keep dependencies current. Its configuration is
[`.github/dependabot.yml`](../.github/dependabot.yml). This page says what it watches, what to check
before merging each kind of pull request, and how to work with it.

## What it watches

Everything is checked weekly, with at most 5 open pull requests for Go and for npm.

| Ecosystem | Folder | Grouped | What the pull request touches |
|---|---|---|---|
| Go modules | `/backend` | `aws-sdk`: every `github.com/aws/*` module in one pull request | `backend/go.mod`, `backend/go.sum` |
| npm (Yarn) | `/frontend` | `angular`: `@angular/*` and `@angular-devkit/*` in one pull request | `frontend/package.json`, `frontend/yarn.lock` |
| GitHub Actions | `/` | no | `uses:` lines in `.github/workflows/` |
| Docker | `/backend`, `/frontend` | no | base images in the two Dockerfiles |
| Docker Compose | `/` | no | image tags in the compose files |

Everything else (for example `@axe-core/playwright`, `prettier`, `jsdom`) arrives as its own pull request.

## Before merging

CI decides what runs (see the CI section of [CONTRIBUTING.md](../CONTRIBUTING.md)), so a pull request
shows only the checks that apply to it. Skipped jobs are normal. What to look at:

- **Go modules (`aws-sdk` and others).** `test`, `vulncheck` and the three `docker-*` parts must be green.
  CI skips the tests that talk to real Cloudflare R2, and the AWS modules are what sign links and move files
  to and from R2. After merging an `aws-sdk` update, upload and download one file on staging to confirm it.
- **npm, the `angular` group.** `frontend` (build and unit tests) and `docker-stack` (the full browser suite,
  which includes the accessibility scans) must be green. Angular Material draws the tables, chips and
  dialogs, so a green browser run is the check that matters.
- **GitHub Actions.** The workflows are tag-pinned (for example `docker/login-action@v4.7.0`). `release.yml`
  runs only on a version tag and `staging.yml` only on a push to `main`, so a pull request never runs the
  changed step. The workflow lint in `docker-stack` catches syntax mistakes only. After merging, watch the
  next `:staging` publish.
- **Docker images.** The images are built from these files in `docker-stack`, so a green run covers the build.
  The base image that matters most is Postgres: read the release notes before accepting a major version, and
  never let a major database upgrade ride along with a routine merge.
- **Accessibility tooling.** `@axe-core/playwright` brings a newer `axe-core`, and a newer rule set can flag
  problems that were always in the app. When the `a11y` scans fail after such an update, fix the app, not the
  tool. Do not pin the old version or ignore the update. Example: `axe-core` 4.14 reported
  `label-content-name-mismatch`, because three controls had an `aria-label` that left out their visible text.
  The fix was to put the visible text into each accessible name (WCAG 2.5.3, Label in Name).

## Working with the pull requests

- **Two pull requests touching `frontend/package.json` and `frontend/yarn.lock`** conflict as soon as one
  merges. Merge one, then comment `@dependabot rebase` on the other and wait for CI to run again.
- **Do not edit a Dependabot branch** unless you have to. Once someone pushes to it, Dependabot stops
  rebasing it. When a fix is needed (as with the axe-core update above), either push it to that branch so the
  update and its fix merge together, or merge the fix to `main` first and then ask for a rebase.
- **Merge style.** Squash, like every other change, so history stays linear and the message ends with
  the pull request number.
- **Lockfiles are never edited by hand.** If a lockfile needs to change, let Dependabot do it, or run the
  package manager (`yarn install` in `frontend/`, `go mod tidy` in `backend/`).
- **Changing `.github/dependabot.yml`** needs no CI run (see the CI table in CONTRIBUTING.md). Test a change
  by opening the repository's **Insights → Dependency graph → Dependabot** page, which shows when each
  ecosystem last ran and any error.

## Commands

Write these as a comment on the pull request:

| Command | Effect |
|---|---|
| `@dependabot rebase` | Rebase the pull request onto `main` |
| `@dependabot recreate` | Recreate it from scratch, dropping any edits made to the branch |
| `@dependabot ignore this minor version` | Close it and skip this minor version (use for one-off problems) |
| `@dependabot ignore this dependency` | Close it and stop proposing this dependency (last resort) |
| `@dependabot show <dependency> ignore conditions` | List what is currently ignored |

For a group pull request, name the dependency: `@dependabot ignore <dependency> minor version`.

## Security updates

Dependabot security updates are a separate GitHub setting (Settings, Code security). When it is on, Dependabot
opens pull requests as soon as an advisory affects a dependency, whatever the weekly schedule says. The weekly CI run also runs `vulncheck` for Go, so a
new advisory shows up there too. Treat a security pull request as urgent: merge it as soon as CI is green.
