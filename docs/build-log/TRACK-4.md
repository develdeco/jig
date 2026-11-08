# Drift is reported only when jig would overwrite the field (TRACK-4)

Ambiguous: state, Status and Store ID reported drift when GitHub differed from `synced:`, even when GitHub already held the value the store would write (for example GitHub's automation closed the issue and set Done before the store's closure synced). Nothing is overwritten then, so reporting drift was noise.

Chosen: a field reports drift only when GitHub differs from both `synced:` and the store's value. The matching-state branch in `updateIssues` reports nothing and records the value in `synced:`; `placeBoardItems` adds the store's value as the second condition for `status` and `store_id`. Title, body and link drift keep their rules, since jig writes in those cases. The dry run follows the same rule.

Provenance: every call above was user-confirmed on 2026-11-07, as the ticket's brief records.

Cross-clone report: not a separate fix. A record update that waits in one clone's working tree leaves that clone's `synced:` stale, but under this rule a stale `synced:` no longer matters when GitHub equals the store's value, which is the whole case the report describes. A stale `synced:` still lets real drift through when GitHub differs from the store, which is correct. Making the record travel with every checkpoint is store write-back policy, not the mirror's drift rule, and is out of scope.
