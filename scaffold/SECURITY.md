# Security Policy

This document is the security policy for the Terraform provider for {{PRODUCT}} and the assurance
case behind it. Mechanism claims name the file or code path that implements them so a reviewer can
check each one against the source.

Module: `{{MODULE}}`
Registry: `registry.terraform.io/{{REGISTRY_NAMESPACE}}/{{PROVIDER}}`
License: {{LICENSE}}. Repository: <https://github.com/{{REPO}}>

## Supported versions

Only the most recent release is supported. Security fixes land on `main` and in the next release
from that line.

| Version | Supported |
|---------|-----------|
| The latest release | Yes |
| Anything older than the most recent release | No |

Because the project ships a single rolling line with no long-term-support branches, users on an
older tag should upgrade before reporting a defect that already has a fix on `main`.

## Reporting a vulnerability

Report privately through GitHub private vulnerability reporting:

<https://github.com/{{REPO}}/security/advisories/new>

Do not open a public issue for a suspected vulnerability. GitHub Discussions are disabled on this
repository; the private advisory form above is the reporting channel.

Include, as far as you can determine:

- The provider version (and the release tag) you tested.
- The Terraform CLI version and the operating system.
- The {{PRODUCT}} server version.
- A description of the impact: credential exposure, privilege escalation, integrity loss, denial of
  service.
- Reproduction steps, ideally a minimal Terraform configuration and the exact commands run.
- A proof of concept if available.
- Whether the issue is already public anywhere, and under what embargo.

Response timelines the maintainer aims to meet:

| Stage | Target |
|-------|--------|
| Acknowledgement of the report | 48 hours |
| Assessment and severity triage | 7 days |
| Fix release | 14 days |

On receipt the maintainer acknowledges the report, reproduces it, agrees a severity and an embargo
with the reporter, prepares a fix on a private branch, and publishes a GitHub security advisory with
the fix. Where a reporter cannot reproduce on the latest release, the maintainer will say so in the
advisory.

Credit policy: reporters are credited by name or handle in the published advisory unless they ask to
remain anonymous. Reporters may request the severity wording be reviewed before publication.

## Security design requirements

Every provider built from this repository must satisfy the following. The implementation of each
requirement lives in the provider's own packages and is reviewed with the change that introduces or
touches it.

- Credentials are sent only in the `Authorization` header of requests to the configured endpoint.
- Credentials and other secrets are marked `Sensitive: true` in the schema, and are never logged.
  Provider code uses `tflog.Debug` at CRUD boundaries, and those calls do not carry credential
  values.
- TLS certificate verification is on by default and cannot be turned off by a provider option.
  Go's standard-library defaults (a modern TLS minimum, certificate verification on) apply.
- The endpoint scheme is restricted to `http` and `https`; a plaintext `http://` endpoint is allowed
  only because the user configured it, and the README says what that costs.
- HTTP clients are bounded by a configurable timeout, so an unresponsive server cannot make a
  Terraform operation hang indefinitely.
- No shell, SQL or template evaluation: request bodies are built from typed values.
- Error bodies are truncated before they are surfaced, so a hostile or verbose server cannot flood
  logs.
- Terraform state is the user's responsibility; the provider marks secret-bearing attributes
  `Sensitive` but does not encrypt state or choose a backend.

## Threat model

Trust boundaries and what crosses each one:

| Boundary | Data crossing | Controls |
|----------|---------------|----------|
| Terraform CLI ↔ plugin process | Configuration values, credentials, resource state | Terraform plugin protocol 6.0; `Sensitive` marking; credentials carried only in the `Authorization` header |
| Provider → {{PRODUCT}} API over the configured endpoint | Resources, data-source reads, request payloads | Scheme restricted to http/https; one bounded `http.Client`; typed JSON request bodies; a `User-Agent` identifying the provider and version |
| Provider → release supply chain: Actions → GoReleaser → signed checksums + provenance → Terraform Registry | Release artifacts | The reusable release workflow imports the signing key from Actions secrets and pushes a signed tag, builds with GoReleaser, detach-signs the checksums, attests SLSA build provenance over the checksum file through OIDC, and attaches the SPDX-JSON SBOM and provenance bundle. Where the repository publishes the public key it is committed at `.github/release-signing-key.asc`; otherwise the release notes name the trust root. The verification steps are in `README.md#verifying-a-release` |

## Common weakness classes

