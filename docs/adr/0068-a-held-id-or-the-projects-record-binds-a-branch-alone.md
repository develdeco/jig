# A held id or the project's record binds a branch alone

A branch leads with a ticket's id or with its GitHub issue number, and binding it checked that no other ticket claimed the same one: the id as an old one under `aliases:` (`TICKET_ALIAS_COLLISION`), the number through a tracker record of any repository (`TICKET_REMOTE_ID_COLLISION`). To see that, binding read every ticket's record, and for a number every tracker record too. The tool-call hook binds the branch at every call, so each call cost grew with the store, against the project's rule that what runs on every tool call reads only the tickets it names.

## Decision

Two leads are settled by what they name. Nothing else is read and no duplicate-id check is made, in every command that takes its ticket from a branch.

- **A led id the store holds.** A branch led by `KEY-N`, where the store holds ticket `KEY-N` (its folder holds more than jig's lock and temporary files), binds `KEY-N`, even where another ticket lists `KEY-N` as an old id.
- **A led number the project's record settles.** A branch led by `N`, where the record of item `N` in the project's own GitHub tracker repo names a ticket the store holds under that id, binds that ticket by reading that one record, even where another record gives `N` to another ticket.

Every other lead is looked up across the store, and refused as before when two tickets claim it. That covers an old id with no ticket of its own, and a number whose record under the project's repo is missing (it may sit under another repository, in a ticket's folder or in the flat layout), cannot be read, names a chart or no ticket, or names an old id.

The refusals stand where a person acts on an id: an id typed to a command resolves through `ResolveTicket`, and `jig validate` checks the whole store.

## Rejected

- **Only the hook skips the check.** One branch would then have two answers: the hook would record a call on a ticket that `jig here` refuses to name.
- **A stored index of old ids, or of tickets by issue number.** It would keep the check cheap, at the price of a second copy of the records that every writer and every pull must keep true, and a store migration.

## Consequences

A clash between a held id and another ticket's alias, or between the same issue number in two repositories, no longer stops a branch from binding. `jig validate` is where an alias clash shows. A tool call on a branch that is settled this way costs the same whatever the number of tickets.
