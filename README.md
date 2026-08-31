# Ledger

> A small transactional backend service written in Go and backed by PostgreSQL.

The project models accounts, money transfers, an immutable transaction history, and reliable publication of transfer events. Its purpose is not to imitate a complete bank. Its purpose is to force the implementation of the parts of backend development where correctness matters: transactions, concurrency, idempotency, failure handling, database consistency, graceful shutdown, and testing.

The implementation language is **Go**. PostgreSQL is the primary datastore.

---

## 1. Project goal

Build a backend service that can safely move money between accounts even when:

- multiple transfers happen concurrently;
- the same request is sent more than once;
- one part of an operation fails;
- the process is shutting down;
- database operations are cancelled or time out;
- an event cannot be published immediately.

When the project is finished, you should be able to explain **why the system is correct**, not merely demonstrate that the happy path works.

This repository should also demonstrate that you can independently learn and use Go for a backend problem without treating Go as "C++ with different syntax".

---

## 2. Learning contract

This project is intentionally underspecified at the implementation level.

The requirements describe **observable behaviour and guarantees**. They do **not** prescribe:

- the package layout;
- database schema;
- SQL queries;
- locking strategy;
- transaction isolation level;
- concrete HTTP router;
- migration library;
- dependency-injection approach;
- repository/service/controller architecture;
- worker implementation;
- retry strategy.

You are expected to research those decisions and justify them yourself.

### Rules

- Do not copy a ready-made implementation of a banking/ledger service.
- Do not use generated application code as a substitute for understanding.
- Prefer official documentation, source code, debugger/profiler output, experiments, and small isolated reproductions.
- When something fails, understand the failure before replacing the component or changing the approach.
- Do not add a library merely because it hides a problem you do not yet understand.
- Every concurrency or consistency guarantee must have a test or reproducible experiment behind it.
- Keep the project small enough to finish.

### Engineering journal

Maintain a lightweight `docs/decisions.md`.

For every non-trivial problem, record:

1. the problem;
2. your initial hypothesis;
3. what you tried;
4. what actually caused it;
5. the solution you chose;
6. alternatives you rejected and why;
7. the source/documentation that helped.

This does not need to be polished prose. The goal is to preserve your reasoning.

---

## 3. Functional scope

### 3.1 Accounts

The service must support accounts with at least:

- a stable unique identifier;
- a currency;
- a balance;
- creation timestamp.

Required operations:

- create an account;
- retrieve an account;
- retrieve its current balance;
- retrieve its ledger/history.

For the MVP, an account uses exactly one currency.

### 3.2 Transfers

The service must support transferring money from one account to another.

A transfer contains at least:

- a stable unique identifier;
- source account;
- destination account;
- amount;
- currency;
- status;
- creation timestamp.

A transfer must be rejected when:

- amount is zero or negative;
- source and destination are the same account;
- either account does not exist;
- currencies do not match;
- the source account has insufficient funds;
- the request is malformed.

A successful transfer must debit one account and credit the other.

### 3.3 Money representation

Floating-point arithmetic must **not** be used for monetary values.

Choose a representation with exact semantics and document the decision.

### 3.4 Ledger

Every successful transfer must produce immutable accounting history.

The ledger must make it possible to answer:

- why an account has its current balance;
- which transfer caused a debit or credit;
- when the operation happened;
- what amount and currency were involved.

Existing ledger entries must never be edited to "fix" history.

If correction functionality is ever added, it should create compensating operations rather than mutate old records.

---

## 4. Correctness requirements

These requirements are the core of the project.

### 4.1 Atomicity

A transfer is a single logical operation.

The following state is forbidden:

- source account debited;
- destination account not credited.

The inverse partial state is also forbidden.

If any required part of a transfer fails, the persistent state must remain logically consistent.

### 4.2 Balance conservation

For transfers inside one currency, money must not appear or disappear as a consequence of a transfer.

Ignoring explicit account creation/funding functionality:

`sum(balances before) == sum(balances after)`

A transfer changes ownership of money, not the total amount.

### 4.3 No negative balances

Concurrent requests must not allow an account to spend more money than it owns.

It is not sufficient for this to work only when requests are processed sequentially.

### 4.4 Concurrent transfers

The service must behave correctly when:

- many clients transfer from the same account;
- two accounts transfer to each other simultaneously;
- unrelated accounts transfer simultaneously;
- multiple requests target the same destination.

