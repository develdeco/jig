# Tracker is a projection

The store, not the tracker, is truth: tracker adapters project tickets, subtasks, and comments outward from it, never the reverse. IDs are minted by the tracker where it owns the sequence (e.g. an issue tracker's own numbering), and from `ticket_format` otherwise - so a ticket has a stable id even before any tracker adapter runs.

**Superseded in part by [ADR 0017](0017-jig-mints-and-claims-every-ticket-id.md):** no tracker mints an id anymore. jig mints every id itself, from `ticket_format`, through the store, and claims it on the store's origin before anything else is written under it - the minting clause above no longer holds. The paragraph's wider claim, that the store is truth and a tracker only ever projects it outward, stands; ADR 0017 is its sharper form for ids specifically.
