# Releases

Releases run automatically after a pull request is squash-merged into `main`.
The applicable lint, test, security, and build jobs in the `ci:app` workflow must pass
`release:check` before `release:publish` runs semantic-release.
The highest applicable Conventional Commit since the last `v*` tag determines
the next version:

| Commit | Release |
| --- | --- |
| `feat` | Minor |
| `fix`, `perf` | Patch |
| `chore(deps)` | Patch |
| `refactor(storage)` | Patch |
| Breaking change (`!` or `BREAKING CHANGE:`) | Major |
| Other types, including `ci` and `chore(ci)` | None |

Use `fix(storage)` for storage security and correctness fixes. Use
`refactor(storage)` for runtime storage implementation changes, such as replacing
an SDK, that need a published image. Unscoped refactors do not trigger releases.

Standard SemVer applies below 1.0 too: a feature after 0.1.18 produces 0.2.0,
and a breaking change produces 1.0.0. No source version bump or release commit is
needed. The Nix build injects the source commit into `main.Version`; release tags
are aliases for the already-built image, so assigning a version never rebuilds it.

`build:go` builds the amd64 application image with `nix build .#image`, scans it,
and uploads it as an immutable workflow artifact. During semantic-release's
`prepare` step, `release:publish` downloads that same artifact and pushes it to
`ghcr.io/fsiwh/web:<version>`, preserving and verifying its manifest
digest. It does not compile or package the application. Only after the
push succeeds does semantic-release push the tag and publish an empty GitHub
Release. The workflow then promotes the `latest` alias. Consumers should deploy
the versioned image or its digest. The release job exposes `released` and `tag`
outputs for subsequent deployment jobs.

## Application image

Trivy generates an SPDX SBOM from the built image. Publishing attaches it to that
image digest as an OCI referrer in GHCR. The same SPDX document is attached to the GitHub
Release as `web-<version>.sbom.spdx.json` (for example,
`web-v0.1.22.sbom.spdx.json`) for direct download. Inspect a version's SBOM with:

```sh
oras discover ghcr.io/fsiwh/web:<version>
```

The Nix image contains a stripped, path-trimmed static Go executable with
embedded templates and browser assets, CA certificates and timezone data. It has
no distribution base image, shell, compiler or Nix runtime. The builder keeps sources and generation
tools out of the runtime. `test:go` verifies committed SQLC query generation and
tests all Go packages with race detection. Node and Bun are release tools only.

`flake.lock` pins the compiler, asset tools and runtime data; Go dependencies have
a fixed vendor hash, and asset downloads have fixed hashes. Nix builds without
network access after fetching those inputs and gives image layers fixed timestamps.
On Linux, reproduce an image from a clean checkout of its source commit with:

```sh
nix build .#image
docker load --input result
```

`nix build .#web` builds just the binary on Linux or macOS. Race tests compile a
separate instrumented test executable; the release binary is built only once per
workflow. Pull requests and their squash-merge commits have distinct source
identities and therefore distinct builds.

The application is PID 1, runs as UID 1001 on port 8080 and owns `/data`.
SIGTERM initiates graceful HTTP shutdown. The container health command checks
`/healthz`. No process supervisor or internal API proxy is required.

For version diagnostics:

```sh
docker run --rm ghcr.io/fsiwh/web:<version> -version
```

### Production cutover

The application replaces the combined V1 runtime with persistent SQLite storage,
account authentication, SMTP and local or S3-compatible object storage.

When preparing deployment, preserve the existing SQLite data, Garage bucket,
credentials and object keys. The new container listens on port 8080, replacing
V1's public port 80. V1 NextAuth/internal JWT variables and `/api/v1`/Swagger
routes no longer configure the HTML application. Verify service configuration,
data permissions and public routing before switching production traffic.

Releases publish images only; production deployment remains a separate operation.

The `ci:app` workflow can be dispatched manually; `dry-run` defaults to true.
A dry run still runs full verification, including image build validation. The
publication step checks authentication and version calculation but skips pushing
images, tags, releases, and alias promotion. It must target `main`.

## Recovery

- An image build fails before tagging: rerun the workflow; no Git tag or GitHub
  Release will have been published.
- The tag exists but GitHub Release publication failed: verify that the versioned
  image exists, then create the missing release for that existing tag with an empty
  body. Do not delete the tag or allocate another version to repair publication.
- The release exists but alias promotion failed: rerun only the failed release job
  to promote the latest existing release's alias. The workflow verifies that its
  commit is still the latest released commit before performing this repair.
- A deployment fails: redeploy the already published image version; publishing and
  deployment are separate operations.

## Release-tool security patch

semantic-release depends on `micromatch`, which depends on `braces@3.0.3`.
GHSA-vfj7-8cjw-p6xm has no published fixed version. The committed Bun patch bounds
brace/parenthesis parsing and recursive AST walkers at depth 100, based on the
mitigation discussed in <https://github.com/micromatch/braces/pull/72>.

`bun run audit` excludes only that advisory because registry audits identify the
original version, not Bun patches. Every other high/critical advisory still fails
the check. CI installs with the frozen lockfile before auditing or releasing.
Revisit the patch and exception when a fixed
upstream release is available. The patch is isolated to release tooling.

Run locally from this directory:

```sh
bun install --frozen-lockfile --ignore-scripts
bun run audit
```
