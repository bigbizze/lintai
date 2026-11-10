# Isolated declaration builds and source links

Status: design-only future work for PR 5. This document does not specify a
runtime build runner, a final build configuration schema, or new source-link
facts.

## Starting point

PR 3's agreed source-resolution and association contract is the baseline:

1. Use source when normal TypeScript resolution reaches it.
2. Use TypeScript project-reference source redirects where configured, respecting
   `disableSourceOfProjectReferenceRedirect` and without requiring built output.
   Do not infer project references from workspace membership alone.
3. For generated declarations that remain in use, use existing declaration maps
   only when their mapping anchors and source declarations validate.
4. Use an explicit configured source-entry association when those mechanisms
   cannot establish a relationship.

The first two steps determine what the checker reads. Maps and configured
associations add optional source evidence; they do not replace checked types or
declaration identity. A failed optional association must leave known declaration
facts usable. The configured fallback associates exported bindings and their
declaration sets; it does not guess source paths or pair overloads one by one.

Optional build execution is separate future work. It could produce fresh
declarations and maps for a repository whose normal build is needed to expose its
types. It is not needed to complete the PR 3 source-link contract.

## Output capture and source provenance

A build runner must make the generated declarations and maps from one build
available as one consistent filesystem view to the checker. A later design must
choose whether extraction runs inside the container or reads captured output on
the host, and must translate container paths to stable workspace identities.
Partial or failed builds must not silently mix new declarations with old maps.

Capturing output proves which files the build produced. It does not, by itself,
prove which source declaration produced a particular generated declaration or
that a source association is correct. A usable, validated declaration map can
provide declaration-association evidence, but does not prove the source and
generated files came from the same build. Missing, malformed, unavailable,
unsupported, or ambiguous maps must remain distinguishable. If maps cannot
establish a link, the explicit PR 3 binding fallback may still apply under its
contract; matching names alone are not proof of freshness or one-to-one
declaration identity.

## Isolation and permissions

Docker or Podman could provide an optional OCI runtime, avoiding a new container
engine in lintai. Containerization alone does not make arbitrary build commands
safe or ensure that writes are isolated.

- Make workspace inputs read-only by default. Give the build a separate writable
  output area for declared paths.
- A bind-mounted writable directory writes to its host directory. A bind mount is
  not copy-on-write. Symlinks and hard links to original workspace files also do
  not isolate writes; a build can modify the files they reference.
- If a build may write outside declared output paths, use a disposable copy or a
  filesystem overlay with understood platform behavior, or fail the build. Do
  not present symlink sharing as isolation.
- Make the command, image and toolchain, writable paths, environment values,
  network access, credentials, timeouts, resource limits, and cleanup behavior
  explicit. Do not pass host credentials or expose the container runtime socket
  by default. The exact permission model remains an open design decision.
- Keep container support optional. Host `node_modules` may not work in a
  container with a different operating system or architecture, so dependency
  installation and package caches need their own explicit handling.

No final command format, permissions framework, or configuration schema is
proposed here. A future implementation should execute only commands selected by
the user through explicit configuration.

## Cost and caching

Measure cold and warm costs separately. Include runtime or VM startup, image
availability, dependency installation, build time, output capture, and source-map
validation. Docker or Podman may have extra startup and filesystem costs on macOS
and Windows. Reuse of images and dependency downloads may help, but should not
hide the cost of a clean build.

Do not add a persistent build-output cache until its inputs can be identified
correctly. A cache key may need the command and arguments, image or toolchain
identity, source files, package manifests and lockfiles, TypeScript/build
configuration, runtime version, permission settings, relevant environment,
platform, and declared network-provided inputs. If a command can read undeclared
files, environment values, or mutable external data, a cache hit cannot be
assumed correct. Any eventual cache must be bounded and have a clear eviction
policy. Cache invalidation must also notice inputs that were missing and later
appear. These requirements are separate from PR 3's snapshot refresh for
source-link configuration, maps, and source files.

## Decisions before implementation

- Which packages and prerequisites a configured command may build, and how its
  writable output paths are captured.
- Whether extraction runs in the container or against captured output, while
  keeping declarations, maps, and module resolution consistent.
- How failed, partial, or non-deterministic builds are reported and cleaned up.
- Which environment, network, credentials, and filesystem permissions are
  available to a build.
- How paths, symlinks, multiple package instances, generated source, and
  conflicting project configurations are handled across platforms.
- What evidence is accepted for each source link, including missing or ambiguous
  maps and the existing configured binding fallback.
- Whether measured warm-run savings justify bounded caching, and which inputs
  can be captured well enough to make a cache hit correct.

These decisions should be made from an adopting repository and measured builds.
Do not invent final source-link schemas or a general build framework in this
design-only PR. PR 3's map/configuration outcomes remain the reference for
unavailable evidence; build output must not be described as source provenance.
