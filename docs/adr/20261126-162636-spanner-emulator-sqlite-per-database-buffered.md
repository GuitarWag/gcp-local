---
id: 20261126-162636-spanner-emulator-sqlite-per-database-buffered
title: "Spanner emulator: SQLite per database, buffered read-write transactions"
status: proposed
date: 2026-11-26
branch: feat/spanner-data-path-r3
worktree: feat-spanner-data-path--b3730a7674d1b4f0
session: ec54fbfe-c845-4279-8fe9-a1e3bc8be1cb
affects:
  - internal/services/spanner/**
supersedes: []
superseded_by: []
depends_on: [20261125-231025-adopt-adrlog-cli-for-architecture-decision]
journal_refs: []
tags: [spanner, engine]
---

## Context

Issue #3 replaces the Spanner gRPC stub with a data path that the real
`cloud.google.com/go/spanner` client can use. The emulator needs a SQL
engine, a storage model per database, and read-write transaction semantics.
The first review of PR #48 found that DML inside a transaction autocommitted:
Rollback did not revert it, and a failed Commit kept it.

## Decision

1. Each Spanner database is one in-memory SQLite database
   (`modernc.org/sqlite`, pure Go, already a dependency for BigQuery) with
   one connection. Spanner DDL is translated to SQLite DDL. BOOL columns are
   declared `BOOLEAN` so that the declared type keeps the BOOL type code.
2. A read-write transaction is a server-side buffer of DML statements keyed
   by transaction id. DML under a transaction id (ExecuteSql,
   ExecuteBatchDml, inline Begin) goes into the buffer and does not change
   committed data. Commit replays the buffer and then applies the mutations
   in one SQLite transaction. Rollback drops the buffer.
3. A statement inside a transaction replays the buffer in a SQLite
   transaction, runs, and rolls back. This gives read-your-writes without an
   open SQLite transaction across RPCs.
4. Partitioned DML and single-use requests autocommit.
5. A malformed key or value returns `InvalidArgument`.

## Alternatives considered

- **Hold an open SQLite transaction per Spanner transaction.** The single
  connection per database would block every other session until Commit.
  More connections would split the in-memory database.
- **Buffer DML without executing it.** The row count of each statement and
  read-your-writes would be unavailable.

## Consequences

- Each statement in a transaction replays all earlier DML of that
  transaction (O(n²) in statements). This is acceptable for test workloads.
- There is no lock-based conflict detection, so the emulator never returns
  `ABORTED`. Concurrent transactions apply in commit order.
- Partitioned queries, interleaved tables, and secondary indexes stay out
  of scope.
