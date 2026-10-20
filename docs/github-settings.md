# GitHub settings checklist

These live in GitHub, not in git, so they must be applied by a repository admin.
Repository: `rpsoftech/CadPortal` (organization-owned, public).

## 1. Protect `main` (Settings > Rules > Rulesets > New branch ruleset)

**Fastest route:** Settings > Rules > Rulesets > New ruleset > **Import a ruleset** and
choose [`ruleset-main.json`](ruleset-main.json). It sets everything below and lets
repository admins bypass the review requirement through a pull request only (so a solo
owner is not locked out). Check the result against this list.

Target: default branch (`main`). Enforcement: **Active**.

- [ ] Restrict deletions
- [ ] Block force pushes
- [ ] Require linear history
- [ ] Require a pull request before merging
  - [ ] Required approvals: **1** (see the note below)
  - [ ] Dismiss stale approvals when new commits are pushed
  - [ ] Require review from Code Owners (`.github/CODEOWNERS`)
  - [ ] Require conversation resolution before merging
- [ ] Require status checks to pass, and require branches to be up to date:
  `test`, `sqlc`, `vulncheck` (these names come from `.github/workflows/ci.yml`; run the
  workflow once first so GitHub offers them in the list)
- [ ] Require signed commits (optional; needs everyone to set up signing)

> **Solo owner:** you cannot approve your own pull request. Either keep approvals at 1
> and add a second reviewer, or set approvals to 0 and keep the status checks, or add
> yourself to the ruleset's bypass list. Do not weaken the check requirements.

## 2. Code security (Settings > Advanced Security)

- [ ] Private vulnerability reporting: **Enable** (SECURITY.md points to it)
- [ ] Dependency graph: **Enable**
- [ ] Dependabot alerts: **Enable**
- [ ] Dependabot security updates: **Enable**
- [ ] Secret scanning: **Enable**
- [ ] Push protection (blocks pushes containing secrets): **Enable**
- [ ] Code scanning with CodeQL (default setup, language: Go): **Enable**

## 3. Actions (Settings > Actions > General)

- [ ] Actions permissions: allow actions created by GitHub, and verified creators only
- [ ] Fork pull request workflows from outside collaborators: **Require approval for all
  outside collaborators**
- [ ] Workflow permissions: **Read repository contents** (not read and write), and leave
  "Allow GitHub Actions to create and approve pull requests" off
- [ ] Do not add repository secrets that CI does not need. The current workflow needs none.

## 4. General

- [ ] Disable Wikis and Projects if unused
- [ ] Issues: keep enabled (templates are in `.github/ISSUE_TEMPLATE`)
- [ ] Pull requests: allow **squash merging** only; tick "Automatically delete head
  branches"
- [ ] Forking: leave enabled (the license permits forking only through GitHub's fork
  feature)

## 5. Organization and accounts

- [ ] Require two-factor authentication for all `rpsoftech` members
- [ ] Keep admin rights to the minimum number of people
- [ ] Prefer fine-grained personal access tokens with an expiry over classic tokens

## 6. One-time hygiene for a repository that is already public

- [ ] Search history for anything sensitive:
  `git log -p --all -S"password" -- . ':!*.md'` and similar. If a real secret was ever
  committed, **rotate it**; deleting it from history is not enough.
- [ ] Check which email your commits expose. Use your GitHub `noreply` address
  (Settings > Emails > "Keep my email addresses private") for future commits.
- [ ] Confirm the `cad` / `cad` credentials in `docker-compose.yml` are used only for
  local development and nowhere real.
- [ ] `CLAUDE.md` is tracked and therefore public. It describes the domain model and
  open business decisions; remove sensitive business details from it if that matters.