Your solution must avoid data corruption and must have a documented policy for database concurrency conflicts and deadlocks if they occur.

### 4.5 Idempotency

Creating a transfer must support an idempotency mechanism.

If a client sends the same logical request again because it did not receive the original response, the transfer must not be applied twice.

The behaviour must be deterministic for:

- same idempotency key + same request;
- same idempotency key + different request.

Document the semantics you choose.

### 4.6 Stable transfer identity

Once a transfer succeeds, repeated reads must refer to the same persistent transfer record.

A retry must not silently create a second transfer with a different identity.

---

## 5. HTTP API requirements

Expose a JSON HTTP API.

At minimum, provide operations equivalent to:

- create account;
- get account;
- create transfer;
- get transfer;
- get account ledger/history;
- health/readiness check.

Exact routes and payload shapes are your decision.

### API behaviour

The API must:

- use appropriate HTTP status codes;
- return structured JSON errors;
- validate input at the boundary;
- distinguish client errors from server errors;
- propagate request cancellation where practical;
- use request-scoped timeouts where appropriate;
- avoid leaking internal database errors or secrets to clients.

Document the API in the repository.

OpenAPI/Swagger is optional.

---

## 6. PostgreSQL requirements

PostgreSQL is the source of truth.

The database must enforce important invariants where practical rather than relying exclusively on application code.

You must understand and document:

- what transaction boundaries exist;
- which transaction isolation level is used and why;
- what happens under concurrent modification;
- whether explicit row locking is needed;
- what can deadlock;
- how failures are retried or surfaced;
- which constraints belong in the database;
- which indexes are necessary and why.

Database migrations must be version-controlled.

A fresh database must be reproducibly initialized from the repository.

---

## 7. Go requirements

The project should demonstrate idiomatic use of Go rather than only successful compilation.

You should become comfortable with at least:

- modules and packages;
- structs and methods;
- interfaces where they provide an actual abstraction;
- error values and error wrapping;
- `context.Context`;
- goroutines;
- channels where message passing is genuinely useful;
- synchronization primitives where shared state requires them;
- `defer`;
- HTTP servers and handlers;
- JSON encoding/decoding;
- database access;
- tests;
- table-driven tests;
- graceful shutdown;
- race detection.

### Constraints

- Do not create interfaces for every type by default.
- Do not use goroutines simply to demonstrate goroutines.
- Do not use channels where ordinary synchronous code is clearer.
- Do not introduce a web framework unless you can explain what it gives you over the standard library.
- Do not reproduce C++ architecture mechanically in Go.
- Prefer simple, explicit code until complexity is justified.

---

## 8. Transactional outbox

A successful transfer must create an event describing the completed transfer.

The event must be persisted reliably using the **Transactional Outbox** pattern.

Required guarantee:

> If the transfer commits, its event must not be permanently lost because the process crashed between the database write and publication.

The transfer and its corresponding outbox record must therefore have a correctness relationship that survives process failure.

A background worker must process pending outbox events.

For the initial version, the publication target may be deliberately simple. A real message broker is **not required** for project completion.

The outbox implementation must account for:

- process restart;
- failed publication;
- duplicate delivery;
- event ordering where relevant;
- multiple workers if you choose to support them;
- marking/recording successful delivery without losing events.

Do not claim "exactly once" delivery unless you can define and prove what exactly that means in your system.

---

## 9. Lifecycle and failure handling

### Startup

On startup, the service must:

- validate required configuration;
- establish that PostgreSQL is reachable before declaring readiness;
- fail clearly when required infrastructure is unavailable.

### Runtime

The service must handle:

- invalid input;
- database timeout/cancellation;
- temporary database errors;
- transaction conflicts;
- unexpected internal errors;
- failed outbox publication.

### Shutdown

On `SIGINT`/`SIGTERM`, the service must shut down gracefully.

The shutdown procedure should define what happens to:

- new HTTP requests;
- in-flight HTTP requests;
- database operations;
- background workers;
- pending outbox work;
- database connections.

The process must not rely on abruptly killing goroutines.

---

## 10. Configuration

Runtime configuration must come from environment/configuration rather than hardcoded machine-specific values.

At minimum, externalize:

- database connection information;
- HTTP listen address/port;
- relevant timeouts;
- worker settings that reasonably need configuration.

Secrets must not be committed to Git.

