# Security Policy

**hspdf** is an independent fork of [pdfcpu](https://github.com/pdfcpu/pdfcpu) and is
maintained separately by [@huzpsb](https://github.com/huzpsb). It is not affiliated with
the upstream pdfcpu project.

## Reporting a vulnerability

Please report vulnerabilities in **hspdf** — this repository, including the code that is
specific to the fork (OCR, rendering, text extraction/replacement, solidify, obfuscate,
the web UI) — privately via GitHub security advisories:

<https://github.com/huzpsb/hspdf/security/advisories/new>

Please do **not** open a public issue for a security problem, and please do not report
hspdf vulnerabilities to the upstream pdfcpu project or its maintainer.

If a vulnerability also reproduces with an unmodified upstream pdfcpu release, please
report it to pdfcpu separately, following the
[pdfcpu security policy](https://github.com/pdfcpu/pdfcpu/blob/master/SECURITY.md).

## Scope

- PDFs and other input files are untrusted input. Crashes, hangs, or memory exhaustion
  caused by crafted files are in scope, for both the CLI and the web UI.
- The web UI runs entirely in your browser; still, issues in its JS/HTML layer are in
  scope.
- Vulnerabilities inherited from upstream pdfcpu are also in scope here, but please tell
  both projects.
