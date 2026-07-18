# Production Launch Checklist

Work through this in order before making the repository public and inviting
community workers. Check off literally — each line has bitten someone,
somewhere.

## Platform

- [ ] VM provisioned per deployment.md; host checklist from
      security-hardening.md applied (ufw, SSH keys, NTP, unattended-upgrades).
- [ ] `.env` complete; secrets generated fresh (nothing from dev); copy of
      `.env` in the team secret store; `chmod 600`.
- [ ] Snapshot signing keypair generated; public key recorded where worker
      onboarding docs reference it.
- [ ] DNS + TLS: `https://$DOMAIN` serves the banner; certificate valid.
- [ ] `/admin/v1` unreachable from a non-allowlisted IP (test it).
- [ ] `/metrics` unreachable externally (test it).
- [ ] Object storage bucket private; credentials scoped to that bucket only.
- [ ] First real snapshot built and published; `vapnctl status` shows sane
      prefix/target counts against the production provider catalog.
- [ ] Builder timer + backup timer enabled; one build and one backup have
      succeeded on schedule (not just manually).
- [ ] Offsite backup configured (`VAPN_BACKUP_S3_URI`) and one restore drill
      completed on a scratch database.
- [ ] Monitoring profile up; all alert rules loaded; test-fire one alert
      (stop the aggregator for 6 min) and confirm it pages whoever is on call.

## VPS Advisor integration

- [ ] Real endpoints implemented per the integration guide; contract tests
      green against staging.
- [ ] Service credential issued + scoped to `/api/v1/monitoring/*`; rotation
      procedure agreed with the website team.
- [ ] Provider opt-out toggle live on provider dashboards **before** any
      probing of real providers (announced probing, easy exit).
- [ ] Platform egress IPs supplied to the website team; 4xx alerting on
      their side.
- [ ] `insufficient_data` renders as "not enough data" on provider pages —
      verified visually, never as an outage.

## Worker network

- [ ] Release tagged; images public on GHCR; `install.sh` + CLI binaries
      downloadable; SHA256SUMS verified.
- [ ] `curl … | bash` walkthrough executed on a fresh VM (not a dev
      machine): install → approve → probing → update → uninstall.
- [ ] 3–5 **anchor workers** (operator-controlled, geographically spread)
      enrolled and active before opening community enrollment — consensus
      needs a trustworthy baseline (MinWorkers is unreachable otherwise).
- [ ] Probe policy published (what workers probe, rates, opt-out) — linked
      from the README and the worker docs; abuse-contact address live.
- [ ] Enrollment flow on VPS Advisor gated (manual approval) for the first
      cohort.

## Security sign-off

- [ ] Threat matrix in architecture/05 reviewed against the implementation;
      each mitigation traced to code/tests (see phase demos 6, 8, 11).
- [ ] Load test at 2× expected initial fleet passes with zero
      register/upload errors.
- [ ] `vapnctl audit` shows the expected trail for every admin action taken
      during this checklist.
- [ ] Dev conveniences absent in production: no `VAPN_DEV_ENROLLMENT_TOKEN`,
      no mockadvisor, no default passwords.

## Repository publication

- [ ] History audit: no secrets ever committed (`git log -p | grep -iE
      'password|secret|key' ` spot-check; the dev keys in demos are
      throwaway).
- [ ] LICENSE decided and added; README quick start verified on a clean
      clone.
- [ ] CI green on a fork (no reliance on private resources).

When every box is checked: make the repo public, announce to the first
operator cohort, and watch the Grafana fleet dashboard for the first 48 h.
