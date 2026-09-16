# Tracker is a projection

The store, not the tracker, is truth: tracker adapters project tickets, subtasks, and comments outward from it, never the reverse. IDs are minted by the tracker where it owns the sequence (e.g. an issue tracker's own numbering), and from `ticket_format` otherwise — so a ticket has a stable id even before any tracker adapter runs.
