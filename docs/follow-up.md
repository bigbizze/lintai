# Follow-up

Work that is not planned for the current release.

- **Lint unsaved editor buffers.** The ESLint plugin sends only file names to `lintai serve`, and the server reads every file from disk, so diagnostics follow the last save. To lint the editor buffer, the plugin must send the buffer text, the server must lay it over the disk file, and the analysis must be incremental: extract facts again only for the changed files and evaluate the rules again on the updated facts. A full analysis on every pause in typing is too slow.
- **Locations for rule-created facts.** A rule can create entities in `facts()`, but it cannot give them a `location`, because `location` is a base relation. A violation that is anchored on such an entity therefore fails or must anchor on its module, and the diagnostic is at line 1. Let `facts()` return `location` rows for ids that it creates. Reject an id that is already a base entity, so that a rule cannot move the location of an extracted fact. `lookupLocation` in `engine/program.go` must then read the rule fact store too.
