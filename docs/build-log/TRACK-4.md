# Drift is reported only when jig would overwrite the field (TRACK-4)

Ambiguous: state, Status and Store ID reported drift when GitHub differed from `synced:`, even when GitHub already held the value the store would write (for example GitHub's automation closed the issue and set Done before the store's closure synced). Nothing is overwritten then, so reporting drift was noise.

Chosen: a field reports drift only when GitHub differs from both `synced:` and the store's value. The matching-state branch in `updateIssues` reports nothing and records the value in `synced:`; `placeBoardItems` adds the store's value as the second condition for `status` and `store_id`. Title, body and link drift keep their rules, since jig writes in those cases. The dry run follows the same rule.
