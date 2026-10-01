# Registration Telegram frontend

Read README. Independent Go 1.26.1 module; no sibling checkout required.
Backend owns canonical protobuf; this repo includes a versioned snapshot and generated client.
Never connect to PostgreSQL/SQLite here. Domain decisions and permissions belong to backend.
Use internal/resources for user-visible text, tgfmt.Escape for every dynamic value.
All content sends go through the common sender/limiter, never directly from polling handlers.
Do not log Telegram errors with raw URLs, tokens, updates or response bodies.

make install, format, check, test-race, build, compose-build. Tests use synthetic
data and fake Telegram HTTP; they never register webhooks or send live messages.
make run/up use real configuration and immediately start the frontend.
One frontend instance per bot. Do not start it against a live token during tests.