Provide an example environment file if useful, but never include real credentials.

---

## 11. Local development

The project must be runnable on a clean development machine with a short, documented sequence.

Use Docker Compose for infrastructure.

The expected developer experience should be roughly:

1. clone repository;
2. start PostgreSQL/infrastructure;
3. apply migrations;
4. start service;
5. run a simple request;
6. run tests.

You may automate these steps with `Makefile`, scripts, or another lightweight mechanism.

The exact tooling is your choice.

---

## 12. Testing requirements

Tests are part of the project, not an optional cleanup phase.

### 12.1 Unit tests

Cover business rules that can be tested without a real database.

At minimum test validation around:

- invalid amounts;
- same source/destination;
- currency mismatch;
- insufficient funds;
- malformed requests or domain values where applicable.

### 12.2 Integration tests

Run important persistence behaviour against a real PostgreSQL instance.

At minimum verify:

- successful transfer;
- rollback on failure;
- persisted balances;
- persisted ledger entries;
- transfer lookup;
- idempotent retry;
- outbox persistence.

Mocks are not sufficient for validating transaction and locking behaviour.

### 12.3 Concurrency tests

Create tests or reproducible stress scenarios that issue transfers concurrently.

At minimum demonstrate:

#### Overspending scenario

Given one account with a finite balance, issue enough simultaneous withdrawals/transfers that they cannot all succeed.

After completion:

- successful transfers must not exceed available funds;
- balance must never become negative;
- failed operations must not partially mutate state.

#### Conservation scenario

Run many concurrent transfers between a fixed set of accounts.

After completion:

- every balance is valid;
- total money is unchanged;
- successful transfers have consistent ledger records.

#### Idempotency scenario

Send the same transfer request concurrently more than once using the same idempotency key.

The money movement must happen once.

### 12.4 Go race detector

The project must pass:

```text
go test -race ./...
```

A passing race detector does not prove the database logic is correct; it only addresses Go memory races.

### 12.5 Static checks

Before declaring the project complete:

```text
gofmt
go vet ./...
go test ./...
go test -race ./...
```

Additional linters are optional.

---

## 13. Observability

Keep observability intentionally small but useful.

The service must have structured or consistently formatted logs for important lifecycle events and failures.

Logs should make it possible to understand:

- service startup/shutdown;
- failed requests;
- failed transfers;
- database errors;
- outbox processing failures.

Do not log:

- database passwords;
- secrets;
- unnecessary sensitive payloads.

Request IDs/correlation IDs are recommended but not mandatory.

Metrics and tracing are optional extensions.

---

## 14. Security baseline

This is not a production bank, but obvious security mistakes should still be avoided.

Required:

- parameterized database queries;
- input validation;
- no secrets in source control;
- no raw internal errors returned to clients;
- reasonable request/body limits;
- sane HTTP timeouts.

Authentication and authorization are explicitly outside the MVP scope.

---

## 15. Non-goals

The following are **not required** for the project to be considered finished:

- frontend;
- mobile app;
- user registration/login;
- OAuth;
- JWT;
- real bank integration;
- cards;
- payment gateways;
- loans;
- interest;
- multi-currency exchange;
- currency conversion;
- authentication/authorization;
- Kafka;
- RabbitMQ;
- Kubernetes;
- cloud deployment;
- service mesh;
- microservice decomposition;
- distributed transactions across multiple databases;
- event sourcing;
- CQRS;
- GraphQL;
- gRPC;
- Prometheus/Grafana;
- OpenTelemetry;
- complex admin panels.

Do not add these while mandatory checklist items remain unfinished.

---

## 16. Suggested milestones

The milestones define outcomes, not implementation instructions.

### Milestone 0 — Go foundations

- [ ] Install/configure Go toolchain.
- [ ] Create the module.
- [ ] Understand package/module basics.
- [ ] Be able to run tests.
- [ ] Be able to debug the program.
- [ ] Read enough official Go documentation to understand the language's error and concurrency model.

### Milestone 1 — Service skeleton

- [ ] HTTP server starts.
- [ ] Configuration is externalized.
- [ ] PostgreSQL is available locally.
- [ ] Application can connect to PostgreSQL.
- [ ] Health/readiness endpoint exists.
- [ ] Graceful shutdown works at a basic level.
- [ ] Fresh environment setup is documented.

### Milestone 2 — Accounts

