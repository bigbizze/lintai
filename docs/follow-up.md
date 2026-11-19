# Follow-up

Pending work and integration checks.

- **Lint unsaved editor buffers.** The ESLint plugin receives the buffer text and compares it with the saved file, but the server analyzes files on disk. In serve mode the plugin hides stale diagnostics and asks for a save when the text differs. To analyze an unsaved buffer, the client and server would need to pass and apply the text and update the analysis without making editor feedback too slow.
- **Locations for rule-created facts.** A rule can create entities in `facts()`, but reporting requires an eligible producer-owned anchor and location. A violation anchored on a rule-created entity therefore fails; anchoring on a module reports at the module's location. Supporting locations for rule-created entities would require a rule-owned reporting mechanism that cannot move a producer-owned entity's location.
