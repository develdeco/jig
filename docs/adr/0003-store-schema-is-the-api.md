# Store schema is the API

Jig's concerns (intake, frontier, verifydeliver, tracker projection) need to evolve independently without breaking each other. Each one reads and writes only its own store subtrees, and `schema_version` gates compatibility across them. This constraint on the store's schema, not any packaging boundary, is what keeps the four concerns independent.