- [ ] Accounts can be created.
- [ ] Accounts can be read.
- [ ] Monetary representation is decided and documented.
- [ ] Database migrations are reproducible.
- [ ] Invalid account data is rejected.

### Milestone 3 — Sequential transfers

- [ ] Transfer endpoint exists.
- [ ] Valid transfer moves money.
- [ ] Invalid transfer changes nothing.
- [ ] Insufficient funds are handled.
- [ ] Ledger entries are persisted.
- [ ] Transfer record can be retrieved.
- [ ] Transaction atomicity is tested.

### Milestone 4 — Concurrency

- [ ] Concurrent overspending is impossible.
- [ ] Concurrent transfers remain consistent.
- [ ] Deadlock/concurrency behaviour is understood.
- [ ] Chosen PostgreSQL isolation/locking strategy is documented.
- [ ] Stress/concurrency test exists.

### Milestone 5 — Idempotency

- [ ] Client can provide an idempotency key.
- [ ] Retrying the same request does not move money twice.
- [ ] Concurrent duplicate requests do not move money twice.
- [ ] Key reuse with different payload has defined behaviour.
- [ ] Idempotency semantics are documented and tested.

### Milestone 6 — Transactional outbox

- [ ] Completed transfer creates an outbox event.
- [ ] Failed/rolled-back transfer cannot leave a valid completion event behind.
- [ ] Background worker processes pending events.
- [ ] Publication failure does not permanently lose an event.
- [ ] Restart behaviour is tested.
- [ ] Duplicate-delivery semantics are understood and documented.

### Milestone 7 — Reliability

- [ ] Request cancellation reaches long-running work where appropriate.
- [ ] HTTP/server timeouts are deliberate.
- [ ] PostgreSQL failures are handled cleanly.
- [ ] Shutdown stops accepting new work.
- [ ] In-flight work has a defined shutdown policy.
- [ ] Background worker terminates cleanly.
- [ ] No goroutine is expected to "just die with the process".

### Milestone 8 — Tests and polish

