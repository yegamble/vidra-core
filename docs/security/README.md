# Security Documentation

This directory contains all security-related documentation for the Athena project.

## Core Security Policy

- **[SECURITY.md](SECURITY.md)** - Security policy, vulnerability reporting, and security advisories (including CVE-ATHENA-2025-001)

## Security Features

- **[SECURITY_E2EE.md](SECURITY_E2EE.md)** - End-to-end encrypted messaging implementation
- **[IPFS_SECURITY_IMPLEMENTATION.md](IPFS_SECURITY_IMPLEMENTATION.md)** - IPFS security hardening and validation

## Security Assessments & Reports

- **[SECURITY_PENTEST_REPORT.md](SECURITY_PENTEST_REPORT.md)** - Penetration testing report
- **[SECURITY_ADVISORY.md](SECURITY_ADVISORY.md)** - Security advisories and credential exposure mitigation

## Virus Scanner Security

- **[SECURITY_ASSESSMENT_VIRUS_SCANNER_P1.md](SECURITY_ASSESSMENT_VIRUS_SCANNER_P1.md)** - P1 vulnerability assessment
- **[SECURITY_ANALYSIS_VIRUS_SCANNER.md](SECURITY_ANALYSIS_VIRUS_SCANNER.md)** - Detailed virus scanner analysis
- **[SECURITY_P1_EXECUTIVE_SUMMARY.md](SECURITY_P1_EXECUTIVE_SUMMARY.md)** - Executive summary of P1 fix
- **[SECURITY_FIX_CHECKLIST.md](SECURITY_FIX_CHECKLIST.md)** - Security fix implementation checklist
- **[SECURITY_DEFENSE_IN_DEPTH_RECOMMENDATIONS.md](SECURITY_DEFENSE_IN_DEPTH_RECOMMENDATIONS.md)** - Defense-in-depth strategy

## Quick Links

- [Main README](../../README.md)
- [Architecture Documentation](../architecture/)
- [Deployment Guide](../deployment/)

## Security Best Practices

1. **Always use `CLAMAV_FALLBACK_MODE=strict` in production**
2. Enable pre-commit hooks to prevent credential leaks
3. Rotate credentials after any security advisory
4. Review security advisories regularly
5. Keep ClamAV signatures up to date
