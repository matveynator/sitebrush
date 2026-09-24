# Database single-writer architecture

For SQLite, Chai, and DuckDB, runtime database access follows a single-writer channel-oriented model.

## Rule

Only the serialized database worker may execute runtime writes against single-writer engines.

Application code must not call writable database methods directly through `db.DB`, including:

- `Exec`
- `ExecContext`
- `Query`
- `QueryContext`
- `QueryRow`
- `QueryRowContext`
- `Begin`
- `BeginTx`
- `Conn`
- `Prepare`
- `PrepareContext`

Runtime work is submitted as:

```text
consumer
  -> task + reply channel
  -> serialized database worker
  -> result through reply channel
```

Use `withSerializedConnectionFor(...)` to submit database work to the worker.

The worker owns the database operation and coordinates tasks using channels and `select/case`. Do not introduce mutexes, shared mutable maps, or parallel direct writers to work around this model.

For long-running operations, split work into bounded jobs where practical so the worker can preserve fairness, backpressure, and responsiveness between workload lanes.

Bootstrap and schema initialization may access `db.DB` directly only before concurrent runtime traffic starts. Database-specific paths for engines that are not single-writer, such as PostgreSQL COPY, may use direct connections when explicitly isolated and documented.

The test `TestSingleWriterPackagesDoNotBypassSerializedPipeline` enforces this rule and reports the offending file, line, function, direct method, and the required rewrite pattern.
