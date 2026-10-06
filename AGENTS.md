# Registration Telegram frontend

Read README. Independent Go 1.26.1 module; no sibling checkout required.
Backend owns canonical protobuf; this repo includes a versioned .proto snapshot.
Update api/registration.proto explicitly and manually from an agreed backend revision;
keep it byte-identical to backends/registration/api/registration.proto, including go_package.
Builds never sync the snapshot or require sibling checkouts, BSR or remote plugins.
make install-proto installs Buf and local Go plugins into .bin; make proto-gen uses
Buf v2 inputs from buf.gen.yaml. Preserve paths=source_relative and
Mapi/registration.proto=registration.local/frontend/api for BOTH plugins.
Buf and gRPC plugin versions belong in versions.mk; the Go protobuf plugin version
comes from go.mod. Use make versions and reinstall after version changes.
make proto-check runs buf build and is part of make check; it checks schema compilation,
not snapshot equality or breaking changes. Breaking checks alone cannot verify RPC
semantics/delivery compatibility. After a coordinated snapshot update, run proto-check,
build and test-race. Never edit generated code.
Generated *.pb.go are ignored. Make generates before Go checks/build/run; Docker generates independently.
Never connect to PostgreSQL/SQLite here. Domain decisions and permissions belong to backend.
Use internal/resources for user-visible text, tgfmt.Escape for every dynamic value.
All content sends go through the common sender/limiter, never directly from polling handlers.
Webhook and polling share durable Accept; replay receipt IDs even on duplicates.
PendingInteractive is bounded recovery; Kafka is broadcast-only and must not block direct work.
Webhook registration/deletion are explicit CLI commands, never startup/shutdown.
Only attest accessible bot-owned text callbacks; edit fallback is restricted to definite uneditable errors.
Participant help/refusals are neutral; roles and permissions are backend decisions.
Do not log Telegram errors with raw URLs, tokens, updates or response bodies.

make install, format, check, test-race, build, compose-build. Tests use synthetic
data and fake Telegram HTTP; they never register webhooks or send live messages.
make run/up use real configuration and immediately start the frontend.
One frontend instance per bot. Do not start it against a live token during tests.

Read docs/OPERATIONS.md before delivery. CI checks/builds PR/push main; SSH CD
requires repository DEPLOY_ENABLED=true after provisioning/coordinated migration 004.
SSH workflow fast-forwards main to the checked SHA, then runs the same make up as locally.
make up builds and waits for healthchecks (180s); up, register-webhook and delete-webhook are live operations, never tests.
No automatic webhook registration/deletion, topic creation, imports or migrations in frontend CD.
Run backend make up first (it applies migrations), then frontend make up; see docs/OPERATIONS.md.
