oas-indexer

CLI to index OpenAPI fragments into a reference-style root file, and optionally bundle and render docs via Redocly.

Quick start
- Build: `go build ./cmd/oas-indexer`
- Generate refs + bundle + HTML: `./oas-indexer --input example --all --redocly-config redocly.yaml`
- Validate API paths: `./oas-indexer --input example --validate google`

Conventions
- Fragments live under `paths/` and `components/{schemas,parameters}/`
- Running the tool appends `$ref` entries into `<input>/root.yaml` automatically
- `--all` writes `dist/openapi.yaml` and `dist/index.html`

Validation
The tool includes a validation engine with predefined rulesets to ensure API paths follow best practices:

- `--validate <preset>`: Run validation with specified preset (google, restful)
- `--list-presets`: Show available validation presets
- `--validate-stop-on-error`: Stop on first validation error
- `--skip-validation`: Skip validation entirely

Available presets:
- `google`: Google API Design Guide best practices (8 rules)
- `restful`: Common RESTful API standards (4 rules)

If validation fails, the program stops with exit code 1, preventing bundling/HTML generation.