- [ ] Unit tests cover core rules.
- [ ] Integration tests use real PostgreSQL.
- [ ] Concurrency tests exist.
- [ ] Idempotency tests exist.
- [ ] Outbox failure tests exist.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./...` passes.
- [ ] `go vet ./...` passes.
- [ ] Code is `gofmt`-formatted.
- [ ] README describes how to run and test the final application.
- [ ] `docs/decisions.md` contains meaningful engineering notes.
- [ ] Git history consists of understandable incremental commits.

---

## 17. Definition of Done

The project is **finished** when every mandatory item below is true.

### Product behaviour

- [ ] A fresh user can run the project using only repository documentation.
- [ ] Accounts can be created and queried.
- [ ] Transfers can be created and queried.
- [ ] Account history can be queried.
- [ ] Money is represented exactly, without floating-point arithmetic.
- [ ] Invalid transfers cannot modify balances.
- [ ] Successful transfers are atomic.
- [ ] Balances cannot become negative through concurrent spending.
- [ ] Total money is conserved by transfers.
- [ ] Ledger history is immutable.
- [ ] Transfer requests are idempotent.
- [ ] Concurrent duplicate requests are idempotent.

### Database correctness

- [ ] Migrations can build the database from scratch.
- [ ] Important invariants are protected by database constraints where appropriate.
- [ ] Transaction boundaries are deliberate and documented.
- [ ] Isolation/locking decisions are deliberate and documented.
- [ ] Concurrency behaviour has been demonstrated with tests.
- [ ] Deadlock/conflict behaviour is understood rather than accidental.

### Outbox

- [ ] Transfer and outbox state cannot become inconsistent through an ordinary partial failure.
- [ ] Pending events survive restart.
- [ ] Failed publication can be retried.
- [ ] Duplicate publication is handled/documented.
- [ ] Rolled-back transfers do not produce valid completion events.

### Go quality

- [ ] The architecture feels natural in Go rather than copied mechanically from C++.
- [ ] Goroutines have clear owners/lifetimes.
- [ ] Channels are used only where justified.
- [ ] Context cancellation is understood and used intentionally.
- [ ] Errors contain useful context without losing their identity unnecessarily.
- [ ] Resource cleanup is explicit and reliable.
- [ ] No obvious goroutine leaks are known.
- [ ] `go vet ./...` passes.
- [ ] `go test ./...` passes.
- [ ] `go test -race ./...` passes.

### Repository quality

- [ ] Build/run/test instructions are correct.
- [ ] Architecture and important decisions are documented.
- [ ] API usage is documented.
- [ ] No secrets are committed.
- [ ] No generated junk/build artifacts are committed unnecessarily.
- [ ] Commit history tells the story of development.
- [ ] The repository can be shown to another engineer without a verbal disclaimer.

### Personal readiness

You should be able to answer these questions without reading the code:

- [ ] Why did you choose your monetary representation?
- [ ] Where exactly does a transfer transaction begin and end?
- [ ] What happens if the process crashes halfway through a transfer?
- [ ] Why can two simultaneous transfers not overspend an account?
- [ ] What PostgreSQL locks/isolation guarantees does your solution rely on?
- [ ] Can your transfer logic deadlock? Under what circumstances?
- [ ] What happens when the client times out but the database transaction commits?
- [ ] Why is idempotency required?
- [ ] What happens if the same idempotency key is reused with different data?
- [ ] Why is the outbox record written transactionally?
- [ ] What happens if event publication succeeds but acknowledgement/marking fails?
- [ ] Is your event delivery at-most-once, at-least-once, or exactly-once?
- [ ] Why did you use each goroutine in the program?
- [ ] Why did you use each channel in the program?
- [ ] How does graceful shutdown propagate through the application?
- [ ] What does `context.Context` solve in your implementation?
- [ ] What does the race detector detect, and what does it *not* detect?
- [ ] Which bugs are covered only by integration/concurrency tests?
- [ ] What would you redesign if this service had to handle production banking traffic?

If several answers are "because a tutorial did it that way", the project is not finished yet.

---

## 18. Optional extensions

Only start these **after the Definition of Done is complete**.

Good next steps:

- Kafka as the outbox publication target;
- a separate event consumer;
- gRPC + Protocol Buffers;
- Prometheus metrics;
- OpenTelemetry tracing;
- benchmark/load-testing scenarios;
- CI pipeline;
- retry with exponential backoff where justified;
- multiple outbox workers;
- PostgreSQL failure/restart testing;
- containerized application image;
- deployment to a small VPS/cloud instance;
- profiling and performance investigation.

Each extension should answer a real engineering question. Do not add infrastructure solely to enlarge the technology list.

---

## 19. Useful sources

Prefer primary documentation. Read the section relevant to the problem you are currently solving rather than trying to memorize everything in advance.

### Go

- Go documentation: https://go.dev/doc/
- A Tour of Go: https://go.dev/tour/
- Effective Go: https://go.dev/doc/effective_go
- Go specification: https://go.dev/ref/spec
- Go memory model: https://go.dev/ref/mem
- Package documentation: https://pkg.go.dev/
- `net/http`: https://pkg.go.dev/net/http
- `context`: https://pkg.go.dev/context
- `sync`: https://pkg.go.dev/sync
- `errors`: https://pkg.go.dev/errors
- `testing`: https://pkg.go.dev/testing
- Data race detector: https://go.dev/doc/articles/race_detector
- Go Code Review Comments: https://go.dev/wiki/CodeReviewComments

### PostgreSQL

- PostgreSQL documentation: https://www.postgresql.org/docs/current/
- Transactions tutorial: https://www.postgresql.org/docs/current/tutorial-transactions.html
- Transaction isolation: https://www.postgresql.org/docs/current/transaction-iso.html
- Explicit locking: https://www.postgresql.org/docs/current/explicit-locking.html
- Constraints: https://www.postgresql.org/docs/current/ddl-constraints.html
- Indexes: https://www.postgresql.org/docs/current/indexes.html

### Go + PostgreSQL

- pgx: https://pkg.go.dev/github.com/jackc/pgx/v5
- pgx connection pool: https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool
- pgx repository: https://github.com/jackc/pgx

### Transactional outbox

- AWS Prescriptive Guidance — Transactional Outbox:
  https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html

Treat the outbox article as an explanation of the problem and its guarantees, not as an implementation to copy.

### Docker

- Docker Compose documentation:
  https://docs.docker.com/compose/

---

## 20. Final constraint

The goal is not to maximize the number of technologies in the README.

The goal is to reach a point where you can open this repository during an interview and confidently explain every important decision, including the decisions that turned out to be wrong during development.

**Finish the boring correctness work before adding impressive infrastructure.**