| Class | Position | Where |
|-------|----------|-------|
| Injection (shell, SQL, template) | Not applicable: inputs are decoded into typed structs and sent as JSON; no shell, SQL or template evaluation anywhere | whole repository |
| Sensitive data exposure | Credential- and secret-bearing attributes are `Sensitive`; error bodies are truncated before they are surfaced | provider schema, client package |
| Man-in-the-middle | Standard-library TLS verification with Go's defaults; no option to disable it; `net/http` strips the `Authorization` header on cross-host redirects | client package |
| Insecure deserialization | Responses are decoded into typed models of the server's response envelope; no arbitrary type resolution | client package |
| XXE / XML parsing | Not applicable unless the provider parses XML, which it must not do for API responses | — |
| Unbounded input | Requests time out; error bodies are truncated | client package |
| Dependency risk | Dependabot covers gomod, docker-compose and github-actions; OpenSSF Scorecard runs weekly and uploads SARIF to code scanning; `govulncheck` runs in CI | `.github/dependabot.yml`, `.github/workflows/` |
| Supply chain | Release tags are signed, checksums are signed, SLSA build provenance is attested, and an SPDX-JSON SBOM is attached | `.github/workflows/release.yml`, `.github/release-signing-key.asc` |

## Hardening in place

- The default branch's ruleset forbids deletion and force-pushes and requires a pull request.
  The ruleset and its code-owner setting are described in [`GOVERNANCE.md`](GOVERNANCE.md); where a
  setting is missing, that document states it as an open item rather than implying it is enforced.
- CI runs the golangci-lint set (`.golangci.yml`) and `gosec`, whose findings are uploaded to code
  scanning and fail the job.
- `dependency-review` runs only when the caller enables it (`dependency-review`).
- The markdown link check runs only when the caller enables it (`markdown`, together with `docs`).
- Release artifacts are built from a clean `CGO_ENABLED=0`, `-trimpath`, `-s -w` GoReleaser
  configuration with the commit timestamp as `mod_timestamp`; the tag and the checksums are signed,
  SLSA build provenance is attested, and the SBOM and provenance bundle are attached to the release.

## Accepted limitations

Stated plainly, because each is a real property of the current release:

- Any secret-bearing attribute the API returns is placed in Terraform state. State encryption and
  remote backends are the user's responsibility; the provider marks the attributes `Sensitive` but
  cannot make state secret.
- A plaintext `http://` endpoint is accepted if the user configures one. TLS is not forced; the
  provider only rejects schemes other than http and https.
- Provider configuration taken from environment variables is only as private as the environment it
  is set in.
- The provider does not manage the {{PRODUCT}} server's own security. Server-side authentication,
  authorisation and audit logging are outside this provider's scope.
- No dated security review has been performed yet.

## Security review status

This policy does not by itself constitute a dated security review. The controls described here are
those implemented in code and CI today; where a formal review is completed, its date and scope
belong in this section. OpenSSF Scorecard results are published and CI reports the merged unit +
acceptance coverage; any dependency-scanning gap is listed in the repository's issues rather than
implied to be closed.

## Dependency scanning

- Dependabot version updates: gomod, docker-compose and github-actions, daily.
- Dependabot security updates and secret scanning are organisation settings; where they are not
  enabled, that is recorded in [`GOVERNANCE.md`](GOVERNANCE.md) as an open item.
- `govulncheck` runs in CI and fails the job on a reachable vulnerability.
- OpenSSF Scorecard: weekly on `main` and whenever branch protection changes; the SARIF result is
  uploaded to code scanning and published for the README badge.

The dependency surface is the provider's `go.mod`; the HashiCorp plugin libraries are MPL-2.0 and
are linked into the provider binary. See [`NOTICE`](NOTICE) for upstream lineage.

## Security best practices for users

1. Use an `https://` endpoint. The provider may accept `http://`, but only TLS protects credentials
   in transit.
2. Do not hardcode credentials in Terraform files. Prefer the provider's environment variables, and
   treat those variables as secret.
3. Encrypt Terraform state or use a remote backend with access controls; secret-bearing attributes
   end up in state.
4. Grant the {{PRODUCT}} principal the least privilege it needs; the provider acts with exactly the
   credentials you configure.
5. Rotate credentials regularly, and store any key material so that only the Terraform runner can
   read it.
6. Upgrade to the latest release before filing a report, so a known fix is not re-reported.
