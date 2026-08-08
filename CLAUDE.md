# CLAUDE.md

# VPS Advisor Community Network Intelligence Platform

## Documentation Consolidation and Simplified Builder Installation

You are working on the documentation for the **VPS Advisor Community Network Intelligence Platform**.

The implementation is already complete.

Your task is to improve and consolidate the existing documentation so that it is:

- accurate
- easy to navigate
- non-redundant
- internally consistent
- beginner-friendly where appropriate
- technically comprehensive where required
- easy to maintain

This is primarily a **documentation restructuring and improvement task**.

Do not redesign or reimplement the application.

Do not change application behaviour merely to accommodate documentation.

---

# 1. Understand the Existing Project First

Before changing any documentation, inspect the repository and understand:

- project architecture
- builder
- community worker
- coordinator
- aggregation
- routing snapshots
- PostgreSQL
- object storage
- RIPE RIS integration
- MaxMind integration
- worker authentication
- snapshot signing
- VPS Advisor integration
- deployment architecture
- CLI commands
- Docker configuration
- systemd configuration
- environment variables
- existing documentation

Also inspect the existing `docs/` directory completely.

Do not immediately create new documentation.

First determine:

1. What documentation already exists?
2. Which documents overlap?
3. Which documents contain the authoritative version of information?
4. Which documents should be merged?
5. Which documents should remain separate?
6. Which documents are missing?
7. Which information is duplicated across multiple files?
8. Which links will need updating after consolidation?

---

# 2. Documentation Consolidation Is a Priority

The current documentation contains several related documents.

Do NOT solve documentation gaps by continuously creating additional files.

Before creating a new document, determine whether the information belongs in an existing document.

The goal is:

> Fewer, clearer, better-organized documents rather than many small documents containing overlapping information.

Merge documents when they cover substantially the same subject.

For example, if multiple documents explain:

- builder installation
- builder configuration
- builder operation
- builder deployment

consider whether they can be consolidated into a coherent builder guide instead of requiring the reader to navigate several documents.

Likewise, identify similar redundancy across:

- architecture
- operations
- worker documentation
- deployment
- security
- API documentation
- integration documentation

Do not merge documents merely because they are related.

Keep documents separate when they serve clearly different audiences or purposes.

---

# 3. Establish a Single Source of Truth

For every important concept, establish one authoritative location.

Examples:

### Configuration

The configuration reference should be authoritative for:

- environment variables
- defaults
- valid values
- configuration semantics

Other documents should explain configuration at a high level and link to the reference instead of duplicating the entire configuration table.

### Architecture

Architecture documentation should be authoritative for:

- system components
- data flows
- architectural decisions
- service relationships

Operational documents should focus on operating the system rather than duplicating architectural explanations.

### API

The API documentation should be authoritative for:

- endpoints
- authentication
- request formats
- response formats
- error responses

Integration guides should explain how to use the API in context rather than duplicating the complete API specification.

### Installation

Installation guides should focus on getting the software running successfully.

Do not turn every installation guide into an architecture manual.

---

# 4. Documentation Audience

The documentation has multiple audiences.

Do not write every document at the same technical level.

Clearly distinguish between:

### Beginners / Community Contributors

Need:

- simple instructions
- copy-and-paste commands
- explanations of unfamiliar terms
- minimal configuration
- troubleshooting

### Platform Operators

Need:

- deployment
- configuration
- monitoring
- backups
- recovery
- upgrades
- security
- operational procedures

### Developers

Need:

- architecture
- APIs
- database
- internal services
- development workflow
- testing

### VPS Advisor Developers

Need:

- Django integration
- API contracts
- worker integration
- provider/ASN synchronization
- monitoring data integration

Do not expose unnecessary implementation details to beginner users.

---

# 5. Simplified Builder Installation Is a Major Requirement

The existing builder documentation is technically accurate but is too advanced for the primary installation experience.

Create a **single, clean, beginner-oriented builder installation path**.

The target user is:

> A person with a freshly installed VPS who can connect through SSH and follow commands, but does not necessarily understand Linux administration, BGP, ASN, RIPE RIS, PostgreSQL, Docker, or cryptography.

The user should be able to follow the guide sequentially.

The guide should feel like:

1. Connect to your VPS.
2. Install the required prerequisites.
3. Clone the repository.
4. Generate the snapshot signing key.
5. Configure the builder.
6. Start the builder.
7. Run the first build.
8. Verify the snapshot.
9. Enable automatic execution.
10. Check that everything is working.

Do not require the user to understand the architecture before completing these steps.

---

# 6. Builder Installation Must Reflect the Actual Implementation

