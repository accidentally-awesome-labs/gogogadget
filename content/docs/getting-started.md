---
title: Getting started
description: From clone to running SaaS in ten minutes.
section: Start
weight: 2
---

Prerequisites: **Go 1.26+** and **Docker**.

```sh
make setup
docker compose up -d db
make seed
make dev
```

Open http://localhost:8080. The fresh clone runs the **full app with zero SaaS
accounts**: `.env.example` ships `DEV_AUTH_BYPASS=true`, and `make seed` loads a
demo user and org. Log in with the cookie:

```
__session=e2e:user_demo:org_demo:org:admin
```

When you are ready for real auth, billing, and email, create the accounts
listed in the README and fill in `.env`. Every unconfigured service degrades
cleanly to a 503 "not configured" fragment or a log no-op — never a crash.

The day-to-day loop is one command: `make check` (generate + vet + test + build).
