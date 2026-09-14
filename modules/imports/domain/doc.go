// Package domain is the reading and validating half of spreadsheet import: it
// turns the file a customer already keeps their tax book in into drafts the
// ordinary create routes could have been given by hand, and reports everything
// wrong with it in one pass.
//
// # What it imports
//
// Entities and their obligations — the standing tax book a pilot has to bring
// in. Workflows and task instances are deliberately NOT importable: they are
// generated work, not recorded facts, and a customer who expects their
// spreadsheet's workflow columns to arrive is told so by name (the columns are
// reported as not imported rather than silently dropped).
//
// # Where validation lives
//
// Server-side, and against the domain rather than against a schema of this
// package's invention. An entity row is checked with the same bounds the
// single-record body carries (modules/entities/dto.CreateEntityBody) and then
// run through entities/domain.ValidateFiscalConfig — literally the function the
// create use case calls — so a row that imports cleanly could have been typed
// in by hand, and a row the API would refuse is refused here too.
//
// That principle decides severity. An Issue is an ERROR only when (a) the
// single-record route's own validation would reject the value, or (b) the file
// is incoherent in a way a JSON body cannot be, because a body arrives already
// structured: a value that cannot be read at all, one field claimed by two
// columns, two mutually exclusive deadline strategies with no declared winner,
// a strategy declared without the values it needs. Everything else — legal but
// probably not meant — is a WARNING and never blocks a commit.
//
// # All or nothing
//
// One error anywhere refuses the whole file: a half-imported tax book is worse
// than a rejected one. ParseResult.Committable is that rule, and it is the only
// question the commit half has to ask.
//
// # Matching existing records
//
// A commit must be safe to repeat, so every draft carries the natural key a
// second import of the same file matches on (see NormalizeKey):
//
//   - an entity is its name, folded — the file's own parent column already
//     references entities by name, so a name that is not unique cannot be
//     imported at all;
//   - an entity obligation is (entity, obligation type) — the pairing the
//     product has always treated as unique (seed/demo/spec: "Exactly one may
//     exist per (entity, obligationType)").
//
// Resolving a key to a record, and everything that needs the database or the
// requester's grants, belongs to the committing half. Nothing here reads or
// writes anything.
package domain
