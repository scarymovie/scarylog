# Security Policy

Languages: [Русский](SECURITY.md)

## Supported versions

| Version | Module path                         | Supported |
| ------- | ----------------------------------- | --------- |
| v2.x    | `github.com/scarymovie/scarylog/v2` | yes       |
| v1.x    | `github.com/scarymovie/scarylog`    | no        |

The module follows semantic import versioning. The current line is **v2**, module
path `github.com/scarymovie/scarylog/v2`; fixes, security fixes included, ship
only there.

The **v1** line (`github.com/scarymovie/scarylog`, no `/v2` suffix) is frozen and
receives no updates. Moving from v1 to v2 is not automatic: v2 deliberately broke
the `Error` signature (now `Error(err error, args ...any)`), so the upgrade is a
manual one. See the "Versioning" section of [`CLAUDE.md`](../CLAUDE.md).

## Reporting a vulnerability

**Do not open a public issue, and do not describe the problem in a pull request.**

Use GitHub private vulnerability reporting:

1. open the repository's **Security** tab;
2. click **Report a vulnerability** — direct link:
   <https://github.com/scarymovie/scarylog/security/advisories/new>;
3. describe the problem in the form.

Worth including:

- the module version (tag or commit) and the output of `go version`;
- a minimal reproducer — a compilable snippet or a test;
- the affected package: the root `scarylog` or `scaryhttp`;
- an impact assessment: what an attacker can actually do.

## Timelines

- acknowledgement of receipt: within 5 business days;
- initial assessment (confirm/decline, agree on severity): within 10 business
  days;
- fix and advisory publication: by agreement, usually together with the next
  patch release of the v2 line.

## Scope

`scarylog` is a thin wrapper around the standard library's `log/slog` with no
third-party dependencies, so its vulnerability surface is small. In scope:

- leaking sensitive data into logs through default attributes, groups, attribute
  overwrite, or key remapping (`WithAttrRemapping`);
- log injection: attribute keys or values that let someone forge or break a
  record for a downstream log consumer;
- `scaryhttp`: trusting client-supplied correlation-id headers; a panic or data
  leak in the middleware; the `http.ResponseWriter` wrapper, which must implement
  exactly the same interfaces as the original writer — getting this wrong breaks
  WebSocket upgrades and streaming;
- denial of service while walking an error chain: `stackOf` traverses the whole
  `Unwrap` tree, including `Unwrap() []error`, and must stay iterative under a
  single budget for the whole walk — a cyclic multi-error must neither loop
  forever nor kill the process.

Out of scope:

- vulnerabilities in the Go standard library itself — report those at
  <https://go.dev/security/policy>;
- issues in third-party code that calls `scarylog`;
- the logger writing exactly the data it was handed: redacting secrets at the
  call site is the application's responsibility.

## Disclosure

Disclosure is coordinated, through GitHub Security Advisories. We request a CVE
where warranted. We will credit you in the advisory unless you ask us not to.