Inspect the implementation before writing the guide.

Verify:

- repository URL
- required packages
- Docker requirements
- Docker Compose requirements
- builder commands
- key generation command
- environment variables
- configuration files
- systemd services
- systemd timers
- PostgreSQL requirements
- MaxMind requirements
- RIPE RIS requirements
- object storage requirements
- snapshot publishing
- verification commands
- update procedure
- rollback procedure

Do not invent commands.

Do not document hypothetical workflows.

If the current implementation has a limitation, document the limitation rather than hiding it.

---

# 7. Snapshot Signing Key

The simplified installation guide must explicitly include a step for generating the snapshot signing key.

Explain this in plain language.

The reader should understand:

- why the key exists
- what the private key does
- what the public key does
- where each is used
- why the private key must remain secret
- what happens if the private key is lost
- what happens if the private key is compromised

Use a clear security warning.

The installation guide should make this a deliberate installation step rather than hiding it inside advanced configuration.

---

# 8. Builder Configuration

The simplified builder guide must include a clear configuration section.

Do not merely provide a large environment-variable dump.

Organize configuration into understandable groups.

For every important setting explain:

- what it does
- whether it is required
- where the value comes from
- whether it is secret
- what the operator should enter

For example:

| Setting | Required? | What it does | What to enter |
| ------- | --------- | ------------ | ------------- |

Explain important settings including, where applicable:

- PostgreSQL connection
- VPS Advisor URL
- VPS Advisor service token
- RIPE RIS source
- RIPE data cache
- RIPE data freshness
- MaxMind GeoIP
- snapshot signing key
- worker compatibility
- target limits
- snapshot sanity checks
- snapshot retention
- artifact/object storage

Use the actual configuration variables implemented by the project.

Do not invent configuration values.

---

# 9. Separate Simple Configuration From Advanced Configuration

The beginner installation guide should only expose the settings that an ordinary operator needs.

If there are advanced settings, do not overwhelm the installation guide.

Create or retain a separate configuration reference containing:

- every configuration variable
- defaults
- accepted values
- technical behaviour
- advanced tuning

The simplified guide should link to that reference.

---

# 10. Builder Installation Should Be Linear

The main installation guide should not force the reader to jump between multiple documents.

The basic journey should be continuous.

For example:

## Step 1

Prepare the VPS.

## Step 2

Download the project.

## Step 3

Generate the signing key.

## Step 4

Configure the builder.

## Step 5

Start the builder.

## Step 6

Run the first build.

## Step 7

Verify the result.

## Step 8

Enable automatic execution.

## Step 9

Learn how to update it.

## Step 10

Troubleshoot common problems.

Advanced documentation may be linked where necessary.

---

# 11. Explain Commands

Every command in beginner-facing documentation should have a short explanation.

Do not provide unexplained blocks of commands.

For example:

```bash
docker --version
```

Then explain:

> This confirms that Docker is installed correctly. You should see the installed Docker version.

Keep explanations concise.

---

# 12. Verification After Major Steps

Where practical, every important installation step should have a verification command.

Examples:

- Docker installed
- repository downloaded
- signing key generated
- configuration accepted
- builder starts
- database connection works
- RIPE data downloaded
- snapshot generated
- snapshot signed
- snapshot published
- automatic schedule enabled

The user should never reach the end of the guide without knowing whether the installation succeeded.

---

# 13. Troubleshooting

Keep troubleshooting beginner-friendly.

Cover the most likely problems.

Examples:

- Docker is unavailable
- repository cannot be cloned
- configuration validation fails
- PostgreSQL connection fails
- VPS Advisor authentication fails
- RIPE RIS download fails
- MaxMind database is unavailable
- snapshot generation fails
- sanity check blocks publication
- signing verification fails
- systemd timer is not running

For every issue explain:

1. What the problem usually means.
2. What to check.
3. What command to run.
4. What a healthy result looks like.
5. What to do next.

Link to advanced troubleshooting/runbooks when appropriate.

---

# 14. Keep Architecture Documentation Separate

Do not duplicate the complete builder architecture inside the simplified installation guide.

The installation guide may briefly explain:

> The builder downloads routing information, processes it, creates a signed snapshot, and publishes it for workers.

Then link to the architecture documentation for readers who want to understand:

- RIPE RIS
- MRT
- ASN
- prefix extraction
- deduplication
- enrichment
- validation
- signing
- publication

---

# 15. Consolidate Related Documentation

Audit the entire `docs/` directory for opportunities to merge related content.

Look particularly for documents that duplicate:

- installation instructions
- deployment instructions
- configuration
- monitoring
- security
- worker setup
- API integration
- architecture explanations
- operational procedures

Where appropriate, consolidate them into a stronger document.

Do not create unnecessary hierarchy.

The documentation tree should be understandable at a glance.

---

# 16. Preserve Important Technical Documentation

Do not simplify everything.

The project still needs comprehensive technical references.

Keep detailed documentation for:

- architecture
- database
- API contracts
- security/trust model
- lifecycle
- deployment
- risk assessment
- operations
- monitoring
- backups
- recovery
- upgrades
- load testing
- development
- integration

The goal is not to remove technical depth.

The goal is to put technical depth in the correct place.

---

# 17. Documentation Navigation

After consolidation:

- update `README.md` files
- update internal links
- remove broken links
- remove references to deleted documents
- update navigation
- ensure every document is discoverable
- ensure there are no orphaned documents
- ensure terminology is consistent

A user should be able to navigate from:

Documentation Home

→ Getting Started

→ Builder

→ Worker

→ VPS Advisor Integration

→ Operations

→ Reference

without encountering duplicate or contradictory information.

---

# 18. Terminology Consistency

Use the project's actual terminology consistently.

Do not alternate unnecessarily between:

- builder / routing builder / collector
- worker / community worker
- snapshot / routing snapshot
- VPS Advisor / Advisor
- RIPE RIS / RIPE
- monitoring platform / network intelligence platform

Where multiple terms are valid, establish the preferred term and use it consistently.

Update the glossary where necessary.

---

# 19. Do Not Hide Important Operational Details

Even though the builder installation guide is simplified, it must still clearly explain:

- what credentials are required
- what secrets must be protected
- what data is downloaded
- where snapshots are stored
- how snapshots are published
- how often the builder runs
- how workers consume snapshots
- how to verify successful operation
- how to recover from failures

Simplify the explanation, not the truth.

---

# 20. Documentation Quality Audit

After restructuring the documentation:

Check for:

- duplicate explanations
- contradictory instructions
- outdated commands
- broken links
- incorrect paths
- stale environment variables
- obsolete architecture descriptions
- inconsistent terminology
- missing prerequisites
- undocumented required credentials
- unexplained commands
- excessive technical depth in beginner guides

Fix these issues.

---

# 21. Do Not Generate Documentation From Assumptions

The repository is the source of truth for implementation-specific information.

Before documenting something, verify it in the project.

If the documentation currently says one thing but the implementation does another:

- determine the actual current behaviour
- update the documentation accordingly
- clearly identify significant discrepancies in your final report

Do not silently invent a solution.

---

# 22. Final Documentation Structure

After consolidation, the documentation should have a clean information architecture.

You may change the existing structure if necessary.

Prefer something conceptually similar to:

```text
docs/
├── README.md
├── getting-started/
├── concepts/
├── architecture/
├── builder/
├── worker/
├── integration/
├── operations/
├── development/
├── reference/
└── demos/
```

This is guidance, not a rigid requirement.

Use your judgment based on the actual contents of the repository.

The important requirement is that related information is grouped logically and redundant documents are merged.

---

# 23. Final Builder Installation Standard

The final simplified builder guide should allow a non-technical operator to go from:

> Fresh VPS

to:

> Running builder + successfully published signed routing snapshot

without requiring assistance from a developer.

The guide should be practical, sequential, concise, and reassuring.

Avoid unnecessary jargon.

Use copy-and-paste commands.

Explain what each step accomplishes.

Provide verification after important steps.

Provide clear warnings for secrets.

Provide troubleshooting when something fails.

---

# 24. Final Deliverables

After completing the documentation work:

1. Consolidate related documentation.
2. Rewrite the builder installation experience.
3. Update the documentation navigation.
4. Update all affected internal links.
5. Remove redundant documentation where appropriate.
6. Preserve comprehensive technical references.
7. Ensure the simplified builder guide is the recommended beginner path.
8. Ensure configuration reference remains comprehensive.
9. Ensure architecture documentation remains technically detailed.
10. Ensure there are no contradictory instructions.

Finally, provide a concise implementation report containing:

### Documentation merged

List documents that were consolidated.

### Documentation removed

List documents that became unnecessary.

### Documentation created

List genuinely new documents.

### Documentation rewritten

List major documents significantly rewritten.

### Important discrepancies found

List any differences discovered between existing documentation and the actual implementation.

### Remaining documentation gaps

List anything that could not be documented accurately because the implementation does not currently provide enough information.

Do not modify application code unless a documentation problem reveals a genuine implementation inconsistency that must be reported.
